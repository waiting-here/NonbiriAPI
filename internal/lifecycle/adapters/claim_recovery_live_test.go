package adapters

import (
	"context"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestClaimStartupRecoveryDrainsBacklogWithoutDoubleSettlement(t *testing.T) {
	fixture := openAdapterFixture(t)
	ctx := context.Background()
	service, err := claim.New(claim.Dependencies{
		DB: fixture.store.DB(), ReadDB: fixture.store.ReadDB(), Secrets: fixture.vault,
		Accounting: claim.NewLedgerAccounting(), Acceptance: adapterAcceptanceGate{},
		Now: func() time.Time { return time.Unix(adapterTestNow, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	seed := beginAdapterTx(t, fixture.store.DB())
	user := seedAdapterUser(t, seed, "recovery-backlog", 1_000)
	candidate, modelID := seedAdapterCandidate(t, seed, fixture.vault, user.id, "recovery-backlog")
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}
	var requests []string
	for index := range 4 {
		request, err := service.Accept(ctx, claim.AcceptInput{
			UserID: user.id, Route: claim.RouteOpenAIChat, ModelSnapshot: "public/model", AttemptLimit: 1, ReservedMilli: 100,
		})
		if err != nil {
			t.Fatal(err)
		}
		handle, err := service.Claim(ctx, claim.ClaimInput{
			RequestID: request.ID, ActorUserID: user.id, AttemptSeq: 1, Purpose: claim.PurposeSelf, Candidate: candidate, PersonalModelID: modelID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if index%2 == 1 {
			dispatch, err := service.TakeForDispatch(ctx, handle)
			if err != nil {
				t.Fatal(err)
			}
			dispatch.Clear()
		}
		requests = append(requests, request.ID)
	}
	adapter := NewClaimRecovery(service)
	processed := 0
	for pass := 0; ; pass++ {
		if pass > 8 {
			t.Fatal("recovery made no bounded progress")
		}
		result, err := adapter.RecoverBeforeListener(ctx, adapterTestNow+60, lifecycle.WorkerBatchLimit, time.Now().Add(lifecycle.WorkerBudget))
		if err != nil {
			t.Fatal(err)
		}
		processed += result.Processed
		if !result.More {
			break
		}
	}
	if processed != 8 {
		t.Fatalf("recovered %d items, want four claims and four requests", processed)
	}
	for index, id := range requests {
		var state, accounting string
		var status int
		if err := fixture.store.ReadDB().QueryRow(`SELECT state,accounting_state,caller_status FROM logical_requests WHERE id=?`, id).Scan(&state, &accounting, &status); err != nil {
			t.Fatal(err)
		}
		wantAccounting, wantStatus := string(claim.AccountingRelease), 503
		if index%2 == 1 {
			wantAccounting, wantStatus = string(claim.AccountingCommit), 502
		}
		if state != "terminal" || accounting != wantAccounting || status != wantStatus {
			t.Fatalf("request %d: state=%s accounting=%s status=%d", index, state, accounting, status)
		}
	}
	// Both another maintenance visit and a fresh-process adapter must be no-ops.
	for _, again := range []*ClaimRecoveryAdapter{adapter, NewClaimRecovery(service)} {
		if result, err := again.RecoverBeforeListener(ctx, adapterTestNow+120, lifecycle.WorkerBatchLimit, time.Now().Add(lifecycle.WorkerBudget)); err != nil || result != (lifecycle.WorkResult{}) {
			t.Fatalf("repeat recovery: %+v %v", result, err)
		}
	}
	verify := beginAdapterTx(t, fixture.store.DB())
	account, err := ledger.UserAccount(ctx, verify, user.id)
	if err != nil || account.Balance.Decimal() != "800" {
		t.Fatalf("wallet after recovery = %s, %v; want 800", account.Balance.Decimal(), err)
	}
	if err := ledger.ValidateRecovery(ctx, verify); err != nil {
		t.Fatalf("ledger after bounded recovery: %v", err)
	}
}

func TestClaimStartupRecoveryDoesNotCompleteLiveRequestsDuringMaintenance(t *testing.T) {
	fixture := openAdapterFixture(t)
	ctx := context.Background()
	service, err := claim.New(claim.Dependencies{
		DB: fixture.store.DB(), Secrets: fixture.vault, Accounting: claim.NewLedgerAccounting(),
		Acceptance: adapterAcceptanceGate{}, Now: func() time.Time { return time.Unix(adapterTestNow, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	seed := beginAdapterTx(t, fixture.store.DB())
	user := seedAdapterUser(t, seed, "live-request", 1_000)
	candidate, modelID := seedAdapterCandidate(t, seed, fixture.vault, user.id, "live-request")
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}
	adapter := NewClaimRecovery(service)
	run := func() lifecycle.WorkResult {
		t.Helper()
		result, err := adapter.RecoverBeforeListener(ctx, adapterTestNow, 100, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := run(); got != (lifecycle.WorkResult{}) {
		t.Fatalf("empty startup: %+v", got)
	}

	request, err := service.Accept(ctx, claim.AcceptInput{
		UserID: user.id, Route: claim.RouteOpenAIChat, ModelSnapshot: "public/model", AttemptLimit: 1, ReservedMilli: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := service.Claim(ctx, claim.ClaimInput{
		RequestID: request.ID, ActorUserID: user.id, AttemptSeq: 1, Purpose: claim.PurposeSelf, Candidate: candidate, PersonalModelID: modelID,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := service.TakeForDispatch(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Clear()

	for index := 0; index < 3; index++ {
		if got := run(); got != (lifecycle.WorkResult{}) {
			t.Fatalf("maintenance completed a live upstream request: %+v", got)
		}
	}
	if _, err := service.CompleteAttempt(ctx, handle, claim.AttemptOutcome{
		Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
		Usage: connectorcontract.Usage{Present: true, UncachedInputTokens: 13, OutputTokens: 147},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteRequest(ctx, claim.CompleteRequestInput{
		RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200},
		Disposition: claim.AccountingCommit, ActualChargeMilli: 100,
	}); err != nil {
		t.Fatal(err)
	}
	var status, unknown, input, output int
	if err := fixture.store.DB().QueryRow(`SELECT caller_status,usage_unknown,uncached_input_tokens,output_tokens FROM request_logs WHERE logical_request_id=?`, request.ID).
		Scan(&status, &unknown, &input, &output); err != nil {
		t.Fatal(err)
	}
	if status != 200 || unknown != 0 || input != 13 || output != 147 {
		t.Fatalf("completed response lost its true result: status=%d unknown=%d tokens=%d/%d", status, unknown, input, output)
	}
}
