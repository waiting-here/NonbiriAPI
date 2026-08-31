package activities

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type inspectingAdminFinalAuth struct {
	calls         int
	usedReadTx    bool
	forcedFailure error
}

func (authorizer *inspectingAdminFinalAuth) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, adminID int64) error {
	authorizer.calls++
	if tx == nil {
		return errors.New("missing final transaction")
	}
	var isAdmin int
	if err := tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id=?`, adminID).Scan(&isAdmin); err != nil {
		return err
	}
	authorizer.usedReadTx = true
	if authorizer.forcedFailure != nil {
		return authorizer.forcedFailure
	}
	if isAdmin != 1 {
		return ErrForbidden
	}
	return nil
}

type rejectingActivityGate struct{ calls int }

func (gate *rejectingActivityGate) AuthorizeUserActivity(context.Context, *sql.Tx, int64) error {
	gate.calls++
	return ErrMaintenance
}

func TestAdminThursdayReadFinalAuthorizationAndMaintenance(t *testing.T) {
	fixture := newActivityFixture(t, beijingThursday(2027, 5, 6)-60)
	authorizer := &inspectingAdminFinalAuth{}
	gate := &rejectingActivityGate{}
	fixture.repository.adminFinalAuth = authorizer
	fixture.repository.userGate = gate

	beforeIdempotency := countActivityRows(t, fixture.store.DB(), `SELECT COUNT(*) FROM idempotency_records`)
	state, err := fixture.repository.GetAdminThursday(context.Background(), fixture.adminID)
	if err != nil || state.Period != nil {
		t.Fatalf("authorized empty state=%+v err=%v", state, err)
	}
	if authorizer.calls != 1 || !authorizer.usedReadTx {
		t.Fatalf("final authorization calls=%d used_read_tx=%v", authorizer.calls, authorizer.usedReadTx)
	}
	if gate.calls != 0 {
		t.Fatalf("admin read crossed maintenance gate %d times", gate.calls)
	}
	if after := countActivityRows(t, fixture.store.DB(), `SELECT COUNT(*) FROM idempotency_records`); after != beforeIdempotency {
		t.Fatalf("admin read wrote idempotency rows: before=%d after=%d", beforeIdempotency, after)
	}

	authorizer.forcedFailure = ErrForbidden
	if _, err := fixture.repository.GetAdminThursday(context.Background(), fixture.adminID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("final authorization rejection error=%v", err)
	}
	callsAfterRejection := authorizer.calls
	if _, err := fixture.repository.GetAdminThursday(context.Background(), 0); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("missing auth context error=%v", err)
	}
	if authorizer.calls != callsAfterRejection {
		t.Fatalf("missing auth context reached final authorizer: calls=%d", authorizer.calls)
	}
}

func TestAdminThursdayRepositorySelection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		fixture := newActivityFixture(t, beijingThursday(2027, 5, 13)-60)
		state, err := fixture.repository.GetAdminThursday(context.Background(), fixture.adminID)
		if err != nil || state.Period != nil {
			t.Fatalf("state=%+v err=%v", state, err)
		}
	})

	t.Run("configured", func(t *testing.T) {
		opensAt := beijingThursday(2027, 5, 20)
		fixture := newActivityFixture(t, opensAt-60)
		created := mustCreateAdminThursdayPeriod(t, fixture, "2027-05-20", opensAt)
		assertAdminThursdayPeriod(t, fixture, created.ID, PeriodStateConfigured)
	})

	t.Run("open", func(t *testing.T) {
		opensAt := beijingThursday(2027, 5, 27)
		fixture := newActivityFixture(t, opensAt-60)
		created := mustCreateAdminThursdayPeriod(t, fixture, "2027-05-27", opensAt)
		if _, err := fixture.store.DB().Exec(`UPDATE thursday_periods SET state='open' WHERE id=?`, created.ID); err != nil {
			t.Fatal(err)
		}
		assertAdminThursdayPeriod(t, fixture, created.ID, PeriodStateOpen)
	})

	t.Run("settling", func(t *testing.T) {
		opensAt := beijingThursday(2027, 6, 3)
		fixture := newActivityFixture(t, opensAt-60)
		created := mustCreateAdminThursdayPeriod(t, fixture, "2027-06-03", opensAt)
		fixture.clock.Store(opensAt + 86400)
		result, _, err := fixture.repository.RunSettlementStep(context.Background())
		if err != nil || !result.Changed || !result.More {
			t.Fatalf("freeze result=%+v err=%v", result, err)
		}
		assertAdminThursdayPeriod(t, fixture, created.ID, PeriodStateSettling)
	})

	t.Run("latest configuration error", func(t *testing.T) {
		firstOpens := beijingThursday(2027, 6, 10)
		fixture := newActivityFixture(t, firstOpens-60)
		first := mustCreateAdminThursdayPeriod(t, fixture, "2027-06-10", firstOpens)
		setThursdayPeriodState(t, fixture, first.ID, PeriodStateConfigurationErr)
		secondOpens := beijingThursday(2027, 6, 17)
		second := mustCreateAdminThursdayPeriod(t, fixture, "2027-06-17", secondOpens)
		setThursdayPeriodState(t, fixture, second.ID, PeriodStateConfigurationErr)
		assertAdminThursdayPeriod(t, fixture, second.ID, PeriodStateConfigurationErr)
	})

	t.Run("active precedes configuration error", func(t *testing.T) {
		firstOpens := beijingThursday(2027, 6, 24)
		fixture := newActivityFixture(t, firstOpens-60)
		failed := mustCreateAdminThursdayPeriod(t, fixture, "2027-06-24", firstOpens)
		setThursdayPeriodState(t, fixture, failed.ID, PeriodStateConfigurationErr)
		activeOpens := beijingThursday(2027, 7, 1)
		active := mustCreateAdminThursdayPeriod(t, fixture, "2027-07-01", activeOpens)
		assertAdminThursdayPeriod(t, fixture, active.ID, PeriodStateConfigured)
	})

	t.Run("settled ignored", func(t *testing.T) {
		opensAt := beijingThursday(2027, 7, 8)
		fixture := newActivityFixture(t, opensAt-60)
		mustCreateAdminThursdayPeriod(t, fixture, "2027-07-08", opensAt)
		fixture.clock.Store(opensAt + 86400)
		if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
			t.Fatal(err)
		}
		state, err := fixture.repository.GetAdminThursday(context.Background(), fixture.adminID)
		if err != nil || state.Period != nil {
			t.Fatalf("settled state=%+v err=%v", state, err)
		}
	})

	t.Run("multiple active invariant", func(t *testing.T) {
		opensAt := beijingThursday(2027, 7, 15)
		fixture := newActivityFixture(t, opensAt-60)
		mustCreateAdminThursdayPeriod(t, fixture, "2027-07-15", opensAt)
		seedSecondActiveThursdayPeriod(t, fixture, "2027-07-22", beijingThursday(2027, 7, 22))
		_, err := fixture.repository.GetAdminThursday(context.Background(), fixture.adminID)
		if !errors.Is(err, ErrInvariant) || !errors.Is(err, ErrUnavailable) {
			t.Fatalf("multiple active error=%v", err)
		}
	})
}

func TestAdminThursdayHTTPStrictReadAndInvariantStatus(t *testing.T) {
	opensAt := beijingThursday(2027, 7, 29)
	fixture := newActivityFixture(t, opensAt-60)
	service, err := NewService(ServiceConfig{Repository: fixture.repository})
	if err != nil {
		t.Fatal(err)
	}
	users, admins := &activityUserRoutes{}, &activityAdminRoutes{}
	if err := RegisterRoutes(users, admins, service); err != nil {
		t.Fatal(err)
	}
	handler := admins.handlers[http.MethodGet+" "+routeAdminThursday]
	if handler == nil {
		t.Fatal("admin Thursday GET was not registered")
	}

	authorizer := &inspectingAdminFinalAuth{}
	fixture.repository.adminFinalAuth = authorizer
	for _, test := range []struct {
		name string
		url  string
		body string
	}{
		{name: "query", url: routeAdminThursday + "?unexpected=1"},
		{name: "body", url: routeAdminThursday, body: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var body *bytes.Reader
			if test.body != "" {
				body = bytes.NewReader([]byte(test.body))
			} else {
				body = bytes.NewReader(nil)
			}
			request := httptest.NewRequest(http.MethodGet, test.url, body)
			recorder := httptest.NewRecorder()
			handler(recorder, request, AdminPrincipal{UserID: fixture.adminID})
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	if authorizer.calls != 0 {
		t.Fatalf("invalid read reached final authorizer %d times", authorizer.calls)
	}

	request := httptest.NewRequest(http.MethodGet, routeAdminThursday, nil)
	recorder := httptest.NewRecorder()
	handler(recorder, request, AdminPrincipal{})
	if recorder.Code != http.StatusUnauthorized || authorizer.calls != 0 {
		t.Fatalf("missing auth status=%d calls=%d body=%s", recorder.Code, authorizer.calls, recorder.Body.String())
	}

	authorizer.forcedFailure = ErrForbidden
	request = httptest.NewRequest(http.MethodGet, routeAdminThursday, nil)
	recorder = httptest.NewRecorder()
	handler(recorder, request, AdminPrincipal{UserID: fixture.adminID})
	if recorder.Code != http.StatusForbidden || authorizer.calls != 1 || !authorizer.usedReadTx {
		t.Fatalf("final reject status=%d calls=%d tx=%v body=%s", recorder.Code, authorizer.calls, authorizer.usedReadTx, recorder.Body.String())
	}

	authorizer.forcedFailure = nil
	beforeIdempotency := countActivityRows(t, fixture.store.DB(), `SELECT COUNT(*) FROM idempotency_records`)
	request = httptest.NewRequest(http.MethodGet, routeAdminThursday, nil)
	request.Header.Set("Idempotency-Key", "ignored-read-key-0001")
	recorder = httptest.NewRecorder()
	handler(recorder, request, AdminPrincipal{UserID: fixture.adminID})
	if recorder.Code != http.StatusOK || recorder.Body.String() != "{\"period\":null}\n" {
		t.Fatalf("empty status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if after := countActivityRows(t, fixture.store.DB(), `SELECT COUNT(*) FROM idempotency_records`); after != beforeIdempotency {
		t.Fatalf("GET wrote idempotency rows: before=%d after=%d", beforeIdempotency, after)
	}

	mustCreateAdminThursdayPeriod(t, fixture, "2027-07-29", opensAt)
	seedSecondActiveThursdayPeriod(t, fixture, "2027-08-05", beijingThursday(2027, 8, 5))
	request = httptest.NewRequest(http.MethodGet, routeAdminThursday, nil)
	recorder = httptest.NewRecorder()
	handler(recorder, request, AdminPrincipal{UserID: fixture.adminID})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("multiple active status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func mustCreateAdminThursdayPeriod(t *testing.T, fixture *activityFixture, periodKey string, opensAt int64) Period {
	t.Helper()
	result, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"period_key": periodKey}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: periodKey, OpensAt: opensAt,
			Literature: "V me", Entry: "0.001", PerUserLimit: 2,
			PumpsBP: PumpsBP{Platform: 100, Welfare: 100, NextPool: 100},
		})
	if err != nil {
		t.Fatalf("create Thursday period %s: %v", periodKey, err)
	}
	return result.Value
}

func seedSecondActiveThursdayPeriod(t *testing.T, fixture *activityFixture, periodKey string, opensAt int64) {
	t.Helper()
	configRevision := fixture.configRevision()
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	input := ThursdayNextMutation{
		ExpectedRevision: configRevision, PeriodKey: periodKey, OpensAt: opensAt,
		Literature: "conflicting active period", Entry: "0.001", PerUserLimit: 1,
		PumpsBP: PumpsBP{Platform: 100, Welfare: 100, NextPool: 100},
	}
	if _, err := fixture.repository.createThursdayPeriodTx(context.Background(), tx, fixture.clock.Load(), input, ledger.AmountFromMilli(1)); err != nil {
		t.Fatalf("seed second active period: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func setThursdayPeriodState(t *testing.T, fixture *activityFixture, periodID, state string) {
	t.Helper()
	if _, err := fixture.store.DB().Exec(`UPDATE thursday_periods SET state=? WHERE id=?`, state, periodID); err != nil {
		t.Fatal(err)
	}
}

func assertAdminThursdayPeriod(t *testing.T, fixture *activityFixture, periodID, state string) {
	t.Helper()
	result, err := fixture.repository.GetAdminThursday(context.Background(), fixture.adminID)
	if err != nil || result.Period == nil || result.Period.ID != periodID || result.Period.State != state {
		t.Fatalf("admin Thursday state=%+v err=%v want_id=%s want_state=%s", result, err, periodID, state)
	}
}

func countActivityRows(t *testing.T, database *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := database.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
