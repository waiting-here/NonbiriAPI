package claim

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"testing"
)

func TestPrepareAccountDeletionConvergesWithUndispatchedReleaseAndRollsBack(t *testing.T) {
	for _, releaseFirst := range []bool{false, true} {
		name := "deletion-first"
		if releaseFirst {
			name = "release-first"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newClaimFixture(t)
			fixture.useLedgerAccounting()
			userID := fixture.seedUser(name, false)
			fixture.seedLedgerUser(userID, 1_000)
			key := fixture.seedKey(userID, name)
			request := fixture.acceptSelf(userID, 2)
			handle := mustDeletionClaim(t, fixture, request, key, 1, PurposeSelf, 0)

			if releaseFirst {
				attempt, err := fixture.service.ReleaseUndispatched(context.Background(), handle)
				if err != nil || attempt.State != StateReleased {
					t.Fatalf("release before deletion = (%+v,%v)", attempt, err)
				}
			}

			// The service owns no commit: rolling the caller transaction back must
			// restore every request, claim, capacity, and ledger mutation.
			tx := fixture.beginLedgerTx()
			if err := fixture.service.PrepareAccountDeletion(context.Background(), tx, userID, 1_200); err != nil {
				t.Fatalf("prepare deletion before rollback: %v", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatalf("rollback prepared deletion: %v", err)
			}
			attached := loadDeletionRequest(t, fixture.db, request.ID)
			if attached.UserID == nil || *attached.UserID != userID || attached.State == RequestTerminal {
				t.Fatalf("rolled-back request = %+v", attached)
			}

			tx = fixture.beginLedgerTx()
			if err := fixture.service.PrepareAccountDeletion(context.Background(), tx, userID, 1_200); err != nil {
				t.Fatalf("prepare deletion: %v", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit prepared deletion: %v", err)
			}

			deleted := loadDeletionRequest(t, fixture.db, request.ID)
			if deleted.UserID != nil || deleted.State != RequestTerminal ||
				deleted.ResultClass != ResultCancelled || deleted.AccountingDisposition != AccountingRelease ||
				deleted.Destination != DestinationUser || deleted.ReservedMilli != 0 {
				t.Fatalf("deleted undispatched request = %+v", deleted)
			}
			fixture.requireLedgerBalanceForUser(userID, "1000")
			fixture.requireRealLedgerState(request.ID, 0, 0, 3, 6)
			attempt, err := fixture.service.ReleaseUndispatched(context.Background(), handle)
			if err != nil || attempt.State != StateReleased {
				t.Fatalf("release replay after deletion = (%+v,%v)", attempt, err)
			}
			if _, err := fixture.service.TakeForDispatch(context.Background(), handle); !errors.Is(err, ErrNotFound) {
				t.Fatalf("dispatch after deletion error = %v, want unavailable", err)
			}
		})
	}
}

func TestPrepareAccountDeletionConvergesWithDispatchAndCompletion(t *testing.T) {
	for _, completeFirst := range []bool{false, true} {
		name := "deletion-before-completion"
		if completeFirst {
			name = "completion-before-deletion"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newClaimFixture(t)
			fixture.useLedgerAccounting()
			userID := fixture.seedUser(name, false)
			fixture.seedLedgerUser(userID, 1_000)
			key := fixture.seedKey(userID, name)
			request := fixture.acceptSelf(userID, 2)
			handle := mustDeletionClaim(t, fixture, request, key, 1, PurposeSelf, 0)
			dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
			if err != nil {
				t.Fatalf("dispatch claim: %v", err)
			}
			dispatch.Clear()

			complete := func() {
				t.Helper()
				attempt, err := fixture.service.CompleteAttempt(context.Background(), handle, successfulDeletionOutcome())
				if err != nil || attempt.State != StateCommitted {
					t.Fatalf("complete attempt = (%+v,%v)", attempt, err)
				}
				completed, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
					RequestID: request.ID,
					Caller: CallerResult{
						Class:  ResultSuccess,
						Status: 200,
					},
					Disposition:       AccountingCommit,
					ActualChargeMilli: 60,
				})
				if err != nil || completed.State != RequestTerminal {
					t.Fatalf("complete request = (%+v,%v)", completed, err)
				}
			}

			if completeFirst {
				complete()
			}
			tx := fixture.beginLedgerTx()
			if err := fixture.service.PrepareAccountDeletion(context.Background(), tx, userID, 1_300); err != nil {
				t.Fatalf("prepare deletion: %v", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit deletion preparation: %v", err)
			}
			if !completeFirst {
				complete()
			}

			terminal := loadDeletionRequest(t, fixture.db, request.ID)
			wantDestination := DestinationExternal
			wantBalance := "900"
			if completeFirst {
				wantDestination = DestinationUser
				wantBalance = "940"
			}
			if terminal.UserID != nil || terminal.State != RequestTerminal ||
				terminal.AccountingDisposition != AccountingCommit || terminal.Destination != wantDestination {
				t.Fatalf("terminal request after two orders = %+v, want destination %s", terminal, wantDestination)
			}
			fixture.requireLedgerBalanceForUser(userID, wantBalance)
			fixture.requireRealLedgerState(request.ID, 0, 0, 3, 7)
		})
	}
}

func TestPrepareAccountDeletionReleasesUnusedCharityCapacityAndKeepsLatePathExternal(t *testing.T) {
	fixture := newClaimFixture(t)
	fixture.useLedgerAccounting()
	userID := fixture.seedUser("charity-delete-caller", false)
	fixture.seedLedgerUser(userID, 1_000)
	donorID := fixture.seedUser("charity-delete-donor", false)
	key := fixture.seedKey(donorID, "charity-delete")
	donationKeyID := fixture.seedDonationKey(donorID, key, "charity-delete", 0)
	request := fixture.acceptCharity(userID, 3)
	seedDeletionCharityProjection(t, fixture.db, request.ID, userID, fixture.clock.Load())

	dispatched := mustDeletionClaim(t, fixture, request, key, 1, PurposeCharity, donationKeyID)
	dispatch, err := fixture.service.TakeForDispatch(context.Background(), dispatched)
	if err != nil {
		t.Fatalf("dispatch charity claim: %v", err)
	}
	dispatch.Clear()
	released := mustDeletionClaim(t, fixture, request, key, 2, PurposeCharity, donationKeyID)

	tx := fixture.beginLedgerTx()
	if err := fixture.service.PrepareAccountDeletion(context.Background(), tx, userID, 1_400); err != nil {
		t.Fatalf("prepare charity deletion: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit charity deletion preparation: %v", err)
	}
	prepared := loadDeletionRequest(t, fixture.db, request.ID)
	if prepared.UserID != nil || prepared.State != RequestRunning || prepared.Destination != DestinationExternal {
		t.Fatalf("prepared charity request = %+v", prepared)
	}
	fixture.requireRealLedgerState(request.ID, 2, 2, 2, 4)
	fixture.requireLedgerBalanceForUser(userID, "800")
	if attempt, err := fixture.service.ReleaseUndispatched(context.Background(), released); err != nil || attempt.State != StateReleased {
		t.Fatalf("released charity claim replay = (%+v,%v)", attempt, err)
	}
	var reservationUser sql.NullInt64
	if err := fixture.db.QueryRow(`SELECT user_id FROM charity_reservations WHERE logical_request_id=?`, request.ID).Scan(&reservationUser); err != nil {
		t.Fatalf("read detached charity projection: %v", err)
	}
	if reservationUser.Valid {
		t.Fatalf("charity reservation retained user %d", reservationUser.Int64)
	}
	if _, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: userID, AttemptSeq: 3, Purpose: PurposeCharity,
		Candidate: key.candidate, DonationKeyID: donationKeyID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new claim after deletion error = %v, want not found", err)
	}

	if err := fixture.service.MarkResponseStarted(context.Background(), dispatched); err != nil {
		t.Fatalf("response checkpoint after account handoff: %v", err)
	}
	if attempt, err := fixture.service.CompleteAttempt(context.Background(), dispatched, successfulDeletionOutcome()); err != nil || attempt.State != StateCommitted {
		t.Fatalf("late charity attempt = (%+v,%v)", attempt, err)
	}
	if completed, err := fixture.service.CompleteRequest(context.Background(), CompleteRequestInput{
		RequestID: request.ID,
		Caller: CallerResult{
			Class:  ResultSuccess,
			Status: 200,
		},
		Disposition:       AccountingCommit,
		ActualChargeMilli: 100,
	}); err != nil || completed.Destination != DestinationExternal {
		t.Fatalf("late charity request = (%+v,%v)", completed, err)
	}
	fixture.requireLedgerBalanceForUser(userID, "800")
	fixture.requireRealLedgerState(request.ID, 0, 0, 3, 7)
	if err := fixture.db.QueryRow(`SELECT user_id FROM charity_reservations WHERE logical_request_id=?`, request.ID).Scan(&reservationUser); err != nil || reservationUser.Valid {
		t.Fatalf("late charity path restored user = (%+v,%v)", reservationUser, err)
	}
}

func TestPrepareAccountDeletionUsesDeterministicRequestOrder(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("deletion-order", false)
	requests := []Request{
		fixture.acceptSelf(userID, 1),
		fixture.acceptSelf(userID, 1),
		fixture.acceptSelf(userID, 1),
	}
	want := []string{requests[0].ID, requests[1].ID, requests[2].ID}
	sort.Strings(want)
	tx, err := fixture.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin ordered deletion: %v", err)
	}
	defer tx.Rollback()
	if err := fixture.service.PrepareAccountDeletion(context.Background(), tx, userID, 1_500); err != nil {
		t.Fatalf("prepare ordered deletion: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit ordered deletion: %v", err)
	}
	fixture.accounting.mu.Lock()
	got := make([]string, len(fixture.accounting.requests))
	for index, request := range fixture.accounting.requests {
		got[index] = request.RequestID
	}
	fixture.accounting.mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("completed request order = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("completed request order = %v, want %v", got, want)
		}
	}
}

func mustDeletionClaim(
	t *testing.T,
	fixture *claimFixture,
	request Request,
	key testKey,
	attemptSeq int,
	purpose Purpose,
	donationKeyID int64,
) Handle {
	t.Helper()
	handle, err := fixture.service.Claim(context.Background(), ClaimInput{
		RequestID: request.ID, ActorUserID: *request.UserID, AttemptSeq: attemptSeq,
		Purpose: purpose, Candidate: key.candidate, DonationKeyID: donationKeyID,
	})
	if err != nil {
		t.Fatalf("claim deletion attempt %d: %v", attemptSeq, err)
	}
	return handle
}

func successfulDeletionOutcome() AttemptOutcome {
	return AttemptOutcome{
		Kind:            ResultResponse,
		UpstreamStatus:  200,
		ProtocolSuccess: true,
		ResponseStarted: true,
	}
}

func loadDeletionRequest(t *testing.T, database *sql.DB, requestID string) Request {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin request read: %v", err)
	}
	defer tx.Rollback()
	request, err := loadRequestTx(context.Background(), tx, requestID)
	if err != nil {
		t.Fatalf("load deletion request: %v", err)
	}
	return request
}

func seedDeletionCharityProjection(t *testing.T, database *sql.DB, requestID string, userID, now int64) {
	t.Helper()
	zero := make([]byte, 16)
	_, err := database.Exec(`INSERT INTO charity_reservations(
logical_request_id,user_id,model_snapshot,state,pricing_mode,discount_percent,
request_user_price_milli,request_donor_reward_milli,
uncached_user_price_milli,cache_write_user_price_milli,cache_read_user_price_milli,output_user_price_milli,
uncached_donor_reward_milli,cache_write_donor_reward_milli,cache_read_donor_reward_milli,output_donor_reward_milli,
token_reserve_milli,user_reserved_milli,original_charge_milli,user_charge_milli,
donor_reward_total_mag,created_at,updated_at)
VALUES(?,?,'fixture','reserved','per_request',0,
0,0,0,0,0,0,0,0,0,0,0,0,0,0,?,?,?)`, requestID, userID, zero, now, now)
	if err != nil {
		t.Fatalf("seed charity deletion projection: %v", err)
	}
}

func TestPrepareAccountDeletionRejectsInvalidCalls(t *testing.T) {
	fixture := newClaimFixture(t)
	tx, err := fixture.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, callErr := range []error{
		fixture.service.PrepareAccountDeletion(nil, tx, 1, 1),
		fixture.service.PrepareAccountDeletion(context.Background(), nil, 1, 1),
		fixture.service.PrepareAccountDeletion(context.Background(), tx, 0, 1),
		fixture.service.PrepareAccountDeletion(context.Background(), tx, 1, -1),
	} {
		if !errors.Is(callErr, ErrInvalidInput) {
			t.Fatalf("invalid deletion call error = %v", callErr)
		}
	}
}
