package claim

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestLedgerAccountingSelfLifecycle(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("ledger-self", false)
	fixture.seedLedgerUser(userID, 1_000)
	fixture.useLedgerAccounting()
	fixture.requireRealLedgerState("", 0, 0, 1, 2)

	request := fixture.acceptSelf(userID, 1)
	fixture.requireRealLedgerState(request.ID, 1, 1, 2, 4)
	fixture.requireLedgerBalanceForUser(userID, "900")
	fixture.requireLedgerBalanceForCode(forwardReserveAccountCode, "100")
	fixture.requireOperationTime(ledger.KindForwardReserve, request.ID, request.CreatedAt)

	// Reserve's domain callback is idempotent on replay; Apply then resolves the
	// immutable request source without creating a second operation.
	replayTx := fixture.beginLedgerTx()
	replayCallbackCalls := 0
	err := NewLedgerAccounting().ReserveRequest(context.Background(), replayTx, RequestReservation{
		RequestID: request.ID, UserID: userID, Route: RouteOpenAIChat,
		ReservedMilli: 100, FutureRows: 1,
	}, func(context.Context, *sql.Tx) error {
		replayCallbackCalls++
		return nil
	})
	if err != nil || replayCallbackCalls != 1 {
		t.Fatalf("reserve source replay = calls %d, error %v", replayCallbackCalls, err)
	}
	if err := replayTx.Commit(); err != nil {
		t.Fatalf("commit reserve source replay: %v", err)
	}
	fixture.requireRealLedgerState(request.ID, 1, 1, 2, 4)

	key := fixture.seedKey(userID, "ledger-self")
	model := strings.Repeat("界", MaxUpstreamModelRunes)
	key.candidate.UpstreamModelID = model
	fixture.clock.Store(1_001)
	handle, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: userID, AttemptSeq: 1,
		Purpose: PurposeSelf, Candidate: key.candidate,
	})
	if err != nil {
		t.Fatalf("claim 512-scalar model: %v", err)
	}
	fixture.requireRealLedgerState(request.ID, 1, 1, 2, 4)
	dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatalf("dispatch self request: %v", err)
	}
	dispatch.Clear()
	fixture.requireRealLedgerState(request.ID, 1, 1, 2, 4)
	fixture.clock.Store(1_002)
	if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
		Usage: connectorcontract.Usage{Present: true},
	}); err != nil {
		t.Fatalf("complete self attempt: %v", err)
	}
	fixture.requireRealLedgerState(request.ID, 1, 1, 2, 4)
	var persistedModel string
	if err := fixture.db.QueryRow(`SELECT upstream_model_id FROM request_attempts WHERE claim_id=?`, handle.ClaimID()).Scan(&persistedModel); err != nil {
		t.Fatalf("read 512-scalar attempt model: %v", err)
	}
	if persistedModel != model {
		t.Fatalf("attempt model scalar count = %d, want %d", len([]rune(persistedModel)), MaxUpstreamModelRunes)
	}

	fixture.clock.Store(1_003)
	terminal, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID:         request.ID,
		Caller:            CallerResult{Class: ResultSuccess, Status: 200},
		Disposition:       AccountingCommit,
		ActualChargeMilli: 150,
	})
	if err != nil {
		t.Fatalf("settle self request above reserve: %v", err)
	}
	if terminal.State != RequestTerminal || terminal.AccountingDisposition != AccountingCommit {
		t.Fatalf("terminal self request = %+v", terminal)
	}
	fixture.requireRealLedgerState(request.ID, 0, 0, 3, 7)
	fixture.requireLedgerBalanceForUser(userID, "850")
	fixture.requireLedgerBalanceForCode(forwardReserveAccountCode, "0")
	fixture.requireLedgerBalanceForCode(platformAccountCode, "150")
	fixture.requireOperationTime(ledger.KindForwardSettle, request.ID, request.CreatedAt)

	// ConsumeReserved resolves the frozen logical-request source before it
	// considers the now-terminal domain callback.
	replayTx = fixture.beginLedgerTx()
	terminalCallbackCalled := false
	err = NewLedgerAccounting().CompleteRequest(context.Background(), replayTx, RequestAccounting{
		RequestID: request.ID, UserID: &userID, Route: RouteOpenAIChat,
		ReservedMilli: 100, ActualMilli: 150, RemainingRows: 1,
		Destination: DestinationUser, Disposition: AccountingCommit,
	}, nil, func(context.Context, *sql.Tx) error {
		terminalCallbackCalled = true
		return errors.New("terminal replay callback must not run")
	})
	if err != nil || terminalCallbackCalled {
		t.Fatalf("terminal source replay = called %v, error %v", terminalCallbackCalled, err)
	}
	if err := ledger.ValidateRecovery(context.Background(), replayTx); err != nil {
		t.Fatalf("validate terminal source replay: %v", err)
	}
	if err := replayTx.Rollback(); err != nil {
		t.Fatalf("rollback terminal source replay: %v", err)
	}
	fixture.requireRealLedgerState(request.ID, 0, 0, 3, 7)
}

func TestLedgerAccountingTerminalReleaseAndExternalSettlement(t *testing.T) {
	t.Run("one-attempt charity terminal release", func(t *testing.T) {
		fixture := newClaimFixture(t)
		userID := fixture.seedUser("ledger-charity-one", false)
		fixture.seedLedgerUser(userID, 1_000)
		fixture.useLedgerAccounting()
		request := fixture.acceptCharity(userID, 1)
		fixture.requireRealLedgerState(request.ID, 2, 2, 2, 4)
		fixture.requireLedgerBalanceForUser(userID, "800")
		terminal, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
			RequestID:   request.ID,
			Caller:      CallerResult{Class: ResultCancelled},
			Disposition: AccountingRelease,
		})
		if err != nil {
			t.Fatalf("release one-attempt charity request: %v", err)
		}
		if terminal.AccountingDisposition != AccountingRelease {
			t.Fatalf("released request = %+v", terminal)
		}
		fixture.requireRealLedgerState(request.ID, 0, 0, 3, 6)
		fixture.requireLedgerBalanceForUser(userID, "1000")
		fixture.requireLedgerBalanceForCode(charityReserveAccountCode, "0")
	})

	t.Run("dispatched external destination", func(t *testing.T) {
		fixture := newClaimFixture(t)
		userID := fixture.seedUser("ledger-external", false)
		fixture.seedLedgerUser(userID, 1_000)
		fixture.useLedgerAccounting()
		key := fixture.seedKey(userID, "ledger-external")
		request := fixture.acceptSelf(userID, 1)
		fixture.requireRealLedgerState(request.ID, 1, 1, 2, 4)
		handle, err := fixture.service.Claim(context.Background(), ClaimInput{
			RequestID: request.ID, ActorUserID: userID, AttemptSeq: 1,
			Purpose: PurposeSelf, Candidate: key.candidate,
		})
		if err != nil {
			t.Fatalf("claim external settlement fixture: %v", err)
		}
		fixture.requireRealLedgerState(request.ID, 1, 1, 2, 4)
		dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatalf("dispatch external settlement fixture: %v", err)
		}
		dispatch.Clear()
		fixture.requireRealLedgerState(request.ID, 1, 1, 2, 4)
		if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
			Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
			Usage: connectorcontract.Usage{Present: true},
		}); err != nil {
			t.Fatalf("complete external settlement attempt: %v", err)
		}
		fixture.requireRealLedgerState(request.ID, 1, 1, 2, 4)
		if _, err := fixture.db.Exec(`UPDATE logical_requests
SET user_id=NULL,settlement_destination='external' WHERE id=?`, request.ID); err != nil {
			t.Fatalf("freeze external settlement destination: %v", err)
		}
		fixture.requireRealLedgerState(request.ID, 1, 1, 2, 4)
		terminal, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
			RequestID:         request.ID,
			Caller:            CallerResult{Class: ResultSuccess, Status: 200},
			Disposition:       AccountingCommit,
			ActualChargeMilli: 80,
		})
		if err != nil {
			t.Fatalf("settle external request: %v", err)
		}
		if terminal.UserID != nil || terminal.Destination != DestinationExternal {
			t.Fatalf("external terminal request = %+v", terminal)
		}
		fixture.requireRealLedgerState(request.ID, 0, 0, 3, 7)
		fixture.requireLedgerBalanceForUser(userID, "900")
		fixture.requireLedgerBalanceForCode(externalAccountCode, "-980")
		fixture.requireLedgerBalanceForCode(platformAccountCode, "80")
		fixture.requireLedgerBalanceForCode(forwardReserveAccountCode, "0")
	})
}

func TestLedgerAccountingCharityAttemptLifecycleAtMaximum(t *testing.T) {
	fixture := newClaimFixture(t)
	consumerID := fixture.seedUser("ledger-charity-consumer", false)
	donorIDs := []int64{
		fixture.seedUser("ledger-charity-posted", false),
		fixture.seedUser("ledger-charity-zero", false),
		fixture.seedUser("ledger-charity-unknown", false),
		fixture.seedUser("ledger-charity-deleted", false),
		fixture.seedUser("ledger-charity-undispatched", false),
	}
	fixture.seedLedgerUser(consumerID, 1_000)
	for _, donorID := range donorIDs {
		fixture.seedLedgerUser(donorID, 0)
	}
	fixture.useLedgerAccounting()
	keys := make([]testKey, len(donorIDs))
	donationKeyIDs := make([]int64, len(donorIDs))
	rewards := []int64{11, 0, 17, 13, 19}
	for index, donorID := range donorIDs {
		keys[index] = fixture.seedKey(donorID, "ledger-charity-"+string(rune('a'+index)))
		donationKeyIDs[index] = fixture.seedDonationKey(donorID, keys[index], "ledger-charity", rewards[index])
	}

	request := fixture.acceptCharity(consumerID, MaxAttempts)
	fixture.requireRealLedgerState(request.ID, 101, 101, 2, 4)
	fixture.requireLedgerBalanceForUser(consumerID, "800")
	fixture.requireLedgerBalanceForCode(charityReserveAccountCode, "200")

	claimAndDispatch := func(index int) Handle {
		t.Helper()
		fixture.clock.Add(1)
		handle, err := fixture.service.Claim(context.Background(), ClaimInput{
			RequestID: request.ID, ActorUserID: consumerID, AttemptSeq: index + 1,
			Purpose: PurposeCharity, Candidate: keys[index].candidate,
			DonationKeyID: donationKeyIDs[index],
		})
		if err != nil {
			t.Fatalf("claim charity attempt %d: %v", index+1, err)
		}
		fixture.requireRealLedgerState(request.ID, uint64(101-index), uint64(101-index), 2+boolInt(index > 0), 4+2*boolInt(index > 0))
		dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatalf("dispatch charity attempt %d: %v", index+1, err)
		}
		dispatch.Clear()
		fixture.validateRealLedger()
		return handle
	}

	postedHandle := claimAndDispatch(0)
	postedClaimAt := fixture.clock.Load()
	fixture.clock.Add(1)
	posted, err := fixture.service.CompleteAttempt(context.Background(), postedHandle, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
		Usage: connectorcontract.Usage{Present: true, UncachedInputTokens: 1},
	})
	if err != nil || posted.RewardState != RewardPosted {
		t.Fatalf("posted donor reward = %+v, %v", posted, err)
	}
	fixture.requireRealLedgerState(request.ID, 100, 100, 3, 6)
	fixture.requireLedgerBalanceForUser(donorIDs[0], "11")
	fixture.requireDonationCredit(donorIDs[0], "11")
	fixture.requireOperationTime(ledger.KindDonorReward, postedHandle.ClaimID(), postedClaimAt)

	replayTx := fixture.beginLedgerTx()
	replayCalled := false
	receiverID := donorIDs[0]
	err = NewLedgerAccounting().CompleteAttempt(context.Background(), replayTx, ClaimAccounting{
		RequestID: request.ID, ClaimID: postedHandle.ClaimID(), Purpose: PurposeCharity,
		RewardActualMilli: rewards[0], RewardState: RewardPosted, ReceiverUserID: &receiverID,
	}, func(context.Context, *sql.Tx) error {
		replayCalled = true
		return errors.New("reward replay callback must not run")
	})
	if err != nil || replayCalled {
		t.Fatalf("donor reward source replay = called %v, error %v", replayCalled, err)
	}
	if err := ledger.ValidateRecovery(context.Background(), replayTx); err != nil {
		t.Fatalf("validate donor reward replay: %v", err)
	}
	if err := replayTx.Rollback(); err != nil {
		t.Fatalf("rollback donor reward replay: %v", err)
	}

	zeroHandle := claimAndDispatch(1)
	fixture.clock.Add(1)
	zero, err := fixture.service.CompleteAttempt(context.Background(), zeroHandle, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
		Usage: connectorcontract.Usage{Present: true},
	})
	if err != nil || zero.RewardState != RewardZero {
		t.Fatalf("zero donor reward = %+v, %v", zero, err)
	}
	fixture.requireRealLedgerState(request.ID, 99, 99, 3, 6)

	unknownHandle := claimAndDispatch(2)
	fixture.clock.Add(1)
	unknown, err := fixture.service.CompleteAttempt(context.Background(), unknownHandle, AttemptOutcome{
		Kind: ResultSynthetic, UpstreamStatus: 502, ResponseStarted: true,
	})
	if err != nil || unknown.RewardState != RewardZero || unknown.Usage.Present {
		t.Fatalf("unknown-usage donor reward = %+v, %v", unknown, err)
	}
	fixture.requireRealLedgerState(request.ID, 98, 98, 3, 6)

	deletedHandle := claimAndDispatch(3)
	fixture.deleteUserWithDonationTombstones(donorIDs[3])
	fixture.validateRealLedger()
	fixture.clock.Add(1)
	deleted, err := fixture.service.CompleteAttempt(context.Background(), deletedHandle, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
		Usage: connectorcontract.Usage{Present: true},
	})
	if err != nil || deleted.RewardState != RewardReceiverDeleted {
		t.Fatalf("deleted-receiver donor reward = %+v, %v", deleted, err)
	}
	fixture.requireRealLedgerState(request.ID, 97, 97, 3, 6)

	fixture.clock.Add(1)
	releaseHandle, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: consumerID, AttemptSeq: 5,
		Purpose: PurposeCharity, Candidate: keys[4].candidate,
		DonationKeyID: donationKeyIDs[4],
	})
	if err != nil {
		t.Fatalf("claim undispatched charity attempt: %v", err)
	}
	fixture.requireRealLedgerState(request.ID, 97, 97, 3, 6)
	released, err := fixture.service.ReleaseUndispatched(context.Background(), releaseHandle)
	if err != nil || released.RewardState != RewardNotDue {
		t.Fatalf("release undispatched charity attempt = %+v, %v", released, err)
	}
	fixture.requireRealLedgerState(request.ID, 96, 96, 3, 6)

	fixture.clock.Add(1)
	terminal, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID:         request.ID,
		Caller:            CallerResult{Class: ResultSuccess, Status: 200},
		Disposition:       AccountingCommit,
		ActualChargeMilli: 23,
	})
	if err != nil {
		t.Fatalf("settle maximum-attempt charity request: %v", err)
	}
	if terminal.AccountingDisposition != AccountingCommit {
		t.Fatalf("terminal charity request = %+v", terminal)
	}
	fixture.requireRealLedgerState(request.ID, 0, 0, 4, 9)
	fixture.requireLedgerBalanceForUser(consumerID, "977")
	fixture.requireLedgerBalanceForCode(charityReserveAccountCode, "0")
	fixture.requireLedgerBalanceForCode(platformAccountCode, "23")
	fixture.requireOperationCount(ledger.KindDonorReward, postedHandle.ClaimID(), 1)
	fixture.requireOperationCount(ledger.KindCharitySettle, request.ID, 1)
}

func TestLedgerAccountingFailuresRollbackOuterTransaction(t *testing.T) {
	t.Run("acceptance reserve operation", func(t *testing.T) {
		fixture := newClaimFixture(t)
		userID := fixture.seedUser("ledger-rollback-accept", false)
		fixture.seedLedgerUser(userID, 50)
		fixture.useLedgerAccounting()
		_, err := fixture.service.Accept(context.Background(), AcceptInput{
			UserID: userID, Route: RouteOpenAIChat, ModelSnapshot: "rollback/model",
			AttemptLimit: 1, ReservedMilli: 100,
		})
		if !errors.Is(err, ledger.ErrInsufficientBalance) {
			t.Fatalf("acceptance rollback error = %v, want insufficient balance", err)
		}
		var requests int
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM logical_requests`).Scan(&requests); err != nil {
			t.Fatalf("count rolled-back requests: %v", err)
		}
		if requests != 0 {
			t.Fatalf("rolled-back acceptance left %d requests", requests)
		}
		fixture.requireRealLedgerState("", 0, 0, 1, 2)
		fixture.requireLedgerBalanceForUser(userID, "50")
		fixture.requireLedgerBalanceForCode(forwardReserveAccountCode, "0")
	})

	t.Run("attempt persistence", func(t *testing.T) {
		fixture := newClaimFixture(t)
		consumerID := fixture.seedUser("ledger-rollback-attempt-consumer", false)
		donorID := fixture.seedUser("ledger-rollback-attempt-donor", false)
		fixture.seedLedgerUser(consumerID, 1_000)
		fixture.seedLedgerUser(donorID, 0)
		fixture.useLedgerAccounting()
		key := fixture.seedKey(donorID, "ledger-rollback-attempt")
		donationKeyID := fixture.seedDonationKey(donorID, key, "ledger-rollback-attempt", 9)
		request := fixture.acceptCharity(consumerID, 1)
		handle, err := fixture.service.Claim(context.Background(), ClaimInput{
			RequestID: request.ID, ActorUserID: consumerID, AttemptSeq: 1,
			Purpose: PurposeCharity, Candidate: key.candidate, DonationKeyID: donationKeyID,
		})
		if err != nil {
			t.Fatalf("claim attempt rollback fixture: %v", err)
		}
		dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatalf("dispatch attempt rollback fixture: %v", err)
		}
		dispatch.Clear()
		fixture.charity.mu.Lock()
		fixture.charity.completeAttemptErr = errLedgerAccountingInjected
		fixture.charity.mu.Unlock()
		_, err = fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
			Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
			Usage: connectorcontract.Usage{Present: true},
		})
		if !errors.Is(err, errLedgerAccountingInjected) {
			t.Fatalf("attempt rollback error = %v", err)
		}
		fixture.requireRealLedgerState(request.ID, 2, 2, 2, 4)
		fixture.requireOperationCount(ledger.KindDonorReward, handle.ClaimID(), 0)
		var state string
		if err := fixture.db.QueryRow(`SELECT state FROM dispatch_claims WHERE id=?`, handle.ClaimID()).Scan(&state); err != nil {
			t.Fatalf("read rolled-back claim: %v", err)
		}
		if state != string(StateDispatched) {
			t.Fatalf("rolled-back claim state = %q", state)
		}
	})

	t.Run("terminal after unused release", func(t *testing.T) {
		fixture := newClaimFixture(t)
		userID := fixture.seedUser("ledger-rollback-terminal", false)
		fixture.seedLedgerUser(userID, 1_000)
		fixture.useLedgerAccounting()
		request := fixture.acceptCharity(userID, 1)
		fixture.charity.mu.Lock()
		fixture.charity.completeRequestErr = errLedgerAccountingInjected
		fixture.charity.mu.Unlock()
		_, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
			RequestID:   request.ID,
			Caller:      CallerResult{Class: ResultCancelled},
			Disposition: AccountingRelease,
		})
		if !errors.Is(err, errLedgerAccountingInjected) {
			t.Fatalf("terminal rollback error = %v", err)
		}
		fixture.requireRealLedgerState(request.ID, 2, 2, 2, 4)
		fixture.requireLedgerBalanceForUser(userID, "800")
		fixture.requireLedgerBalanceForCode(charityReserveAccountCode, "200")
		var state string
		if err := fixture.db.QueryRow(`SELECT state FROM logical_requests WHERE id=?`, request.ID).Scan(&state); err != nil {
			t.Fatalf("read rolled-back request: %v", err)
		}
		if state != string(RequestAccepted) {
			t.Fatalf("rolled-back request state = %q", state)
		}
	})
}

func TestLedgerAccountingDiscoveryUsesEmptyPhysicalModel(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("ledger-discovery", false)
	fixture.seedLedgerUser(userID, 1_000)
	fixture.useLedgerAccounting()
	key := fixture.seedKey(userID, "ledger-discovery")
	discoveryCandidate := key.candidate
	discoveryCandidate.UpstreamModelID = ""
	request, handle, err := fixture.service.ClaimDiscovery(context.Background(), DiscoveryClaimInput{
		ActorUserID: userID, Candidate: discoveryCandidate,
	})
	if err != nil {
		t.Fatalf("claim discovery with empty model: %v", err)
	}
	fixture.requireRealLedgerState(request.ID, 0, 0, 1, 2)
	dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatalf("dispatch discovery: %v", err)
	}
	dispatch.Clear()
	fixture.requireRealLedgerState(request.ID, 0, 0, 1, 2)
	if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
		Usage: connectorcontract.Usage{Present: true},
	}); err != nil {
		t.Fatalf("complete discovery attempt: %v", err)
	}
	fixture.requireRealLedgerState(request.ID, 0, 0, 1, 2)
	var logModel, attemptModel string
	if err := fixture.db.QueryRow(`SELECT l.upstream_model_id,a.upstream_model_id
FROM request_logs l JOIN request_attempts a ON a.request_log_id=l.id
WHERE l.logical_request_id=?`, request.ID).Scan(&logModel, &attemptModel); err != nil {
		t.Fatalf("read discovery physical models: %v", err)
	}
	if logModel != "" || attemptModel != "" {
		t.Fatalf("discovery physical models = log %q attempt %q", logModel, attemptModel)
	}
	if _, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID:   request.ID,
		Caller:      CallerResult{Class: ResultSuccess, Status: 200},
		Disposition: AccountingNone,
	}); err != nil {
		t.Fatalf("complete discovery request: %v", err)
	}
	fixture.requireRealLedgerState(request.ID, 0, 0, 1, 2)

	ordinary := fixture.acceptSelf(userID, 1)
	if _, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: ordinary.ID, ActorUserID: userID, AttemptSeq: 1,
		Purpose: PurposeSelf, Candidate: discoveryCandidate,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ordinary empty-model claim error = %v, want invalid input", err)
	}
	fixture.requireRealLedgerState(ordinary.ID, 1, 1, 2, 4)
	if _, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID:   ordinary.ID,
		Caller:      CallerResult{Class: ResultCancelled},
		Disposition: AccountingRelease,
	}); err != nil {
		t.Fatalf("release ordinary empty-model fixture: %v", err)
	}
	fixture.requireRealLedgerState(ordinary.ID, 0, 0, 3, 6)
}

var errLedgerAccountingInjected = errors.New("injected ledger-accounting domain failure")

func (f *claimFixture) useLedgerAccounting() {
	f.t.Helper()
	service, err := New(Dependencies{
		DB: f.db, Secrets: f.codec, Accounting: NewLedgerAccounting(), Charity: f.charity,
		Acceptance: allowAcceptanceGate{},
		Now:        func() time.Time { return time.Unix(f.clock.Load(), 0) },
	})
	if err != nil {
		f.t.Fatalf("new claim service with ledger accounting: %v", err)
	}
	f.service = service
}

func (f *claimFixture) seedLedgerUser(userID, balanceMilli int64) int64 {
	f.t.Helper()
	tx := f.beginLedgerTx()
	account, err := ledger.CreateUserAccount(context.Background(), tx, userID, f.clock.Load())
	if err != nil {
		tx.Rollback()
		f.t.Fatalf("create ledger user account: %v", err)
	}
	if balanceMilli != 0 {
		external, err := ledger.CodedAccount(context.Background(), tx, externalAccountCode)
		if err != nil {
			tx.Rollback()
			f.t.Fatalf("read funding external account: %v", err)
		}
		operationID, err := db.GenerateOpaqueID("op_")
		if err != nil {
			tx.Rollback()
			f.t.Fatalf("generate funding operation: %v", err)
		}
		plan, err := ledger.NewAdminUserAdjustment(
			ledger.Meta{OperationID: operationID, ActorUserID: userID, CreatedAt: f.clock.Load()},
			account.ID, external.ID, ledger.AmountFromMilli(balanceMilli), 0, ledger.Amount{}, "test funding",
		)
		if err != nil {
			tx.Rollback()
			f.t.Fatalf("build funding operation: %v", err)
		}
		if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
			tx.Rollback()
			f.t.Fatalf("apply funding operation: %v", err)
		}
	}
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		tx.Rollback()
		f.t.Fatalf("validate seeded ledger user: %v", err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatalf("commit ledger user: %v", err)
	}
	return account.ID
}

func (f *claimFixture) beginLedgerTx() *sql.Tx {
	f.t.Helper()
	tx, err := f.db.BeginTx(context.Background(), nil)
	if err != nil {
		f.t.Fatalf("begin ledger transaction: %v", err)
	}
	return tx
}

func (f *claimFixture) validateRealLedger() {
	f.t.Helper()
	tx := f.beginLedgerTx()
	defer tx.Rollback()
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		f.t.Fatalf("validate real ledger recovery: %v", err)
	}
}

func (f *claimFixture) requireRealLedgerState(
	requestID string,
	requestRows, globalRows uint64,
	operations, entries int,
) {
	f.t.Helper()
	tx := f.beginLedgerTx()
	defer tx.Rollback()
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		f.t.Fatalf("validate real ledger recovery: %v", err)
	}
	capacity, err := ledger.ReadCapacity(context.Background(), tx)
	if err != nil {
		f.t.Fatalf("read real ledger capacity: %v", err)
	}
	if !capacity.ReservedFutureRows.Big().IsUint64() || capacity.ReservedFutureRows.Big().Uint64() != globalRows {
		f.t.Fatalf("global reserved rows = %s, want %d", capacity.ReservedFutureRows.Decimal(), globalRows)
	}
	if requestID != "" {
		var raw []byte
		if err := tx.QueryRow(`SELECT ledger_rows_remaining FROM logical_requests WHERE id=?`, requestID).Scan(&raw); err != nil {
			f.t.Fatalf("read request reserved rows: %v", err)
		}
		value, err := db.DecodeU128(raw)
		if err != nil || !value.Big().IsUint64() || value.Big().Uint64() != requestRows {
			f.t.Fatalf("request reserved rows = %x/%v, want %d", raw, err, requestRows)
		}
	}
	var operationCount, entryCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM credit_operations`).Scan(&operationCount); err != nil {
		f.t.Fatalf("count credit operations: %v", err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM credit_entries`).Scan(&entryCount); err != nil {
		f.t.Fatalf("count credit entries: %v", err)
	}
	if capacity.LastLedgerSeq != int64(operations) || operationCount != operations || entryCount != entries {
		f.t.Fatalf("ledger rows = seq %d operations %d entries %d, want %d/%d",
			capacity.LastLedgerSeq, operationCount, entryCount, operations, entries)
	}
	rows, err := tx.Query(`SELECT id FROM credit_operations`)
	if err != nil {
		f.t.Fatalf("read accounting operation identities: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var operationID string
		if err := rows.Scan(&operationID); err != nil {
			f.t.Fatalf("scan accounting operation identity: %v", err)
		}
		if !db.ValidateOpaqueID(operationID, "op_") {
			f.t.Fatalf("non-canonical accounting operation identity %q", operationID)
		}
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("iterate accounting operation identities: %v", err)
	}
}

func (f *claimFixture) requireLedgerBalanceForUser(userID int64, want string) {
	f.t.Helper()
	tx := f.beginLedgerTx()
	defer tx.Rollback()
	account, err := ledger.UserAccount(context.Background(), tx, userID)
	if err != nil {
		f.t.Fatalf("read user ledger balance: %v", err)
	}
	if account.Balance.Decimal() != want {
		f.t.Fatalf("user %d ledger balance = %s, want %s", userID, account.Balance.Decimal(), want)
	}
}

func (f *claimFixture) requireLedgerBalanceForCode(code, want string) {
	f.t.Helper()
	tx := f.beginLedgerTx()
	defer tx.Rollback()
	account, err := ledger.CodedAccount(context.Background(), tx, code)
	if err != nil {
		f.t.Fatalf("read coded ledger balance %q: %v", code, err)
	}
	if account.Balance.Decimal() != want {
		f.t.Fatalf("coded ledger balance %q = %s, want %s", code, account.Balance.Decimal(), want)
	}
}

func (f *claimFixture) requireDonationCredit(userID int64, want string) {
	f.t.Helper()
	var raw []byte
	if err := f.db.QueryRow(`SELECT donation_credit_mag FROM users WHERE id=?`, userID).Scan(&raw); err != nil {
		f.t.Fatalf("read donation credit: %v", err)
	}
	value, err := db.DecodeU128(raw)
	if err != nil || value.Decimal() != want {
		f.t.Fatalf("donation credit = %x/%v, want %s", raw, err, want)
	}
}

func (f *claimFixture) requireOperationTime(kind ledger.Kind, sourceID string, want int64) {
	f.t.Helper()
	var createdAt int64
	if err := f.db.QueryRow(`SELECT created_at FROM credit_operations WHERE kind=? AND source_id=?`, kind, sourceID).Scan(&createdAt); err != nil {
		f.t.Fatalf("read %s operation time: %v", kind, err)
	}
	if createdAt != want {
		f.t.Fatalf("%s operation time = %d, want authoritative %d", kind, createdAt, want)
	}
}

func (f *claimFixture) requireOperationCount(kind ledger.Kind, sourceID string, want int) {
	f.t.Helper()
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind=? AND source_id=?`, kind, sourceID).Scan(&count); err != nil {
		f.t.Fatalf("count %s operations: %v", kind, err)
	}
	if count != want {
		f.t.Fatalf("%s operation count = %d, want %d", kind, count, want)
	}
}
