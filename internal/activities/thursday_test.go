package activities

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestThursdayCapacityContributionAndSettlement(t *testing.T) {
	opensAt := beijingThursday(2027, 1, 7)
	fixture := newActivityFixture(t, opensAt-60)
	userOne, _ := fixture.seedUser("thursday-one", false)
	userTwo, _ := fixture.seedUser("thursday-two", false)
	fixture.fundUser(userOne, 2000)
	fixture.fundUser(userTwo, 2000)

	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"period": "one"}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-01-07", OpensAt: opensAt,
			Literature: "V me", Entry: "0.1", PerUserLimit: 2,
			PumpsBP: PumpsBP{Platform: 100, Welfare: 100, NextPool: 100},
		})
	if err != nil {
		t.Fatalf("create period: %v", err)
	}
	periodID := created.Value.ID
	fixture.fundPool(created.Value.CurrentPoolID, 1000)
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)

	contribute := func(userID int64) ThursdayContributionResult {
		period := fixture.period(periodID)
		result, _, err := fixture.repository.ContributeThursday(context.Background(), userID,
			fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"user": userID}),
			ThursdayContributionInput{PeriodID: periodID, ExpectedRevision: period.revision})
		if err != nil {
			t.Fatalf("contribute user %d: %v", userID, err)
		}
		return result.Value
	}
	if got := contribute(userOne); got.Count != "1" {
		t.Fatalf("first count = %s", got.Count)
	}
	assertReservedRows(t, fixture.store.DB(), "2") // period + first participant
	if got := contribute(userOne); got.Count != "2" {
		t.Fatalf("repeat count = %s", got.Count)
	}
	assertReservedRows(t, fixture.store.DB(), "2") // repeat adds no future row
	if _, _, err := fixture.repository.ContributeThursday(context.Background(), userOne,
		fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"limit": true}),
		ThursdayContributionInput{PeriodID: periodID, ExpectedRevision: fixture.period(periodID).revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("limit error = %v", err)
	}
	contribute(userTwo)
	assertReservedRows(t, fixture.store.DB(), "3")
	if _, err := fixture.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, userTwo); err != nil {
		t.Fatal(err)
	}

	fixture.clock.Store(opensAt + 86400)
	first, _, err := fixture.repository.RunSettlementStep(context.Background())
	if err != nil || !first.Changed || !first.More {
		t.Fatalf("freeze step = %+v err=%v", first, err)
	}
	second, _, err := fixture.repository.RunSettlementStep(context.Background())
	if err != nil || !second.Changed || second.More || second.ProcessedRows != 2 {
		t.Fatalf("settlement step = %+v err=%v", second, err)
	}
	period := fixture.period(periodID)
	if period.state != PeriodStateSettled || period.frozenPool.Decimal() != "1300" ||
		period.frozenContributionCount.Decimal() != "3" || period.eligibleContributionCount.Decimal() != "2" ||
		period.platformCut.Decimal() != "13" || period.welfareCut.Decimal() != "13" || period.nextCut.Decimal() != "13" ||
		period.payoutTotal.Decimal() != "840" || period.rollover.Decimal() != "421" || period.settlementCursor.Valid {
		t.Fatalf("terminal period = %+v", period)
	}
	assertReservedRows(t, fixture.store.DB(), "0")
	assertPoolBalance(t, fixture.store.DB(), created.Value.CurrentPoolID, "0")
	assertPoolBalance(t, fixture.store.DB(), created.Value.NextPoolID, "434")
	var welfarePool string
	_ = fixture.store.DB().QueryRow(`SELECT id FROM shared_pools WHERE pool_type='welfare'`).Scan(&welfarePool)
	assertPoolBalance(t, fixture.store.DB(), welfarePool, "13")
	assertCodedBalance(t, fixture.store.DB(), "platform", "13")
	assertUserBalance(t, fixture.store.DB(), userOne, "2640")
	assertUserBalance(t, fixture.store.DB(), userTwo, "1900")
	var reason sql.NullString
	if err := fixture.store.DB().QueryRow(`SELECT unpaid_reason FROM thursday_participants WHERE period_id=? AND user_id=?`, periodID, userTwo).Scan(&reason); err != nil || !reason.Valid || reason.String != UnpaidAccountBanned {
		t.Fatalf("banned reason=%+v err=%v", reason, err)
	}
	validateLedgerRecovery(t, fixture.store.DB())
}

func TestThursdayZeroPayoutConsumesParticipantReservations(t *testing.T) {
	opensAt := beijingThursday(2027, 1, 14)
	fixture := newActivityFixture(t, opensAt-1)
	users := make([]int64, 2)
	for i := range users {
		users[i], _ = fixture.seedUser(string(rune('a'+i)), false)
		fixture.fundUser(users[i], 10)
	}
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"zero": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-01-14", OpensAt: opensAt,
			Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{Platform: 9999},
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)
	for _, userID := range users {
		period := fixture.period(created.Value.ID)
		if _, _, err := fixture.repository.ContributeThursday(context.Background(), userID,
			fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"u": userID}),
			ThursdayContributionInput{PeriodID: period.id, ExpectedRevision: period.revision}); err != nil {
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
	var zeroPayouts, payoutOperations int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM thursday_participants WHERE period_id=? AND settled=1 AND hex(payout_mag)=?`, created.Value.ID, "00000000000000000000000000000000").Scan(&zeroPayouts); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='thursday_payout'`).Scan(&payoutOperations); err != nil {
		t.Fatal(err)
	}
	if zeroPayouts != 2 || payoutOperations != 2 {
		t.Fatalf("zero payouts=%d operations=%d", zeroPayouts, payoutOperations)
	}
	assertReservedRows(t, fixture.store.DB(), "0")
	validateLedgerRecovery(t, fixture.store.DB())
}

func assertReservedRows(t *testing.T, database *sql.DB, want string) {
	t.Helper()
	var raw []byte
	if err := database.QueryRow(`SELECT reserved_future_rows FROM credit_capacity WHERE id=1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	value, err := db.DecodeU128(raw)
	if err != nil || value.Decimal() != want {
		t.Fatalf("reserved rows=%v err=%v want=%s", value.Decimal(), err, want)
	}
}

func assertPoolBalance(t *testing.T, database *sql.DB, poolID, want string) {
	t.Helper()
	var accountID int64
	if err := database.QueryRow(`SELECT account_id FROM shared_pools WHERE id=?`, poolID).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	assertAccountBalance(t, database, accountID, want)
}

func assertCodedBalance(t *testing.T, database *sql.DB, code, want string) {
	t.Helper()
	var accountID int64
	if err := database.QueryRow(`SELECT id FROM credit_accounts WHERE code=?`, code).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	assertAccountBalance(t, database, accountID, want)
}

func assertUserBalance(t *testing.T, database *sql.DB, userID int64, want string) {
	t.Helper()
	var accountID int64
	if err := database.QueryRow(`SELECT id FROM credit_accounts WHERE kind='user' AND user_id=?`, userID).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	assertAccountBalance(t, database, accountID, want)
}

func assertAccountBalance(t *testing.T, database *sql.DB, accountID int64, want string) {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	account, err := ledger.ReadAccount(context.Background(), tx, accountID)
	if err != nil || account.Balance.Decimal() != want {
		t.Fatalf("account %d balance=%s err=%v want=%s", accountID, account.Balance.Decimal(), err, want)
	}
}

func validateLedgerRecovery(t *testing.T, database *sql.DB) {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		t.Fatalf("ledger recovery: %v", err)
	}
}
