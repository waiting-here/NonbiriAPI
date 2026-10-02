package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestAdminAccountDeleteHTTPRejectsInvalidIntentBeforeElevation(t *testing.T) {
	f := newLifecycleTestFixture(t, 100)
	userID := seedLifecycleUser(t, f.store.DB(), "admin-delete", false, 100)
	coordinator := mustNewLifecycleCoordinator(t, f.config)
	routes := newLifecycleRouteRecorder()
	if err := RegisterRoutes(routes, routes, coordinator); err != nil {
		t.Fatal(err)
	}
	handler := routes.admins[http.MethodDelete+" "+adminAccountDeleteRoute]
	for _, body := range []string{
		`{"expected_revision":"0","confirmation":"delete"}`,
		`{"expected_revision":"0","confirmation":"DELETE","extra":true}`,
		`{"expected_revision":"0","confirmation":"DELETE","confirmation":"DELETE"}`,
		`{"expected_revision":"00","confirmation":"DELETE"}`,
		`{"expected_revision":"340282366920938463463374607431768211456","confirmation":"DELETE"}`,
	} {
		request := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/admin/api/users/%d", userID), bytes.NewBufferString(body))
		request.SetPathValue("id", fmt.Sprint(userID))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", strings.Repeat("K", 22))
		response := httptest.NewRecorder()
		handler(response, request, AdminPrincipal{UserID: 1})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid intent %s: %d %s", body, response.Code, response.Body.String())
		}
	}
	if f.auth.freshCalls != 0 {
		t.Fatalf("invalid intent consumed elevation %d times", f.auth.freshCalls)
	}
}

type adminDeletionRetirementFunc func(context.Context, int64) (Retirement, error)

func (fn adminDeletionRetirementFunc) BeginUserRetirement(ctx context.Context, userID int64) (Retirement, error) {
	return fn(ctx, userID)
}

func TestAdminAccountDeletionRechecksRevisionAfterDrain(t *testing.T) {
	f := newLifecycleTestFixture(t, 100)
	userID := seedLifecycleUser(t, f.store.DB(), "changed-during-drain", false, 100)
	retirement := &testRetirement{}
	f.config.Retirement = adminDeletionRetirementFunc(func(ctx context.Context, target int64) (Retirement, error) {
		one, _ := db.ParseU128Decimal("1")
		_, err := f.store.DB().ExecContext(ctx, `UPDATE users SET revision=? WHERE id=?`, db.EncodeU128(one), target)
		return retirement, err
	})
	coordinator := mustNewLifecycleCoordinator(t, f.config)
	err := coordinator.DeleteAccountByAdmin(context.Background(), AdminAccountDeletion{
		AdminID: 42, UserID: userID, ExpectedRevision: "0", IdempotencyKey: strings.Repeat("T", 22), DecisionNow: 100,
	})
	if !errors.Is(err, ErrConflict) || retirement.commits != 0 || retirement.aborts != 1 {
		t.Fatalf("revision changed during drain: %v retirement=%+v", err, retirement)
	}
	var users, receipts int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM users WHERE id=?`, userID).Scan(&users); err != nil || users != 1 {
		t.Fatalf("stale deletion changed account: %d %v", users, err)
	}
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM idempotency_records`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("stale deletion left receipt: %d %v", receipts, err)
	}
}
