package activities

import (
	"context"
	"database/sql"
	"net/http"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestActivityExportAndDeleteDeidentifiesWithoutRecomputing(t *testing.T) {
	opensAt := beijingThursday(2027, 1, 21)
	fixture := newActivityFixture(t, opensAt-1)
	remainingUser, _ := fixture.seedUser("remaining", false)
	deletedUser, _ := fixture.seedUser("deleted", false)
	fixture.fundUser(remainingUser, 1000)
	fixture.fundUser(deletedUser, 1000)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"delete": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-01-21", OpensAt: opensAt,
			Entry: "0.1", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)
	for _, userID := range []int64{remainingUser, deletedUser} {
		period := fixture.period(created.Value.ID)
		if _, _, err := fixture.repository.ContributeThursday(context.Background(), userID,
			fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"user": userID}),
			ThursdayContributionInput{PeriodID: period.id, ExpectedRevision: period.revision}); err != nil {
			t.Fatal(err)
		}
	}
	exported, err := fixture.repository.ExportUser(context.Background(), deletedUser)
	if err != nil || len(exported.Thursday) != 1 || exported.Thursday[0].Count != "1" || exported.Thursday[0].Contributed != "0.1" {
		t.Fatalf("export=%+v err=%v", exported, err)
	}

	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	deletionNow := opensAt + 1
	if err := fixture.repository.PrepareUserDeletion(context.Background(), tx, deletedUser, deletionNow); err != nil {
		tx.Rollback()
		t.Fatalf("PrepareUserDeletion: %v", err)
	}
	wallet, err := ledger.UserAccount(context.Background(), tx, deletedUser)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	external, err := ledger.CodedAccount(context.Background(), tx, "external")
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	plan, err := ledger.NewAccountDeleteZero(ledger.Meta{OperationID: fixture.operationID(), CreatedAt: deletionNow}, wallet.ID, external.ID)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, deletedUser); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertReservedRows(t, fixture.store.DB(), "2") // period + remaining participant
	var userID sql.NullInt64
	var countRaw, contributedRaw, payoutRaw []byte
	var reason sql.NullString
	if err := fixture.store.DB().QueryRow(`
SELECT user_id,contribution_count,contributed_mag,payout_mag,unpaid_reason
FROM thursday_participants WHERE period_id=? AND user_id IS NULL`, created.Value.ID).Scan(
		&userID, &countRaw, &contributedRaw, &payoutRaw, &reason); err != nil {
		t.Fatal(err)
	}
	count, _ := db.DecodeU128(countRaw)
	contributed, _ := db.DecodeU128(contributedRaw)
	payout, _ := db.DecodeU128(payoutRaw)
	if userID.Valid || count.Decimal() != "1" || contributed.Decimal() != "100" || payout.Decimal() != "0" || !reason.Valid || reason.String != UnpaidAccountDeleted {
		t.Fatalf("deidentified row user=%+v count=%s contributed=%s payout=%s reason=%+v", userID, count.Decimal(), contributed.Decimal(), payout.Decimal(), reason)
	}

	fixture.clock.Store(opensAt + 86400)
	if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	period := fixture.period(created.Value.ID)
	if period.frozenContributionCount.Decimal() != "2" || period.payoutTotal.Decimal() != "100" || period.rollover.Decimal() != "100" {
		t.Fatalf("deletion changed denominator: %+v", period)
	}
	assertUserBalance(t, fixture.store.DB(), remainingUser, "1000")
	validateLedgerRecovery(t, fixture.store.DB())
}

func TestSettledThursdayDeletionPreservesTerminalFacts(t *testing.T) {
	tests := []struct {
		name           string
		periodKey      string
		entry          string
		funding        int64
		pumps          PumpsBP
		withPeer       bool
		banBeforeClose bool
	}{
		{name: "positive payout", periodKey: "2027-02-04", entry: "0.1", funding: 1000},
		{name: "zero payout", periodKey: "2027-02-11", entry: "0.001", pumps: PumpsBP{Platform: 9999}, withPeer: true},
		{name: "banned", periodKey: "2027-02-18", entry: "0.1", banBeforeClose: true},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opensAt := beijingThursday(2027, 2, 4+index*7)
			fixture := newActivityFixture(t, opensAt-1)
			deletedUser, _ := fixture.seedUser("settled-delete", false)
			fixture.fundUser(deletedUser, 2000)
			users := []int64{deletedUser}
			if test.withPeer {
				peerUser, _ := fixture.seedUser("settled-peer", false)
				fixture.fundUser(peerUser, 2000)
				users = append(users, peerUser)
			}
			created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
				fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"case": test.name}), ThursdayNextMutation{
					ExpectedRevision: fixture.configRevision(), PeriodKey: test.periodKey, OpensAt: opensAt,
					Entry: test.entry, PerUserLimit: 1, PumpsBP: test.pumps,
				})
			if err != nil {
				t.Fatal(err)
			}
			if test.funding > 0 {
				fixture.fundPool(created.Value.CurrentPoolID, test.funding)
			}
			fixture.setActivityConfig(true, false, true, 0, 0)
			fixture.clock.Store(opensAt)
			for _, userID := range users {
				period := fixture.period(created.Value.ID)
				if _, _, err := fixture.repository.ContributeThursday(context.Background(), userID,
					fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"user": userID}),
					ThursdayContributionInput{PeriodID: period.id, ExpectedRevision: period.revision}); err != nil {
					t.Fatal(err)
				}
			}
			if test.banBeforeClose {
				if _, err := fixture.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, deletedUser); err != nil {
					t.Fatal(err)
				}
			}
			fixture.clock.Store(opensAt + 86400)
			if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
				t.Fatal(err)
			}

			before, found := readParticipantForDeletionTest(t, fixture, created.Value.ID, deletedUser)
			if !found || !before.settled || before.remaining.Big().Sign() != 0 {
				t.Fatalf("terminal participant=%+v found=%v", before, found)
			}
			periodBefore := fixture.period(created.Value.ID)
			peerBefore := participantRecord{}
			if len(users) > 1 {
				peerBefore, found = readParticipantForDeletionTest(t, fixture, created.Value.ID, users[1])
				if !found {
					t.Fatal("peer participant missing")
				}
			}
			var payoutOperationsBefore int
			if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='thursday_payout'`).Scan(&payoutOperationsBefore); err != nil {
				t.Fatal(err)
			}

			deletionNow := opensAt + 86401
			deleteActivityUser(t, fixture, deletedUser, deletionNow)

			tx, err := fixture.store.DB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			after, err := readParticipantByRefTx(context.Background(), tx, before.periodID, before.participantRef)
			if err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			want := before
			want.userID = sql.NullInt64{}
			want.updatedAt = deletionNow
			if !reflect.DeepEqual(after, want) {
				t.Fatalf("settled fact changed\nbefore=%+v\nafter=%+v\nwant=%+v", before, after, want)
			}
			periodAfter := fixture.period(created.Value.ID)
			periodWant := periodBefore
			periodWant.revision++
			if !reflect.DeepEqual(periodAfter, periodWant) {
				t.Fatalf("period economics changed\nbefore=%+v\nafter=%+v", periodBefore, periodAfter)
			}
			if len(users) > 1 {
				peerAfter, peerFound := readParticipantForDeletionTest(t, fixture, created.Value.ID, users[1])
				if !peerFound || !reflect.DeepEqual(peerAfter, peerBefore) {
					t.Fatalf("peer fact changed\nbefore=%+v\nafter=%+v", peerBefore, peerAfter)
				}
			}
			var payoutOperationsAfter int
			if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='thursday_payout'`).Scan(&payoutOperationsAfter); err != nil {
				t.Fatal(err)
			}
			if payoutOperationsAfter != payoutOperationsBefore {
				t.Fatalf("payout operations changed: before=%d after=%d", payoutOperationsBefore, payoutOperationsAfter)
			}
			assertReservedRows(t, fixture.store.DB(), "0")
			validateLedgerRecovery(t, fixture.store.DB())
		})
	}
}

func TestActivityDeletionFailureRollsBackReservationAndIdentity(t *testing.T) {
	opensAt := beijingThursday(2027, 3, 4)
	fixture := newActivityFixture(t, opensAt-1)
	userID, _ := fixture.seedUser("delete-rollback", false)
	fixture.fundUser(userID, 1000)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"rollback": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-03-04", OpensAt: opensAt,
			Entry: "0.1", PerUserLimit: 1,
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)
	if _, _, err := fixture.repository.ContributeThursday(context.Background(), userID,
		fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"rollback": true}),
		ThursdayContributionInput{PeriodID: created.Value.ID, ExpectedRevision: fixture.period(created.Value.ID).revision}); err != nil {
		t.Fatal(err)
	}
	before, found := readParticipantForDeletionTest(t, fixture, created.Value.ID, userID)
	if !found || before.settled {
		t.Fatalf("participant=%+v found=%v", before, found)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE thursday_periods SET revision=9223372036854775807 WHERE id=?`, created.Value.ID); err != nil {
		t.Fatal(err)
	}

	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.PrepareUserDeletion(context.Background(), tx, userID, opensAt+1); err == nil {
		tx.Rollback()
		t.Fatal("deletion unexpectedly succeeded at terminal revision")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	after, found := readParticipantForDeletionTest(t, fixture, created.Value.ID, userID)
	if !found || !reflect.DeepEqual(after, before) {
		t.Fatalf("failed deletion changed participant\nbefore=%+v\nafter=%+v found=%v", before, after, found)
	}
	assertReservedRows(t, fixture.store.DB(), "2")
	validateLedgerRecovery(t, fixture.store.DB())
}

func readParticipantForDeletionTest(t *testing.T, fixture *activityFixture, periodID string, userID int64) (participantRecord, bool) {
	t.Helper()
	tx, err := fixture.store.DB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	participant, found, err := readParticipantForUserTx(context.Background(), tx, periodID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return participant, found
}

func deleteActivityUser(t *testing.T, fixture *activityFixture, userID, now int64) {
	t.Helper()
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := fixture.repository.PrepareUserDeletion(context.Background(), tx, userID, now); err != nil {
		t.Fatalf("PrepareUserDeletion: %v", err)
	}
	wallet, err := ledger.UserAccount(context.Background(), tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	external, err := ledger.CodedAccount(context.Background(), tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ledger.NewAccountDeleteZero(ledger.Meta{OperationID: fixture.operationID(), CreatedAt: now}, wallet.ID, external.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
