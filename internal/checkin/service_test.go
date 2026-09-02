package checkin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestNewServiceRequiresTransactionDependencies(t *testing.T) {
	fixture := newCheckinFixture(t)
	tests := []ServiceConfig{
		{},
		{Store: fixture.store, Maintenance: fixture.maintenance},
		{Store: fixture.store, FinalAuth: fixture.authorizer},
	}
	for index, config := range tests {
		if _, err := NewService(config); err == nil {
			t.Fatalf("configuration %d was accepted", index)
		}
	}
}

func TestStatusDisabledAndLevelGated(t *testing.T) {
	t.Run("fresh disabled projection is exact", func(t *testing.T) {
		fixture := newCheckinFixture(t)
		userID := fixture.seedUser("disabled")
		status, err := fixture.service.Status(context.Background(), userID)
		if err != nil {
			t.Fatalf("read disabled status: %v", err)
		}
		if status != (Status{Enabled: false}) {
			t.Fatalf("disabled status = %#v", status)
		}
	})

	t.Run("enabled status reads authoritative wallet", func(t *testing.T) {
		fixture := newCheckinFixture(t)
		fixture.configure(db.CheckinModeEnabled, "0", 1_250, 2_750, 9_000)
		userID := fixture.seedUser("enabled")
		fixture.fundUser(userID, 3_500)
		status, err := fixture.service.Status(context.Background(), userID)
		if err != nil {
			t.Fatalf("read enabled status: %v", err)
		}
		want := Status{
			Enabled: true, CheckedInToday: false, Balance: "3.5",
			AwardMinimum: "1.25", AwardMaximum: "2.75", BalanceCap: "9",
		}
		if status != want {
			t.Fatalf("status = %#v, want %#v", status, want)
		}
	})

	t.Run("GET computes but does not persist automatic promotion", func(t *testing.T) {
		fixture := newCheckinFixture(t)
		fixture.configure(db.CheckinModeLevelGated, "0", 1_000, 1_000, 100_000)
		fixture.setConfig(levelThreshold2Key, "100")
		fixture.setConfig(levelThreshold3Key, "200")
		fixture.setConfig(levelThreshold4Key, "300")
		userID := fixture.seedUser("promotion")
		credit, err := db.U128FromBig(big.NewInt(200))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.database.Exec(`UPDATE users SET donation_credit_mag=? WHERE id=?`, db.EncodeU128(credit), userID); err != nil {
			t.Fatal(err)
		}
		status, err := fixture.service.Status(context.Background(), userID)
		if err != nil {
			t.Fatalf("read promoted status: %v", err)
		}
		if !status.Enabled {
			t.Fatal("effective level 3 was not admitted")
		}
		if got := fixture.scalar(`SELECT auto_level FROM users WHERE id=?`, userID); got != 1 {
			t.Fatalf("GET persisted auto level %d", got)
		}
		if got := fixture.u128(`SELECT revision FROM users WHERE id=?`, userID); got.Sign() != 0 {
			t.Fatalf("GET advanced revision to %s", got)
		}
		if _, err := fixture.service.Checkin(context.Background(), userID); err != nil {
			t.Fatalf("check in after promotion: %v", err)
		}
		if got := fixture.scalar(`SELECT auto_level FROM users WHERE id=?`, userID); got != 3 {
			t.Fatalf("POST persisted auto level %d, want 3", got)
		}
		if got := fixture.u128(`SELECT revision FROM users WHERE id=?`, userID); got.Cmp(big.NewInt(1)) != 0 {
			t.Fatalf("POST revision = %s, want 1", got)
		}
	})

	t.Run("low effective level remains unavailable", func(t *testing.T) {
		fixture := newCheckinFixture(t)
		fixture.configure(db.CheckinModeLevelGated, "0", 1_000, 1_000, 100_000)
		userID := fixture.seedUser("low-level")
		status, err := fixture.service.Status(context.Background(), userID)
		if err != nil || status != (Status{Enabled: false}) {
			t.Fatalf("status = %#v, err=%v", status, err)
		}
		if _, err := fixture.service.Checkin(context.Background(), userID); !errors.Is(err, ErrFeatureDisabled) {
			t.Fatalf("POST error = %v, want feature disabled", err)
		}
	})
}

func TestCheckinCommitsLedgerActivityAndLocalDayAtomically(t *testing.T) {
	fixture := newCheckinFixture(t)
	beforeBoundary := time.Date(2027, time.January, 1, 18, 29, 59, 0, time.UTC).Unix()
	fixture.clock.Store(beforeBoundary)
	fixture.configure(db.CheckinModeEnabled, "330", 1_500, 1_500, 250_000)
	userID := fixture.seedUser("local-day")

	result, err := fixture.service.Checkin(context.Background(), userID)
	if err != nil {
		t.Fatalf("first check-in: %v", err)
	}
	if result != (Result{Award: "1.5", Balance: "1.5"}) {
		t.Fatalf("first result = %#v", result)
	}
	var siteDate, operationID string
	var award int64
	if err := fixture.database.QueryRow(`SELECT site_day,award_milli,operation_id FROM checkins WHERE user_id=?`, userID).Scan(&siteDate, &award, &operationID); err != nil {
		t.Fatal(err)
	}
	if siteDate != "2027-01-01" || award != 1_500 || !db.ValidateOpaqueID(operationID, "op_") {
		t.Fatalf("check-in fact = day %q award %d operation %q", siteDate, award, operationID)
	}
	var kind, sourceType, sourceID string
	var actor int64
	if err := fixture.database.QueryRow(`SELECT kind,source_type,source_id,actor_user_id FROM credit_operations WHERE id=?`, operationID).Scan(&kind, &sourceType, &sourceID, &actor); err != nil {
		t.Fatal(err)
	}
	if kind != "checkin_award" || sourceType != "operation" || sourceID != operationID || actor != userID {
		t.Fatalf("ledger fact = %q %q %q actor %d", kind, sourceType, sourceID, actor)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM credit_entries WHERE operation_id=?`, operationID); got != 2 {
		t.Fatalf("credit entry count = %d, want 2", got)
	}
	if fixture.balance(userID).Cmp(big.NewInt(1_500)) != 0 {
		t.Fatalf("wallet balance = %s", fixture.balance(userID))
	}
	activityDay := db.SiteDayKey(beforeBoundary, 330)
	var productActive, checkins int64
	if err := fixture.database.QueryRow(`SELECT product_active,checkins FROM user_activity_daily WHERE day=? AND user_id=?`, activityDay, userID).Scan(&productActive, &checkins); err != nil {
		t.Fatal(err)
	}
	if productActive != 1 || checkins != 1 {
		t.Fatalf("user activity = active %d checkins %d", productActive, checkins)
	}
	if got := fixture.u128(`SELECT checkins FROM site_activity_daily WHERE day=?`, activityDay); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("site check-ins = %s", got)
	}
	if got := fixture.u128(`SELECT distinct_product_users FROM site_activity_daily WHERE day=?`, activityDay); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("site distinct users = %s", got)
	}
	var lockValue string
	if err := fixture.database.QueryRow(`SELECT value FROM site_config WHERE key=?`, timezoneLockKey).Scan(&lockValue); err != nil || lockValue != "1" {
		t.Fatalf("timezone lock = %q, err=%v", lockValue, err)
	}

	if _, err := fixture.service.Checkin(context.Background(), userID); !errors.Is(err, ErrAlreadyCheckedIn) {
		t.Fatalf("same local day error = %v", err)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM checkins WHERE user_id=?`, userID); got != 1 {
		t.Fatalf("duplicate changed check-in count to %d", got)
	}

	fixture.clock.Store(beforeBoundary + 1)
	second, err := fixture.service.Checkin(context.Background(), userID)
	if err != nil {
		t.Fatalf("next local day check-in: %v", err)
	}
	if second != (Result{Award: "1.5", Balance: "3"}) {
		t.Fatalf("second result = %#v", second)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM checkins WHERE user_id=?`, userID); got != 2 {
		t.Fatalf("next local day count = %d", got)
	}
	status, err := fixture.service.Status(context.Background(), userID)
	if err != nil || !status.CheckedInToday || status.Balance != "3" {
		t.Fatalf("authoritative status = %#v, err=%v", status, err)
	}
}

func TestBalanceCapAndHighLevelBypass(t *testing.T) {
	fixture := newCheckinFixture(t)
	fixture.configure(db.CheckinModeEnabled, "0", 250, 250, 1_000)
	userID := fixture.seedUser("cap")
	fixture.fundUser(userID, 1_000)
	beforeOperations := fixture.scalar(`SELECT COUNT(*) FROM credit_operations`)
	if _, err := fixture.service.Checkin(context.Background(), userID); !errors.Is(err, ErrBalanceCap) {
		t.Fatalf("cap error = %v", err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM checkins`) != 0 || fixture.scalar(`SELECT COUNT(*) FROM credit_operations`) != beforeOperations {
		t.Fatal("cap refusal wrote domain or ledger facts")
	}
	if _, err := fixture.database.Exec(`UPDATE users SET level=3 WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Checkin(context.Background(), userID)
	if err != nil {
		t.Fatalf("level 3 bypass: %v", err)
	}
	if result.Balance != "1.25" {
		t.Fatalf("level 3 balance = %q", result.Balance)
	}
}

func TestZeroAwardPersistsOperationAndActivity(t *testing.T) {
	fixture := newCheckinFixture(t)
	fixture.configure(db.CheckinModeEnabled, "0", 0, 0, 0)
	userID := fixture.seedUser("zero")
	beforeSequence := fixture.scalar(`SELECT last_ledger_seq FROM credit_capacity WHERE id=1`)
	result, err := fixture.service.Checkin(context.Background(), userID)
	if err != nil {
		t.Fatalf("zero award check-in: %v", err)
	}
	if result != (Result{Award: "0", Balance: "0"}) {
		t.Fatalf("zero result = %#v", result)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM checkins WHERE user_id=?`, userID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='checkin_award'`) != 1 ||
		fixture.scalar(`SELECT last_ledger_seq FROM credit_capacity WHERE id=1`) != beforeSequence+1 {
		t.Fatal("zero award did not preserve check-in and operation facts")
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM credit_entries e JOIN credit_operations o ON o.id=e.operation_id WHERE o.kind='checkin_award' AND e.delta_sign=0`); got != 2 {
		t.Fatalf("zero award zero-delta ledger entries = %d, want 2", got)
	}
	if got := fixture.scalar(`SELECT SUM(checkins) FROM user_activity_daily WHERE user_id=?`, userID); got != 1 {
		t.Fatalf("zero award user activity = %d", got)
	}
}

func TestAuthorizationPrecedesMaintenanceAndAllWrites(t *testing.T) {
	t.Run("revoked authority wins", func(t *testing.T) {
		fixture := newCheckinFixture(t)
		fixture.configure(db.CheckinModeEnabled, "0", 1, 1, 100)
		userID := fixture.seedUser("revoked")
		fixture.authorizer.setError(resources.ErrForbidden)
		if _, err := fixture.service.Checkin(context.Background(), userID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("error = %v", err)
		}
		if got := fixture.order.snapshot(); !reflect.DeepEqual(got, []string{"auth"}) {
			t.Fatalf("decision order = %v", got)
		}
		if fixture.maintenance.calls.Load() != 0 || fixture.scalar(`SELECT COUNT(*) FROM checkins`) != 0 {
			t.Fatal("maintenance or domain write ran after failed final authorization")
		}
	})

	t.Run("maintenance follows final authorization", func(t *testing.T) {
		fixture := newCheckinFixture(t)
		fixture.configure(db.CheckinModeEnabled, "0", 1, 1, 100)
		userID := fixture.seedUser("maintenance")
		fixture.setMaintenance(true)
		if _, err := fixture.service.Checkin(context.Background(), userID); !errors.Is(err, ErrMaintenance) {
			t.Fatalf("error = %v", err)
		}
		if got := fixture.order.snapshot(); !reflect.DeepEqual(got, []string{"auth", "maintenance"}) {
			t.Fatalf("decision order = %v", got)
		}
		if fixture.scalar(`SELECT COUNT(*) FROM checkins`) != 0 {
			t.Fatal("maintenance refusal wrote check-in")
		}
	})

	t.Run("deleted identity fails final transaction", func(t *testing.T) {
		fixture := newCheckinFixture(t)
		fixture.configure(db.CheckinModeEnabled, "0", 1, 1, 100)
		if _, err := fixture.service.Checkin(context.Background(), 999_999); !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v", err)
		}
		if fixture.scalar(`SELECT COUNT(*) FROM checkins`) != 0 {
			t.Fatal("missing identity wrote check-in")
		}
	})
}

func TestAwardDrawUsesUniformInclusiveRejectionSampling(t *testing.T) {
	minimum, err := drawAward(bytes.NewReader([]byte{3, 0}), 10, 12)
	if err != nil || minimum != 10 {
		t.Fatalf("minimum draw = %d, err=%v", minimum, err)
	}
	maximum, err := drawAward(bytes.NewReader([]byte{2}), 10, 12)
	if err != nil || maximum != 12 {
		t.Fatalf("maximum draw = %d, err=%v", maximum, err)
	}
	if _, err := drawAward(bytes.NewReader(nil), 10, 12); !errors.Is(err, io.EOF) {
		t.Fatalf("exhausted source error = %v", err)
	}
	if got := formatMilliPoints(big.NewInt(-1_250)); got != "-1.25" {
		t.Fatalf("signed formatting = %q", got)
	}
}
