package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type inspectingAcceptanceGate struct {
	calls int
	err   error
	times []int64
}

func (gate *inspectingAcceptanceGate) AuthorizeChatAcceptance(ctx context.Context, tx *sql.Tx, userID, decisionNow int64) error {
	gate.calls++
	gate.times = append(gate.times, decisionNow)
	if userID <= 0 || decisionNow < 0 {
		return ErrInvalidInput
	}
	var requestRows, logRows int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM logical_requests`).Scan(&requestRows); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_logs`).Scan(&logRows); err != nil {
		return err
	}
	if requestRows != 0 || logRows != 0 {
		return errors.New("acceptance gate observed premature request facts")
	}
	return gate.err
}

func TestAcceptRunsFinalGateAfterUserRevalidationBeforeAllWrites(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("acceptance-order", false)
	gateErr := errors.New("maintenance won acceptance")
	gate := &inspectingAcceptanceGate{err: gateErr}
	fixture.service.acceptance = gate

	_, err := fixture.service.Accept(context.Background(), AcceptInput{
		UserID: userID, Route: RouteOpenAIChat, ModelSnapshot: "public/model", AttemptLimit: 1,
	})
	if !errors.Is(err, gateErr) || gate.calls != 1 {
		t.Fatalf("gated acceptance = calls %d err %v", gate.calls, err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM logical_requests`,
		`SELECT COUNT(*) FROM request_logs`,
		`SELECT COUNT(*) FROM claim_test_ledger_rows`,
	} {
		var count int
		if err := fixture.db.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("gated acceptance wrote rows for %q: count=%d err=%v", query, count, err)
		}
	}

	gate.err = nil
	if _, err := fixture.service.Accept(context.Background(), AcceptInput{
		UserID: userID, Route: RouteOpenAIChat, ModelSnapshot: "public/model", AttemptLimit: 1,
	}); err != nil {
		t.Fatalf("open acceptance: %v", err)
	}
	if gate.calls != 2 {
		t.Fatalf("acceptance gate calls = %d, want 2", gate.calls)
	}
}

func TestAcceptMissingGateFailsClosedAndBannedUserWinsBeforeGate(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("acceptance-fail-closed", false)
	fixture.service.acceptance = nil
	if _, err := fixture.service.Accept(context.Background(), AcceptInput{
		UserID: userID, Route: RouteOpenAIChat, ModelSnapshot: "public/model", AttemptLimit: 1,
	}); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("missing gate error = %v", err)
	}

	gate := &inspectingAcceptanceGate{}
	fixture.service.acceptance = gate
	if _, err := fixture.db.Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Accept(context.Background(), AcceptInput{
		UserID: userID, Route: RouteOpenAIChat, ModelSnapshot: "public/model", AttemptLimit: 1,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("banned acceptance error = %v", err)
	}
	if gate.calls != 0 {
		t.Fatalf("gate ran before user revalidation: %d", gate.calls)
	}
}

func TestAcceptDecisionTimeIsRequiredOnlyForCharity(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("acceptance-decision-shape", false)
	decisionNow := fixture.clock.Load()

	if _, err := fixture.service.Accept(context.Background(), AcceptInput{
		UserID: userID, Route: RouteOpenAIChat, ModelSnapshot: "public/model", AttemptLimit: 1,
		CharityDecisionNow: &decisionNow,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("personal acceptance with charity decision time error = %v", err)
	}
	if _, err := fixture.service.Accept(context.Background(), AcceptInput{
		UserID: userID, Route: RouteCharityChat, ModelSnapshot: "[公益]provider/model", AttemptLimit: 1,
		CharityModelID: 1,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("charity acceptance without decision time error = %v", err)
	}
	var requests int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM logical_requests`).Scan(&requests); err != nil || requests != 0 {
		t.Fatalf("invalid decision-time shapes wrote %d requests: %v", requests, err)
	}
}

func TestAcceptCharityUsesExplicitDecisionTimeAcrossClockSecond(t *testing.T) {
	t.Run("account authorization", func(t *testing.T) {
		fixture := newClaimFixture(t)
		userID := fixture.seedUser("acceptance-explicit-auth-time", false)
		decisionNow := fixture.clock.Load()
		if _, err := fixture.db.Exec(`UPDATE users SET charity_suspended_until=? WHERE id=?`, decisionNow+1, userID); err != nil {
			t.Fatal(err)
		}
		clockCalls := 0
		fixture.service.now = func() time.Time {
			clockCalls++
			return time.Unix(decisionNow+2, 0)
		}
		gate := &inspectingAcceptanceGate{}
		fixture.service.acceptance = gate

		_, err := fixture.service.Accept(context.Background(), AcceptInput{
			UserID: userID, Route: RouteCharityChat, ModelSnapshot: "[公益]provider/model", AttemptLimit: 1,
			CharityModelID: 1, CharityDecisionNow: &decisionNow,
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("charity authorization at explicit second error = %v, want not found", err)
		}
		if clockCalls != 0 || gate.calls != 0 {
			t.Fatalf("authorization used later clock or crossed gate: clock=%d gate=%d", clockCalls, gate.calls)
		}
	})

	t.Run("gate reservation and persisted facts", func(t *testing.T) {
		fixture := newClaimFixture(t)
		userID := fixture.seedUser("acceptance-explicit-persist-time", false)
		fixture.seedLedgerUser(userID, 1_000)
		fixture.useLedgerAccounting()
		decisionNow := fixture.clock.Load()
		clockCalls := 0
		fixture.service.now = func() time.Time {
			clockCalls++
			return time.Unix(decisionNow+int64(clockCalls), 0)
		}
		gate := &inspectingAcceptanceGate{}
		fixture.service.acceptance = gate

		request, err := fixture.service.Accept(context.Background(), AcceptInput{
			UserID: userID, Route: RouteCharityChat, ModelSnapshot: "[公益]provider/model", AttemptLimit: 2,
			ReservedMilli: 200, CharityModelID: 1, CharityDecisionNow: &decisionNow,
		})
		if err != nil {
			t.Fatal(err)
		}
		if clockCalls != 0 {
			t.Fatalf("charity acceptance sampled its clock %d times", clockCalls)
		}
		if gate.calls != 1 || len(gate.times) != 1 || gate.times[0] != decisionNow {
			t.Fatalf("acceptance gate times = %v calls=%d, want %d", gate.times, gate.calls, decisionNow)
		}
		if request.CreatedAt != decisionNow {
			t.Fatalf("returned request created_at = %d, want %d", request.CreatedAt, decisionNow)
		}
		var requestCreatedAt, logStartedAt int64
		if err := fixture.db.QueryRow(`SELECT r.created_at,l.started_at
		FROM logical_requests r JOIN request_logs l ON l.logical_request_id=r.id WHERE r.id=?`, request.ID).
			Scan(&requestCreatedAt, &logStartedAt); err != nil {
			t.Fatal(err)
		}
		if requestCreatedAt != decisionNow || logStartedAt != decisionNow {
			t.Fatalf("persisted request/log times = %d/%d, want %d", requestCreatedAt, logStartedAt, decisionNow)
		}
		fixture.requireOperationTime(ledger.KindCharityReserve, request.ID, decisionNow)
		fixture.charity.mu.Lock()
		accepts := append([]CharityAcceptance(nil), fixture.charity.accepts...)
		fixture.charity.mu.Unlock()
		if len(accepts) != 1 || accepts[0].AcceptedAt != decisionNow {
			t.Fatalf("charity acceptance facts = %+v, want accepted_at %d", accepts, decisionNow)
		}
	})
}

func TestDispatchRailPersistsBeforeCredentialAndSeparatesAttemptFromCaller(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("dispatch-owner", false)
	key := fixture.seedKey(userID, "dispatch")
	request := fixture.acceptSelf(userID, 1)
	fixture.requireCapacity(request.ID, 1, 1, 1)
	handle, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID:   request.ID,
		ActorUserID: userID,
		AttemptSeq:  1,
		Purpose:     PurposeSelf,
		Candidate:   key.candidate,
	})
	if err != nil {
		t.Fatalf("claim request: %v", err)
	}
	if fixture.codec.openCount() != 0 {
		t.Fatal("credential was opened during Claim")
	}
	fixture.codec.mu.Lock()
	fixture.codec.onOpen = func() {
		var state string
		if err := fixture.db.QueryRow(`SELECT state FROM dispatch_claims WHERE id=?`, handle.ClaimID()).Scan(&state); err != nil {
			t.Errorf("read state at credential open: %v", err)
			return
		}
		if state != string(StateDispatched) {
			t.Errorf("state at credential open = %q, want dispatched", state)
		}
	}
	fixture.codec.mu.Unlock()
	dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatalf("take for dispatch: %v", err)
	}
	if fixture.codec.openCount() != 1 {
		t.Fatalf("credential open count = %d, want 1", fixture.codec.openCount())
	}
	credential, ok := dispatch.TakeCredential()
	if !ok || credential == nil {
		t.Fatal("dispatch did not transfer its credential")
	}
	if second, secondOK := dispatch.TakeCredential(); secondOK || second != nil {
		t.Fatal("dispatch transferred its credential twice")
	}
	plaintext, ciphertext, ok := credential.Take()
	if !ok || string(plaintext) != "fixture-credential-dispatch" || string(ciphertext) != "fixture-envelope-1" {
		t.Fatalf("credential transfer = %q / %q / %v", plaintext, ciphertext, ok)
	}
	clear(plaintext)
	clear(ciphertext)
	if plainAgain, cipherAgain, again := credential.Take(); again || plainAgain != nil || cipherAgain != nil {
		t.Fatal("short-lived credential transferred twice")
	}
	credential.Clear()
	dispatch.Clear()
	if _, err := fixture.service.TakeForDispatch(context.Background(), handle); !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("second TakeForDispatch error = %v, want ErrAlreadyDispatched", err)
	}

	fixture.clock.Store(1_001)
	attempt, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
		Kind:            ResultResponse,
		UpstreamStatus:  200,
		UpstreamCode:    "unsafe-雪",
		Diagnostic:      "clean EOF\nwithout protocol terminal",
		ProtocolSuccess: false,
		ResponseStarted: true,
	})
	if err != nil {
		t.Fatalf("complete nonterminal HTTP 200 attempt: %v", err)
	}
	if attempt.State != StateCommitted || attempt.UpstreamStatus != 200 {
		t.Fatalf("attempt projection = %+v", attempt)
	}
	if attempt.UpstreamCode != "" || strings.ContainsAny(attempt.Diagnostic, "\r\n\t") {
		t.Fatalf("unsafe attempt projection persisted: %+v", attempt)
	}
	fixture.requireCapacity(request.ID, 1, 1, 1)
	if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
		Kind:            ResultResponse,
		UpstreamStatus:  200,
		ProtocolSuccess: true,
		ResponseStarted: true,
	}); err != nil {
		t.Fatalf("idempotent attempt completion: %v", err)
	}

	fixture.clock.Store(1_002)
	completionInput := CompleteRequestInput{
		RequestID: request.ID,
		Caller: CallerResult{
			Class:     ResultFailed,
			Status:    502,
			ErrorCode: httperr.CodeUpstream,
		},
		Disposition:       AccountingCommit,
		ActualChargeMilli: 100,
	}
	terminal, err := fixture.service.CompleteRequest(context.Background(), completionInput)
	if err != nil {
		t.Fatalf("complete caller request: %v", err)
	}
	if terminal.State != RequestTerminal || terminal.CallerStatus != 502 || terminal.ResultClass != ResultFailed {
		t.Fatalf("terminal request = %+v", terminal)
	}
	fixture.requireCapacity(request.ID, 0, 0, 2)
	fixture.requireCallerMirror(request.ID, completionInput.Caller, terminal.TerminalAt)
	if replay, err := fixture.service.CompleteRequest(context.Background(), completionInput); err != nil ||
		replay.State != RequestTerminal || replay.TerminalAt != terminal.TerminalAt {
		t.Fatalf("replay terminal request = %+v, %v", replay, err)
	}
	fixture.requireCapacity(request.ID, 0, 0, 2)
	fixture.requireCallerMirror(request.ID, completionInput.Caller, terminal.TerminalAt)
	conflictingReplay := completionInput
	conflictingReplay.Caller = CallerResult{Class: ResultCancelled}
	_, err = fixture.service.CompleteRequest(context.Background(), conflictingReplay)
	requireErrorIs(t, err, ErrConflict)
	fixture.requireCapacity(request.ID, 0, 0, 2)
	fixture.requireCallerMirror(request.ID, completionInput.Caller, terminal.TerminalAt)
	var callerStatus, attemptStatus, secretRefs int
	if err := fixture.db.QueryRow(`SELECT l.status_code,a.upstream_status,
(SELECT COUNT(*) FROM dispatch_claims WHERE id=? AND secret_ref_id IS NOT NULL)
FROM request_logs l JOIN request_attempts a ON a.request_log_id=l.id
WHERE l.logical_request_id=?`, handle.ClaimID(), request.ID).Scan(
		&callerStatus, &attemptStatus, &secretRefs); err != nil {
		t.Fatalf("read caller/attempt split: %v", err)
	}
	if callerStatus != 502 || attemptStatus != 200 || secretRefs != 0 {
		t.Fatalf("caller=%d attempt=%d secret_refs=%d", callerStatus, attemptStatus, secretRefs)
	}
	var orphaned sql.NullInt64
	if err := fixture.db.QueryRow(`SELECT orphaned_at FROM endpoint_key_secrets WHERE id=?`, key.secretID).Scan(&orphaned); err != nil {
		t.Fatalf("read retained secret: %v", err)
	}
	if orphaned.Valid {
		t.Fatal("live endpoint secret was marked orphaned")
	}
	fixture.accounting.mu.Lock()
	defer fixture.accounting.mu.Unlock()
	if len(fixture.accounting.reservations) != 1 || len(fixture.accounting.requests) != 1 ||
		len(fixture.accounting.claimCompletions) != 0 {
		t.Fatalf("accounting callbacks = reserve %d request %d claim %d",
			len(fixture.accounting.reservations), len(fixture.accounting.requests),
			len(fixture.accounting.claimCompletions))
	}
}

func TestClaimRevalidatesRevocationOrder(t *testing.T) {
	tests := []struct {
		name   string
		before func(*claimFixture, testKey)
		after  func(*claimFixture, testKey)
	}{
		{
			name: "endpoint disable",
			before: func(f *claimFixture, key testKey) {
				if _, err := f.db.Exec(`UPDATE endpoints SET enabled=0 WHERE id=?`, key.endpointID); err != nil {
					f.t.Fatalf("disable endpoint: %v", err)
				}
			},
			after: func(f *claimFixture, key testKey) {
				if _, err := f.db.Exec(`UPDATE endpoints SET enabled=0 WHERE id=?`, key.endpointID); err != nil {
					f.t.Fatalf("disable endpoint: %v", err)
				}
			},
		},
		{
			name: "endpoint delete",
			before: func(f *claimFixture, key testKey) {
				if _, err := f.db.Exec(`DELETE FROM endpoints WHERE id=?`, key.endpointID); err != nil {
					f.t.Fatalf("delete endpoint: %v", err)
				}
			},
			after: func(f *claimFixture, key testKey) {
				if _, err := f.db.Exec(`DELETE FROM endpoints WHERE id=?`, key.endpointID); err != nil {
					f.t.Fatalf("delete endpoint: %v", err)
				}
			},
		},
		{
			name: "disable",
			before: func(f *claimFixture, key testKey) {
				if _, err := f.db.Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, key.keyID); err != nil {
					f.t.Fatalf("disable key: %v", err)
				}
			},
			after: func(f *claimFixture, key testKey) {
				if _, err := f.db.Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, key.keyID); err != nil {
					f.t.Fatalf("disable key: %v", err)
				}
			},
		},
		{
			name: "delete",
			before: func(f *claimFixture, key testKey) {
				if _, err := f.db.Exec(`DELETE FROM endpoint_keys WHERE id=?`, key.keyID); err != nil {
					f.t.Fatalf("delete key: %v", err)
				}
			},
			after: func(f *claimFixture, key testKey) {
				if _, err := f.db.Exec(`DELETE FROM endpoint_keys WHERE id=?`, key.keyID); err != nil {
					f.t.Fatalf("delete key: %v", err)
				}
			},
		},
		{
			name:   "report suspension",
			before: func(f *claimFixture, key testKey) { f.seedSuspension(key.keyID) },
			after:  func(f *claimFixture, key testKey) { f.seedSuspension(key.keyID) },
		},
	}
	for _, test := range tests {
		t.Run(test.name+"_wins_before_claim", func(t *testing.T) {
			fixture := newClaimFixture(t)
			userID := fixture.seedUser("before-"+test.name, false)
			key := fixture.seedKey(userID, "before-"+strings.ReplaceAll(test.name, " ", "-"))
			request := fixture.acceptSelf(userID, 1)
			test.before(fixture, key)
			_, err := fixture.service.Claim(context.Background(), ClaimInput{
				RequestID: request.ID, ActorUserID: userID, AttemptSeq: 1,
				Purpose: PurposeSelf, Candidate: key.candidate,
			})
			requireErrorIs(t, err, ErrNotFound)
			var claims int
			if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM dispatch_claims WHERE logical_request_id=?`, request.ID).Scan(&claims); err != nil {
				t.Fatalf("count rejected claims: %v", err)
			}
			if claims != 0 {
				t.Fatalf("rejected claim wrote %d rows", claims)
			}
		})
		t.Run("claim_wins_before_"+test.name, func(t *testing.T) {
			fixture := newClaimFixture(t)
			userID := fixture.seedUser("after-"+test.name, false)
			key := fixture.seedKey(userID, "after-"+strings.ReplaceAll(test.name, " ", "-"))
			request := fixture.acceptSelf(userID, 1)
			handle, err := fixture.service.Claim(context.Background(), ClaimInput{
				RequestID: request.ID, ActorUserID: userID, AttemptSeq: 1,
				Purpose: PurposeSelf, Candidate: key.candidate,
			})
			if err != nil {
				t.Fatalf("claim before revocation: %v", err)
			}
			test.after(fixture, key)
			dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
			if err != nil {
				t.Fatalf("dispatch won claim after revocation: %v", err)
			}
			dispatch.Clear()
		})
	}
}

func TestAttemptLimitOneHundredAndCrossKeyRetry(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("hundred", false)
	keys := []testKey{
		fixture.seedKey(userID, "retry-a"),
		fixture.seedKey(userID, "retry-b"),
	}
	request := fixture.acceptSelf(userID, MaxAttempts)
	for sequence := 1; sequence <= MaxAttempts; sequence++ {
		key := keys[(sequence-1)%len(keys)]
		handle, err := fixture.service.Claim(context.Background(), ClaimInput{
			RequestID: request.ID, ActorUserID: userID, AttemptSeq: sequence,
			Purpose: PurposeSelf, Candidate: key.candidate,
		})
		if err != nil {
			t.Fatalf("claim attempt %d: %v", sequence, err)
		}
		dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatalf("dispatch attempt %d: %v", sequence, err)
		}
		dispatch.Clear()
		if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
			Kind:            ResultSynthetic,
			UpstreamStatus:  502,
			Diagnostic:      "retryable upstream failure",
			Usage:           connectorcontract.Usage{Present: true},
			ProtocolSuccess: false,
			ResponseStarted: false,
		}); err != nil {
			t.Fatalf("complete attempt %d: %v", sequence, err)
		}
	}
	_, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: userID, AttemptSeq: MaxAttempts,
		Purpose: PurposeSelf, Candidate: keys[0].candidate,
	})
	requireErrorIs(t, err, ErrConflict)
	_, err = fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: userID, AttemptSeq: MaxAttempts + 1,
		Purpose: PurposeSelf, Candidate: keys[0].candidate,
	})
	requireErrorIs(t, err, ErrInvalidInput)
	if _, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID:   request.ID,
		Caller:      CallerResult{Class: ResultFailed, Status: 502, ErrorCode: httperr.CodeUpstream},
		Disposition: AccountingCommit, ActualChargeMilli: 100,
	}); err != nil {
		t.Fatalf("complete 100-attempt request: %v", err)
	}
	var attempts, firstKeyCount, secondKeyCount int
	if err := fixture.db.QueryRow(`SELECT COUNT(*),
SUM(endpoint_key_id_snapshot=?),SUM(endpoint_key_id_snapshot=?)
FROM request_attempts`, keys[0].keyID, keys[1].keyID).Scan(&attempts, &firstKeyCount, &secondKeyCount); err != nil {
		t.Fatalf("count attempt snapshots: %v", err)
	}
	if attempts != MaxAttempts || firstKeyCount != 50 || secondKeyCount != 50 {
		t.Fatalf("attempt distribution = total %d, keyA %d, keyB %d", attempts, firstKeyCount, secondKeyCount)
	}
}

func TestConcurrentClaimHasOneWinner(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("concurrent", false)
	key := fixture.seedKey(userID, "concurrent")
	request := fixture.acceptSelf(userID, 1)
	const contenders = 16
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := fixture.service.Claim(context.Background(), ClaimInput{
				RequestID: request.ID, ActorUserID: userID, AttemptSeq: 1,
				Purpose: PurposeSelf, Candidate: key.candidate,
			})
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	winners := 0
	losers := 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrConflict):
			losers++
		default:
			t.Fatalf("unexpected concurrent claim error: %v", err)
		}
	}
	if winners != 1 || losers != contenders-1 {
		t.Fatalf("winners=%d losers=%d", winners, losers)
	}
}

func TestInputAndHandleBoundaries(t *testing.T) {
	if validModelID(" leading") || validModelID("trailing ") || validModelID("control\n") ||
		validModelID(strings.Repeat("x", MaxUpstreamModelRunes+1)) || !validModelID(strings.Repeat("界", MaxUpstreamModelRunes)) {
		t.Fatal("upstream model scalar boundary mismatch")
	}
	if safeUpstreamCode("line\nforge") != "" || safeUpstreamCode(strings.Repeat("x", 65)) != "" ||
		safeUpstreamCode("safe_code") != "safe_code" {
		t.Fatal("upstream code projection mismatch")
	}
	if fmt.Sprintf("%v %#v", Handle{}, Handle{}) != "[redacted dispatch claim] [redacted dispatch claim]" {
		t.Fatal("claim handle formatting is not redacted")
	}
	if fmt.Sprintf("%v %#v", (*Dispatch)(nil), (*Dispatch)(nil)) != "[redacted outbound dispatch] [redacted outbound dispatch]" {
		t.Fatal("dispatch formatting is not redacted")
	}
}

func TestOrdinaryClaimRejectsEmptyUpstreamModel(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("empty-model", false)
	key := fixture.seedKey(userID, "empty-model")
	request := fixture.acceptSelf(userID, 1)
	key.candidate.UpstreamModelID = ""
	_, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: userID, AttemptSeq: 1,
		Purpose: PurposeSelf, Candidate: key.candidate,
	})
	requireErrorIs(t, err, ErrInvalidInput)
	var claims int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM dispatch_claims WHERE logical_request_id=?`, request.ID).Scan(&claims); err != nil {
		t.Fatalf("count empty-model claims: %v", err)
	}
	if claims != 0 {
		t.Fatalf("empty-model ordinary claim wrote %d rows", claims)
	}
}
