package claim

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestCharityClaimsSettleIndependently(t *testing.T) {
	fixture := newClaimFixture(t)
	consumerID := fixture.seedUser("charity-consumer", false)
	donorIDs := []int64{
		fixture.seedUser("charity-donor-posted", false),
		fixture.seedUser("charity-donor-zero", false),
		fixture.seedUser("charity-donor-deleted", false),
		fixture.seedUser("charity-donor-unknown", false),
		fixture.seedUser("charity-donor-release", false),
	}
	keys := make([]testKey, len(donorIDs))
	donationKeyIDs := make([]int64, len(donorIDs))
	rewards := []int64{11, 0, 13, 17, 19}
	for index, donorID := range donorIDs {
		keys[index] = fixture.seedKey(donorID, "charity-key-"+string(rune('a'+index)))
		donationKeyIDs[index] = fixture.seedDonationKey(donorID, keys[index], "charity", rewards[index])
	}
	request := fixture.acceptCharity(consumerID, len(keys))
	fixture.requireCapacity(request.ID, 6, 6, 1)

	claimAndDispatch := func(index int) Handle {
		t.Helper()
		handle, err := fixture.service.Claim(context.Background(), ClaimInput{
			RequestID: request.ID, ActorUserID: consumerID, AttemptSeq: index + 1,
			Purpose: PurposeCharity, Candidate: keys[index].candidate,
			DonationKeyID: donationKeyIDs[index],
		})
		if err != nil {
			t.Fatalf("claim charity attempt %d: %v", index+1, err)
		}
		fixture.charity.mu.Lock()
		seamInput := fixture.charity.claims[len(fixture.charity.claims)-1]
		fixture.charity.mu.Unlock()
		if seamInput.UpstreamModelID != keys[index].candidate.UpstreamModelID {
			t.Fatalf("charity seam upstream model = %q, want %q",
				seamInput.UpstreamModelID, keys[index].candidate.UpstreamModelID)
		}
		dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatalf("dispatch charity attempt %d: %v", index+1, err)
		}
		dispatch.Clear()
		return handle
	}

	postedHandle := claimAndDispatch(0)
	posted, err := fixture.service.CompleteAttempt(context.Background(), postedHandle, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
		Usage: connectorcontract.Usage{UncachedInputTokens: 3, OutputTokens: 2, Present: true},
	})
	if err != nil {
		t.Fatalf("complete posted reward: %v", err)
	}
	if posted.RewardState != RewardPosted || posted.RewardActualMilli != rewards[0] {
		t.Fatalf("posted reward = %+v", posted)
	}
	fixture.requireCapacity(request.ID, 5, 5, 2)

	zeroHandle := claimAndDispatch(1)
	zero, err := fixture.service.CompleteAttempt(context.Background(), zeroHandle, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
		Usage: connectorcontract.Usage{Present: true},
	})
	if err != nil {
		t.Fatalf("complete zero reward: %v", err)
	}
	if zero.RewardState != RewardZero || zero.RewardActualMilli != 0 {
		t.Fatalf("zero reward = %+v", zero)
	}
	fixture.requireCapacity(request.ID, 4, 4, 2)

	deletedHandle := claimAndDispatch(2)
	fixture.deleteUserWithDonationTombstones(donorIDs[2])
	deleted, err := fixture.service.CompleteAttempt(context.Background(), deletedHandle, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
		Usage: connectorcontract.Usage{Present: true},
	})
	if err != nil {
		t.Fatalf("complete deleted receiver reward: %v", err)
	}
	if deleted.RewardState != RewardReceiverDeleted || deleted.RewardActualMilli != rewards[2] {
		t.Fatalf("deleted receiver reward = %+v", deleted)
	}
	fixture.requireCapacity(request.ID, 3, 3, 2)

	unknownHandle := claimAndDispatch(3)
	unknown, err := fixture.service.CompleteAttempt(context.Background(), unknownHandle, AttemptOutcome{
		Kind: ResultSynthetic, UpstreamStatus: 502, ProtocolSuccess: false, ResponseStarted: true,
	})
	if err != nil {
		t.Fatalf("complete unknown usage reward: %v", err)
	}
	if unknown.RewardState != RewardZero || unknown.RewardActualMilli != 0 || unknown.Usage.Present {
		t.Fatalf("unknown usage reward = %+v", unknown)
	}
	fixture.requireCapacity(request.ID, 2, 2, 2)

	releaseHandle, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: consumerID, AttemptSeq: 5,
		Purpose: PurposeCharity, Candidate: keys[4].candidate, DonationKeyID: donationKeyIDs[4],
	})
	if err != nil {
		t.Fatalf("claim undispatched charity attempt: %v", err)
	}
	released, err := fixture.service.ReleaseUndispatched(context.Background(), releaseHandle)
	if err != nil {
		t.Fatalf("release undispatched charity attempt: %v", err)
	}
	if released.State != StateReleased || released.RewardState != RewardNotDue {
		t.Fatalf("released charity attempt = %+v", released)
	}
	if replay, err := fixture.service.ReleaseUndispatched(context.Background(), releaseHandle); err != nil || replay.State != StateReleased {
		t.Fatalf("idempotent charity release = %+v, %v", replay, err)
	}
	fixture.requireCapacity(request.ID, 1, 1, 2)

	terminal, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID:   request.ID,
		Caller:      CallerResult{Class: ResultSuccess, Status: 200},
		Disposition: AccountingCommit, ActualChargeMilli: 23,
	})
	if err != nil {
		t.Fatalf("complete charity request: %v", err)
	}
	if terminal.State != RequestTerminal || terminal.AccountingDisposition != AccountingCommit {
		t.Fatalf("terminal charity request = %+v", terminal)
	}
	fixture.requireCapacity(request.ID, 0, 0, 3)
	fixture.requireCallerMirror(request.ID, CallerResult{Class: ResultSuccess, Status: 200}, terminal.TerminalAt)
	fixture.accounting.mu.Lock()
	defer fixture.accounting.mu.Unlock()
	if len(fixture.accounting.claimCompletions) != 4 || len(fixture.accounting.claimReleases) != 1 ||
		len(fixture.accounting.requests) != 1 {
		t.Fatalf("charity accounting callbacks = completed %d released %d requests %d",
			len(fixture.accounting.claimCompletions), len(fixture.accounting.claimReleases),
			len(fixture.accounting.requests))
	}
	requestAccounting := fixture.accounting.requests[0]
	if requestAccounting.RemainingRows != 1 || requestAccounting.ActualMilli != 23 {
		t.Fatalf("request accounting = %+v", requestAccounting)
	}
}

func TestCharityTerminalReleasesUnusedRowsBeforeFinalConsumption(t *testing.T) {
	fixture := newClaimFixture(t)
	consumerID := fixture.seedUser("unused-charity-capacity", false)
	request := fixture.acceptCharity(consumerID, 3)
	fixture.requireCapacity(request.ID, 4, 4, 1)

	terminal, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID:   request.ID,
		Caller:      CallerResult{Class: ResultCancelled},
		Disposition: AccountingRelease,
	})
	if err != nil {
		t.Fatalf("release unused charity request: %v", err)
	}
	if terminal.State != RequestTerminal || terminal.AccountingDisposition != AccountingRelease {
		t.Fatalf("released charity request = %+v", terminal)
	}
	fixture.requireCapacity(request.ID, 0, 0, 2)
	if fixture.charityFactCount("request", request.ID) != 1 {
		t.Fatal("charity terminal facts were not persisted with final consumption")
	}
	fixture.accounting.mu.Lock()
	defer fixture.accounting.mu.Unlock()
	if len(fixture.accounting.requests) != 1 || fixture.accounting.requests[0].RemainingRows != 4 {
		t.Fatalf("request accounting = %+v", fixture.accounting.requests)
	}
}

func TestCapacityCallbacksRollbackTheirDomainMutations(t *testing.T) {
	t.Run("acceptance", func(t *testing.T) {
		fixture := newClaimFixture(t)
		consumerID := fixture.seedUser("rollback-accept", false)
		fixture.accounting.setFailPoint("reserve_after_persistence")
		decisionNow := fixture.clock.Load()
		_, err := fixture.service.Accept(context.Background(), AcceptInput{
			UserID:             consumerID,
			Route:              RouteCharityChat,
			ModelSnapshot:      "[公益]rollback/model",
			AttemptLimit:       2,
			ReservedMilli:      20,
			CharityModelID:     1,
			CharityDecisionNow: &decisionNow,
		})
		requireErrorIs(t, err, errTestAccounting)
		var requests, logs, charityFacts, last int
		var reservedRaw []byte
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM logical_requests`).Scan(&requests); err != nil {
			t.Fatalf("count rolled-back requests: %v", err)
		}
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM request_logs`).Scan(&logs); err != nil {
			t.Fatalf("count rolled-back logs: %v", err)
		}
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM claim_test_charity_facts`).Scan(&charityFacts); err != nil {
			t.Fatalf("count rolled-back charity facts: %v", err)
		}
		if err := fixture.db.QueryRow(`SELECT last_ledger_seq,reserved_future_rows FROM credit_capacity WHERE id=1`).Scan(&last, &reservedRaw); err != nil {
			t.Fatalf("read rolled-back capacity: %v", err)
		}
		reserved, ok := decodeSmallU128(reservedRaw)
		if requests != 0 || logs != 0 || charityFacts != 0 || last != 0 || !ok || reserved != 0 {
			t.Fatalf("accept rollback left requests=%d logs=%d charity=%d last=%d reserved=%d/%t",
				requests, logs, charityFacts, last, reserved, ok)
		}
	})

	t.Run("undispatched claim", func(t *testing.T) {
		fixture := newClaimFixture(t)
		consumerID := fixture.seedUser("rollback-release-consumer", false)
		donorID := fixture.seedUser("rollback-release-donor", false)
		key := fixture.seedKey(donorID, "rollback-release")
		donationKeyID := fixture.seedDonationKey(donorID, key, "rollback-release", 5)
		request := fixture.acceptCharity(consumerID, 2)
		handle, err := fixture.service.Claim(context.Background(), ClaimInput{
			RequestID: request.ID, ActorUserID: consumerID, AttemptSeq: 1,
			Purpose: PurposeCharity, Candidate: key.candidate, DonationKeyID: donationKeyID,
		})
		if err != nil {
			t.Fatalf("claim rollback release fixture: %v", err)
		}
		fixture.accounting.setFailPoint("release_after_persistence")
		_, err = fixture.service.ReleaseUndispatched(context.Background(), handle)
		requireErrorIs(t, err, errTestAccounting)
		fixture.requireCapacity(request.ID, 3, 3, 1)
		if fixture.charityFactCount("release", handle.ClaimID()) != 0 {
			t.Fatal("failed release retained charity facts")
		}
		var state string
		var secretRef sql.NullInt64
		if err := fixture.db.QueryRow(`SELECT state,secret_ref_id FROM dispatch_claims WHERE id=?`,
			handle.ClaimID()).Scan(&state, &secretRef); err != nil {
			t.Fatalf("read rolled-back release: %v", err)
		}
		if state != string(StateClaimed) || !secretRef.Valid {
			t.Fatalf("rolled-back release state=%q secret=%+v", state, secretRef)
		}
	})

	t.Run("dispatched attempt", func(t *testing.T) {
		fixture := newClaimFixture(t)
		consumerID := fixture.seedUser("rollback-attempt-consumer", false)
		donorID := fixture.seedUser("rollback-attempt-donor", false)
		key := fixture.seedKey(donorID, "rollback-attempt")
		donationKeyID := fixture.seedDonationKey(donorID, key, "rollback-attempt", 7)
		request := fixture.acceptCharity(consumerID, 1)
		handle, err := fixture.service.Claim(context.Background(), ClaimInput{
			RequestID: request.ID, ActorUserID: consumerID, AttemptSeq: 1,
			Purpose: PurposeCharity, Candidate: key.candidate, DonationKeyID: donationKeyID,
		})
		if err != nil {
			t.Fatalf("claim rollback attempt fixture: %v", err)
		}
		dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatalf("dispatch rollback attempt fixture: %v", err)
		}
		dispatch.Clear()
		fixture.charity.mu.Lock()
		fixture.charity.completeAttemptErr = errTestAccounting
		fixture.charity.mu.Unlock()
		_, err = fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
			Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
			Usage: connectorcontract.Usage{Present: true},
		})
		requireErrorIs(t, err, errTestAccounting)
		fixture.requireCapacity(request.ID, 2, 2, 1)
		if fixture.charityFactCount("attempt", handle.ClaimID()) != 0 {
			t.Fatal("failed charity callback retained attempt facts")
		}
		fixture.charity.mu.Lock()
		fixture.charity.completeAttemptErr = nil
		fixture.charity.mu.Unlock()
		fixture.accounting.setFailPoint("attempt_after_persistence")
		_, err = fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
			Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
			Usage: connectorcontract.Usage{Present: true},
		})
		requireErrorIs(t, err, errTestAccounting)
		fixture.requireCapacity(request.ID, 2, 2, 1)
		if fixture.charityFactCount("attempt", handle.ClaimID()) != 0 {
			t.Fatal("failed attempt retained charity facts")
		}
		var state string
		var attempts int
		if err := fixture.db.QueryRow(`SELECT state FROM dispatch_claims WHERE id=?`, handle.ClaimID()).Scan(&state); err != nil {
			t.Fatalf("read rolled-back attempt state: %v", err)
		}
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM request_attempts WHERE claim_id=?`, handle.ClaimID()).Scan(&attempts); err != nil {
			t.Fatalf("count rolled-back attempts: %v", err)
		}
		if state != string(StateDispatched) || attempts != 0 {
			t.Fatalf("rolled-back attempt state=%q rows=%d", state, attempts)
		}
	})

	t.Run("request terminal", func(t *testing.T) {
		fixture := newClaimFixture(t)
		consumerID := fixture.seedUser("rollback-request", false)
		request := fixture.acceptCharity(consumerID, 3)
		fixture.charity.mu.Lock()
		fixture.charity.completeRequestErr = errTestAccounting
		fixture.charity.mu.Unlock()
		_, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
			RequestID: request.ID, Caller: CallerResult{Class: ResultCancelled},
			Disposition: AccountingRelease,
		})
		requireErrorIs(t, err, errTestAccounting)
		fixture.requireCapacity(request.ID, 4, 4, 1)
		if fixture.charityFactCount("request", request.ID) != 0 {
			t.Fatal("failed charity callback retained request facts")
		}
		fixture.charity.mu.Lock()
		fixture.charity.completeRequestErr = nil
		fixture.charity.mu.Unlock()
		fixture.accounting.setFailPoint("request_after_unused")
		_, err = fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
			RequestID: request.ID, Caller: CallerResult{Class: ResultCancelled},
			Disposition: AccountingRelease,
		})
		requireErrorIs(t, err, errTestAccounting)
		fixture.requireCapacity(request.ID, 4, 4, 1)
		if fixture.charityFactCount("request", request.ID) != 0 {
			t.Fatal("failed terminal transition retained charity facts")
		}
		var state, accounting string
		if err := fixture.db.QueryRow(`SELECT state,accounting_state FROM logical_requests WHERE id=?`,
			request.ID).Scan(&state, &accounting); err != nil {
			t.Fatalf("read rolled-back request: %v", err)
		}
		if state != string(RequestAccepted) || accounting != string(AccountingReserved) {
			t.Fatalf("rolled-back request state=%q accounting=%q", state, accounting)
		}
		fixture.requireCallerMirrorOpen(request.ID)

		fixture.accounting.setFailPoint("request_after_terminal")
		_, err = fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
			RequestID: request.ID, Caller: CallerResult{Class: ResultCancelled},
			Disposition: AccountingRelease,
		})
		requireErrorIs(t, err, errTestAccounting)
		fixture.requireCapacity(request.ID, 4, 4, 1)
		if fixture.charityFactCount("request", request.ID) != 0 {
			t.Fatal("post-terminal adapter failure retained charity facts")
		}
		fixture.requireCallerMirrorOpen(request.ID)
	})
}

func TestCharityExpiryOrderingIsOwnedByClaimTransactionAdapter(t *testing.T) {
	fixture := newClaimFixture(t)
	consumerID := fixture.seedUser("expiry-consumer", false)
	donorID := fixture.seedUser("expiry-donor", false)
	key := fixture.seedKey(donorID, "expiry")
	donationKeyID := fixture.seedDonationKey(donorID, key, "expiry", 5)
	request := fixture.acceptCharity(consumerID, 2)

	fixture.charity.mu.Lock()
	config := fixture.charity.configs[donationKeyID]
	config.claimErr = ErrNotFound
	fixture.charity.configs[donationKeyID] = config
	fixture.charity.mu.Unlock()
	_, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: consumerID, AttemptSeq: 1,
		Purpose: PurposeCharity, Candidate: key.candidate, DonationKeyID: donationKeyID,
	})
	requireErrorIs(t, err, ErrNotFound)

	fixture.charity.mu.Lock()
	config.claimErr = nil
	fixture.charity.configs[donationKeyID] = config
	fixture.charity.mu.Unlock()
	handle, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: consumerID, AttemptSeq: 1,
		Purpose: PurposeCharity, Candidate: key.candidate, DonationKeyID: donationKeyID,
	})
	if err != nil {
		t.Fatalf("claim before expiry: %v", err)
	}
	fixture.charity.mu.Lock()
	config.claimErr = ErrNotFound
	fixture.charity.configs[donationKeyID] = config
	fixture.charity.mu.Unlock()
	dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatalf("claim winner dispatch after expiry: %v", err)
	}
	dispatch.Clear()
}

func TestDiscoveryClaimHasNoEconomicReservation(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("discovery", false)
	key := fixture.seedKey(userID, "discovery")
	discoveryCandidate := key.candidate
	discoveryCandidate.UpstreamModelID = ""
	service, err := New(Dependencies{
		DB:      fixture.db,
		Secrets: fixture.codec,
		Now:     fixture.service.now,
	})
	if err != nil {
		t.Fatalf("new discovery-only claim service: %v", err)
	}
	request, handle, err := service.ClaimDiscovery(context.Background(), DiscoveryClaimInput{
		ActorUserID: userID,
		Candidate:   discoveryCandidate,
	})
	if err != nil {
		t.Fatalf("claim discovery: %v", err)
	}
	if request.AccountingDisposition != AccountingNone || request.AttemptLimit != 1 {
		t.Fatalf("discovery request = %+v", request)
	}
	if request.ModelSnapshot != "" || handle.Target().UpstreamModel() != "" {
		t.Fatalf("discovery model was not empty: request=%q target=%q",
			request.ModelSnapshot, handle.Target().UpstreamModel())
	}
	var requestModel, logModel string
	if err := fixture.db.QueryRow(`SELECT r.model_snapshot,l.upstream_model_id
FROM logical_requests r JOIN request_logs l ON l.logical_request_id=r.id WHERE r.id=?`,
		request.ID).Scan(&requestModel, &logModel); err != nil {
		t.Fatalf("read persisted discovery models: %v", err)
	}
	if requestModel != "" || logModel != "" {
		t.Fatalf("persisted discovery models = request %q log %q", requestModel, logModel)
	}
	var accounting string
	var reserved int64
	var remaining []byte
	if err := fixture.db.QueryRow(`SELECT accounting_state,account_reserved_milli,ledger_rows_remaining
FROM logical_requests WHERE id=?`, request.ID).Scan(&accounting, &reserved, &remaining); err != nil {
		t.Fatalf("read discovery reservation: %v", err)
	}
	remainingValue, ok := decodeSmallU128(remaining)
	if accounting != string(AccountingNone) || reserved != 0 || !ok || remainingValue != 0 {
		t.Fatalf("discovery reservation = %q %d %x", accounting, reserved, remaining)
	}
	dispatch, err := service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatalf("take discovery dispatch: %v", err)
	}
	dispatch.Clear()
	if _, err := service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
		Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true,
		Usage: connectorcontract.Usage{Present: true},
	}); err != nil {
		t.Fatalf("complete discovery attempt: %v", err)
	}
	var attemptModel string
	if err := fixture.db.QueryRow(`SELECT upstream_model_id FROM request_attempts WHERE claim_id=?`,
		handle.ClaimID()).Scan(&attemptModel); err != nil {
		t.Fatalf("read discovery attempt model: %v", err)
	}
	if attemptModel != "" {
		t.Fatalf("discovery attempt model = %q, want empty", attemptModel)
	}
	discoveryCaller := CallerResult{Class: ResultSuccess, Status: 200}
	discoveryTerminal, err := service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID:   request.ID,
		Caller:      discoveryCaller,
		Disposition: AccountingNone,
	})
	if err != nil {
		t.Fatalf("complete discovery request: %v", err)
	}
	fixture.requireCallerMirror(request.ID, discoveryCaller, discoveryTerminal.TerminalAt)
	recoveryRequest, recoveryHandle, err := service.ClaimDiscovery(context.Background(), DiscoveryClaimInput{
		ActorUserID: userID,
		Candidate:   discoveryCandidate,
	})
	if err != nil {
		t.Fatalf("claim discovery recovery fixture: %v", err)
	}
	recoveryDispatch, err := service.TakeForDispatch(context.Background(), recoveryHandle)
	if err != nil {
		t.Fatalf("dispatch discovery recovery fixture: %v", err)
	}
	recoveryDispatch.Clear()
	report, err := service.RecoverNonterminal(context.Background(), 10)
	if err != nil {
		t.Fatalf("recover discovery request: %v", err)
	}
	if report.CommittedClaims != 1 || report.CompletedRequests != 1 || report.More {
		t.Fatalf("discovery recovery report = %+v", report)
	}
	var recoveryState, recoveryAccounting string
	if err := fixture.db.QueryRow(`SELECT state,accounting_state FROM logical_requests WHERE id=?`,
		recoveryRequest.ID).Scan(&recoveryState, &recoveryAccounting); err != nil {
		t.Fatalf("read recovered discovery request: %v", err)
	}
	if recoveryState != string(RequestTerminal) || recoveryAccounting != string(AccountingNone) {
		t.Fatalf("recovered discovery = state %q accounting %q", recoveryState, recoveryAccounting)
	}
}

func TestDiscoveryRecoveryUsesAcceptedCallerProjectionAcrossCrashWindows(t *testing.T) {
	ctx := context.Background()
	wantCaller := CallerResult{Class: ResultSuccess, Status: 202}
	wantCompletion := func(requestID string) CompleteRequestInput {
		return CompleteRequestInput{
			RequestID:   requestID,
			Caller:      wantCaller,
			Disposition: AccountingNone,
		}
	}
	newDiscovery := func(t *testing.T, label string) (*claimFixture, Request, Handle) {
		t.Helper()
		fixture := newClaimFixture(t)
		userID := fixture.seedUser("discovery-recovery-"+label, false)
		key := fixture.seedKey(userID, "discovery-recovery-"+label)
		candidate := key.candidate
		candidate.UpstreamModelID = ""
		request, handle, err := fixture.service.ClaimDiscovery(ctx, DiscoveryClaimInput{
			ActorUserID: userID,
			Candidate:   candidate,
		})
		if err != nil {
			t.Fatalf("claim discovery: %v", err)
		}
		return fixture, request, handle
	}
	requireReport := func(t *testing.T, report RecoveryReport, released, committed int) {
		t.Helper()
		if report.ReleasedClaims != released || report.CommittedClaims != committed ||
			report.CompletedRequests != 1 || report.MarkedOrphans != 0 ||
			report.DeletedOrphans != 0 || report.More {
			t.Fatalf("recovery report = %+v, want released=%d committed=%d completed=1", report, released, committed)
		}
	}
	requireProjection := func(t *testing.T, fixture *claimFixture, requestID string) Request {
		t.Helper()
		input := wantCompletion(requestID)
		terminal, err := fixture.service.CompleteRequest(ctx, input)
		if err != nil {
			t.Fatalf("replay recovered discovery request: %v", err)
		}
		if terminal.State != RequestTerminal || terminal.ResultClass != ResultSuccess ||
			terminal.CallerStatus != 202 || terminal.CallerErrorCode != "" ||
			terminal.AccountingDisposition != AccountingNone || terminal.ReservedMilli != 0 ||
			terminal.TerminalAt == 0 {
			t.Fatalf("recovered discovery request = %+v", terminal)
		}
		fixture.requireCallerMirror(requestID, wantCaller, terminal.TerminalAt)
		replay, err := fixture.service.CompleteRequest(ctx, input)
		if err != nil || replay.TerminalAt != terminal.TerminalAt || replay.State != RequestTerminal {
			t.Fatalf("second recovered discovery replay = %+v, %v", replay, err)
		}
		fixture.accounting.mu.Lock()
		accountingRequests := len(fixture.accounting.requests)
		fixture.accounting.mu.Unlock()
		if accountingRequests != 0 {
			t.Fatalf("discovery recovery invoked %d accounting request callbacks", accountingRequests)
		}
		return terminal
	}

	t.Run("claimed", func(t *testing.T) {
		fixture, request, handle := newDiscovery(t, "claimed")
		fixture.clock.Store(1_001)
		report, err := fixture.service.RecoverNonterminal(ctx, MaxRecoveryBatch)
		if err != nil {
			t.Fatalf("recover claimed discovery: %v", err)
		}
		requireReport(t, report, 1, 0)
		released, err := fixture.service.ReleaseUndispatched(ctx, handle)
		if err != nil {
			t.Fatalf("replay recovered discovery release: %v", err)
		}
		if released.State != StateReleased || released.CompletedAt != 1_001 ||
			released.RewardState != RewardNotApplicable {
			t.Fatalf("released discovery claim = %+v", released)
		}
		var attempts int
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM request_attempts WHERE claim_id=?`,
			handle.ClaimID()).Scan(&attempts); err != nil {
			t.Fatalf("count claimed-window attempts: %v", err)
		}
		if attempts != 0 {
			t.Fatalf("claimed discovery recovery created %d attempt rows", attempts)
		}
		requireProjection(t, fixture, request.ID)

		conflicts := []struct {
			name  string
			input CompleteRequestInput
		}{
			{
				name: "failed",
				input: CompleteRequestInput{RequestID: request.ID,
					Caller:      CallerResult{Class: ResultFailed, Status: 502, ErrorCode: httperr.CodeUpstream},
					Disposition: AccountingNone},
			},
			{
				name: "status",
				input: CompleteRequestInput{RequestID: request.ID,
					Caller: CallerResult{Class: ResultSuccess, Status: 200}, Disposition: AccountingNone},
			},
			{
				name: "error",
				input: CompleteRequestInput{RequestID: request.ID,
					Caller:      CallerResult{Class: ResultFailed, Status: 503, ErrorCode: httperr.CodeServiceUnavailable},
					Disposition: AccountingNone},
			},
			{
				name:  "disposition",
				input: CompleteRequestInput{RequestID: request.ID, Caller: wantCaller, Disposition: AccountingRelease},
			},
			{
				name: "charge",
				input: CompleteRequestInput{RequestID: request.ID, Caller: wantCaller,
					Disposition: AccountingCommit, ActualChargeMilli: 1},
			},
		}
		for _, test := range conflicts {
			t.Run(test.name, func(t *testing.T) {
				_, err := fixture.service.CompleteRequest(ctx, test.input)
				requireErrorIs(t, err, ErrConflict)
			})
		}
	})

	t.Run("dispatched", func(t *testing.T) {
		fixture, request, handle := newDiscovery(t, "dispatched")
		dispatch, err := fixture.service.TakeForDispatch(ctx, handle)
		if err != nil {
			t.Fatalf("dispatch discovery: %v", err)
		}
		dispatch.Clear()
		fixture.clock.Store(1_001)
		report, err := fixture.service.RecoverNonterminal(ctx, MaxRecoveryBatch)
		if err != nil {
			t.Fatalf("recover dispatched discovery: %v", err)
		}
		requireReport(t, report, 0, 1)
		attempt, err := fixture.service.CompleteAttempt(ctx, handle, AttemptOutcome{Kind: ResultSynthetic})
		if err != nil {
			t.Fatalf("read recovered synthetic attempt: %v", err)
		}
		if attempt.State != StateCommitted || attempt.Kind != ResultSynthetic ||
			attempt.UpstreamStatus != 502 || attempt.UpstreamCode != "" ||
			attempt.Diagnostic != "dispatch outcome unavailable after restart" || attempt.Usage.Present ||
			attempt.StartedAt != 1_000 || attempt.CompletedAt != 1_001 ||
			attempt.RewardState != RewardNotApplicable {
			t.Fatalf("recovered synthetic attempt = %+v", attempt)
		}
		requireProjection(t, fixture, request.ID)
	})

	t.Run("attempt committed", func(t *testing.T) {
		fixture, request, handle := newDiscovery(t, "attempt-committed")
		dispatch, err := fixture.service.TakeForDispatch(ctx, handle)
		if err != nil {
			t.Fatalf("dispatch discovery: %v", err)
		}
		dispatch.Clear()
		fixture.clock.Store(1_001)
		outcome := AttemptOutcome{
			Kind:            ResultResponse,
			UpstreamStatus:  429,
			UpstreamCode:    "rate_limited",
			Diagnostic:      "discovery upstream rejected the request",
			ProtocolSuccess: false,
			ResponseStarted: true,
			Usage: connectorcontract.Usage{
				UncachedInputTokens:   11,
				CacheWriteInputTokens: 2,
				CacheReadInputTokens:  3,
				OutputTokens:          5,
				Present:               true,
			},
		}
		attempt, err := fixture.service.CompleteAttempt(ctx, handle, outcome)
		if err != nil {
			t.Fatalf("complete real discovery attempt: %v", err)
		}
		fixture.clock.Store(1_002)
		report, err := fixture.service.RecoverNonterminal(ctx, MaxRecoveryBatch)
		if err != nil {
			t.Fatalf("recover discovery after attempt commit: %v", err)
		}
		requireReport(t, report, 0, 0)
		replayedAttempt, err := fixture.service.CompleteAttempt(ctx, handle, AttemptOutcome{Kind: ResultSynthetic})
		if err != nil {
			t.Fatalf("replay real discovery attempt: %v", err)
		}
		if replayedAttempt.State != StateCommitted || replayedAttempt.Kind != attempt.Kind ||
			replayedAttempt.UpstreamStatus != attempt.UpstreamStatus ||
			replayedAttempt.UpstreamCode != attempt.UpstreamCode ||
			replayedAttempt.Diagnostic != attempt.Diagnostic || replayedAttempt.Usage != attempt.Usage ||
			replayedAttempt.StartedAt != attempt.StartedAt || replayedAttempt.CompletedAt != attempt.CompletedAt ||
			replayedAttempt.RewardState != attempt.RewardState {
			t.Fatalf("real discovery attempt changed during recovery: before=%+v after=%+v", attempt, replayedAttempt)
		}
		requireProjection(t, fixture, request.ID)
	})
}

func TestRecoveryReleasesClaimedAndConservativelyCommitsDispatched(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("recovery", false)
	key := fixture.seedKey(userID, "recovery")
	acceptedRequest := fixture.acceptSelf(userID, 1)
	claimedRequest := fixture.acceptSelf(userID, 1)
	if _, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: claimedRequest.ID, ActorUserID: userID, AttemptSeq: 1,
		Purpose: PurposeSelf, Candidate: key.candidate,
	}); err != nil {
		t.Fatalf("create claimed recovery row: %v", err)
	}
	dispatchedRequest := fixture.acceptSelf(userID, 1)
	dispatchedHandle, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: dispatchedRequest.ID, ActorUserID: userID, AttemptSeq: 1,
		Purpose: PurposeSelf, Candidate: key.candidate,
	})
	if err != nil {
		t.Fatalf("create dispatched recovery claim: %v", err)
	}
	dispatch, err := fixture.service.TakeForDispatch(context.Background(), dispatchedHandle)
	if err != nil {
		t.Fatalf("mark dispatched recovery claim: %v", err)
	}
	dispatch.Clear()

	decryptFailureRequest := fixture.acceptSelf(userID, 1)
	decryptFailureHandle, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: decryptFailureRequest.ID, ActorUserID: userID, AttemptSeq: 1,
		Purpose: PurposeSelf, Candidate: key.candidate,
	})
	if err != nil {
		t.Fatalf("create decrypt-failure claim: %v", err)
	}
	fixture.codec.mu.Lock()
	fixture.codec.failOpen = true
	fixture.codec.mu.Unlock()
	_, err = fixture.service.TakeForDispatch(context.Background(), decryptFailureHandle)
	requireErrorIs(t, err, ErrCredentialUnavailable)
	fixture.codec.mu.Lock()
	fixture.codec.failOpen = false
	fixture.codec.mu.Unlock()

	fixture.clock.Store(2_000)
	report, err := fixture.service.RecoverNonterminal(context.Background(), MaxRecoveryBatch)
	if err != nil {
		t.Fatalf("recover nonterminal claims: %v", err)
	}
	if report.ReleasedClaims != 1 || report.CommittedClaims != 2 || report.CompletedRequests != 4 || report.More {
		t.Fatalf("recovery report = %+v", report)
	}
	var released, committed, synthetic, unknown int
	if err := fixture.db.QueryRow(`SELECT
SUM(state='released'),SUM(state='committed'),
(SELECT COUNT(*) FROM request_attempts WHERE result_kind='synthetic' AND upstream_status=502),
(SELECT COUNT(*) FROM request_attempts WHERE usage_unknown=1)
FROM dispatch_claims`).Scan(&released, &committed, &synthetic, &unknown); err != nil {
		t.Fatalf("read recovered claim states: %v", err)
	}
	if released != 1 || committed != 2 || synthetic != 2 || unknown != 2 {
		t.Fatalf("recovered states = released %d committed %d synthetic %d unknown %d",
			released, committed, synthetic, unknown)
	}
	var releasedRequests, committedRequests int
	if err := fixture.db.QueryRow(`SELECT SUM(accounting_state='released'),SUM(accounting_state='committed')
FROM logical_requests WHERE id IN (?,?,?,?)`, acceptedRequest.ID, claimedRequest.ID,
		dispatchedRequest.ID, decryptFailureRequest.ID).Scan(&releasedRequests, &committedRequests); err != nil {
		t.Fatalf("read recovered request states: %v", err)
	}
	if releasedRequests != 2 || committedRequests != 2 {
		t.Fatalf("recovered requests = released %d committed %d", releasedRequests, committedRequests)
	}
	selfCaller := CallerResult{Class: ResultFailed, Status: 502, ErrorCode: httperr.CodeUpstream}
	recoveredSelf, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID:         dispatchedRequest.ID,
		Caller:            selfCaller,
		Disposition:       AccountingCommit,
		ActualChargeMilli: dispatchedRequest.ReservedMilli,
	})
	if err != nil {
		t.Fatalf("replay conservatively recovered self request: %v", err)
	}
	if recoveredSelf.ResultClass != ResultFailed || recoveredSelf.CallerStatus != 502 ||
		recoveredSelf.CallerErrorCode != httperr.CodeUpstream ||
		recoveredSelf.AccountingDisposition != AccountingCommit {
		t.Fatalf("conservatively recovered self request = %+v", recoveredSelf)
	}
	fixture.requireCallerMirror(dispatchedRequest.ID, selfCaller, recoveredSelf.TerminalAt)
	fixture.accounting.mu.Lock()
	var selfAccounting RequestAccounting
	var foundSelfAccounting bool
	for _, completion := range fixture.accounting.requests {
		if completion.RequestID == dispatchedRequest.ID {
			selfAccounting = completion
			foundSelfAccounting = true
			break
		}
	}
	fixture.accounting.mu.Unlock()
	if !foundSelfAccounting || selfAccounting.Disposition != AccountingCommit ||
		selfAccounting.ActualMilli != dispatchedRequest.ReservedMilli {
		t.Fatalf("conservative self accounting = %+v, found=%v", selfAccounting, foundSelfAccounting)
	}
	replay, err := fixture.service.RecoverNonterminal(context.Background(), MaxRecoveryBatch)
	if err != nil {
		t.Fatalf("replay recovery: %v", err)
	}
	if replay.ReleasedClaims != 0 || replay.CommittedClaims != 0 || replay.CompletedRequests != 0 || replay.More {
		t.Fatalf("replayed recovery report = %+v", replay)
	}
}

func TestOrphanSecretCleanupBoundary(t *testing.T) {
	for _, elapsed := range []int64{3_599, 3_600, 3_601} {
		t.Run(string(rune('a'+elapsed-3_599)), func(t *testing.T) {
			fixture := newClaimFixture(t)
			userID := fixture.seedUser("orphan", false)
			key := fixture.seedKey(userID, "orphan")
			request := fixture.acceptSelf(userID, 1)
			handle, err := fixture.service.Claim(context.Background(), ClaimInput{
				RequestID: request.ID, ActorUserID: userID, AttemptSeq: 1,
				Purpose: PurposeSelf, Candidate: key.candidate,
			})
			if err != nil {
				t.Fatalf("claim orphan fixture: %v", err)
			}
			dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
			if err != nil {
				t.Fatalf("dispatch orphan fixture: %v", err)
			}
			dispatch.Clear()
			if _, err := fixture.db.Exec(`DELETE FROM endpoint_keys WHERE id=?`, key.keyID); err != nil {
				t.Fatalf("delete orphaned key: %v", err)
			}
			maintenance, err := fixture.service.MaintainOrphanSecrets(context.Background(), 10)
			if err != nil {
				t.Fatalf("maintenance while claim is live: %v", err)
			}
			if maintenance.Marked != 0 || maintenance.Deleted != 0 {
				t.Fatalf("live claim credential was maintained: %+v", maintenance)
			}
			orphanedAt := fixture.clock.Load()
			if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
				Kind: ResultSynthetic, UpstreamStatus: 502, ResponseStarted: true,
			}); err != nil {
				t.Fatalf("complete orphan fixture: %v", err)
			}
			var marker sql.NullInt64
			if err := fixture.db.QueryRow(`SELECT orphaned_at FROM endpoint_key_secrets WHERE id=?`, key.secretID).Scan(&marker); err != nil {
				t.Fatalf("read orphan marker: %v", err)
			}
			if !marker.Valid || marker.Int64 != orphanedAt {
				t.Fatalf("orphan marker = %+v, want %d", marker, orphanedAt)
			}
			newRequest := fixture.acceptSelf(userID, 1)
			_, err = fixture.service.Claim(context.Background(), ClaimInput{
				RequestID: newRequest.ID, ActorUserID: userID, AttemptSeq: 1,
				Purpose: PurposeSelf, Candidate: key.candidate,
			})
			requireErrorIs(t, err, ErrNotFound)
			fixture.clock.Store(orphanedAt + elapsed)
			maintenance, err = fixture.service.MaintainOrphanSecrets(context.Background(), 10)
			if err != nil {
				t.Fatalf("maintain orphan at elapsed %d: %v", elapsed, err)
			}
			var exists int
			if err := fixture.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM endpoint_key_secrets WHERE id=?)`, key.secretID).Scan(&exists); err != nil {
				t.Fatalf("read orphan existence: %v", err)
			}
			wantExists := elapsed < int64(OrphanSecretTTL.Seconds())
			if (exists != 0) != wantExists {
				t.Fatalf("elapsed %d existence = %d, want %v (maintenance %+v)", elapsed, exists, wantExists, maintenance)
			}
		})
	}
}

func TestCancellationRemainsIndependentCallerResult(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("cancel", false)
	key := fixture.seedKey(userID, "cancel")
	request := fixture.acceptSelf(userID, 1)
	handle, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: userID, AttemptSeq: 1,
		Purpose: PurposeSelf, Candidate: key.candidate,
	})
	if err != nil {
		t.Fatalf("claim cancellation fixture: %v", err)
	}
	dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatalf("dispatch cancellation fixture: %v", err)
	}
	dispatch.Clear()
	if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
		Kind: ResultSynthetic, ProtocolSuccess: false, ResponseStarted: false,
	}); err != nil {
		t.Fatalf("complete cancelled attempt: %v", err)
	}
	requestResult, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID:   request.ID,
		Caller:      CallerResult{Class: ResultCancelled},
		Disposition: AccountingCommit, ActualChargeMilli: 100,
	})
	if err != nil {
		t.Fatalf("complete cancelled caller result: %v", err)
	}
	if requestResult.ResultClass != ResultCancelled || requestResult.CallerStatus != 0 || requestResult.CallerErrorCode != "" {
		t.Fatalf("cancelled caller result = %+v", requestResult)
	}
	var status sql.NullInt64
	if err := fixture.db.QueryRow(`SELECT caller_status FROM logical_requests WHERE id=?`, request.ID).Scan(&status); err != nil {
		t.Fatalf("read cancelled caller status: %v", err)
	}
	if status.Valid {
		t.Fatalf("cancelled caller status persisted as %d", status.Int64)
	}
	fixture.requireCallerMirror(request.ID, CallerResult{Class: ResultCancelled}, requestResult.TerminalAt)
}

func TestCompleteRequestRejectsReleaseAfterDispatch(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("release-after-dispatch", false)
	key := fixture.seedKey(userID, "release-after-dispatch")
	request := fixture.acceptSelf(userID, 1)
	handle, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: userID, AttemptSeq: 1,
		Purpose: PurposeSelf, Candidate: key.candidate,
	})
	if err != nil {
		t.Fatalf("claim request: %v", err)
	}
	dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatalf("take dispatch: %v", err)
	}
	dispatch.Clear()
	if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
		Kind: ResultSynthetic, UpstreamStatus: 502,
	}); err != nil {
		t.Fatalf("complete attempt: %v", err)
	}
	_, err = fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID:   request.ID,
		Caller:      CallerResult{Class: ResultFailed, Status: 503, ErrorCode: httperr.CodeServiceUnavailable},
		Disposition: AccountingRelease,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("release after dispatch error = %v, want ErrConflict", err)
	}
}
