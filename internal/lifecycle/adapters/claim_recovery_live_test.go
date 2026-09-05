package adapters

import (
	"context"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

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
	candidate := seedAdapterCandidate(t, seed, fixture.vault, user.id, "live-request")
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
		RequestID: request.ID, ActorUserID: user.id, AttemptSeq: 1, Purpose: claim.PurposeSelf, Candidate: candidate,
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
