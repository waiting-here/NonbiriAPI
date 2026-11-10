package inactivity

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type previewAuthority func(context.Context, *sql.Tx, int64) error

func (f previewAuthority) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, id int64) error {
	return f(ctx, tx, id)
}

func TestPreviewBatchesKeepExemptionsAmountsAndAuditSnapshot(t *testing.T) {
	e := fixture(t)
	seedTx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer seedTx.Rollback()
	result, err := seedTx.Exec(`INSERT INTO users(username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
VALUES('preview-admin',1,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,?)`, testNow-100*day, testNow-100*day)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	// The exempt administrator needs no user wallet to preview this policy.
	if err := InitializeTx(context.Background(), seedTx, admin, testNow-100*day); err != nil {
		t.Fatal(err)
	}
	if err := seedTx.Commit(); err != nil {
		t.Fatal(err)
	}
	positive := e.user(t, "preview-positive", 1, 10000, 2000)
	negative := e.user(t, "preview-negative", 1, -100, -200)
	floor := e.user(t, "preview-floor", 1, 105, 50)
	steward := e.user(t, "preview-steward", 5, 10000, 2000)
	banned := e.user(t, "preview-banned", 1, 10000, 2000)
	fresh := e.user(t, "preview-fresh", 1, 10000, 2000)
	if _, err := e.store.DB().Exec(`UPDATE users SET is_banned=1 WHERE id=?`, banned); err != nil {
		t.Fatal(err)
	}
	active(t, e, fresh, testNow, true)
	policy := testPolicy()
	e.policy(t, policy)
	e.s.auth = previewAuthority(func(ctx context.Context, tx *sql.Tx, id int64) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
			t.Fatal("preview lacks its query budget")
		}
		var live bool
		if err := tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id=?`, id).Scan(&live); err != nil {
			return err
		}
		if !live {
			return ErrForbidden
		}
		return nil
	})
	ctx := auth.WithSessionActor(context.Background(), authz.Actor{Kind: authz.ActorAdminSession, UserID: admin, SessionTokenHash: "preview-test-session", SessionGeneration: "preview-test-generation"})
	revision := scalar(t, e.store.DB(), `SELECT revision FROM inactivity_policy`)
	input := PreviewInput{Update: Update{ExpectedRevision: revision, Policy: policy}, Page: "1", Limit: 10}
	beforeOperations := scalar(t, e.store.DB(), `SELECT count(*) FROM credit_operations`)
	beforeAudits := scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_audits`)
	got, err := e.s.Preview(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pagination == nil || got.Pagination.TotalItems != "7" || len(got.Data) != 7 {
		t.Fatalf("preview page=%+v", got)
	}
	type projection struct{ action, exempt, general, game string }
	want := map[string]projection{
		label(admin):    {"none", "administrator", "0", "0"},
		label(positive): {"decay", "", "1000", "500"},
		label(negative): {"decay", "", "0", "0"},
		label(floor):    {"decay", "", "5", "50"},
		label(steward):  {"none", "steward", "0", "0"},
		label(banned):   {"none", "banned", "0", "0"},
		label(fresh):    {"decay", "", "1000", "500"},
	}
	for _, item := range got.Data {
		actual := projection{item.Action, item.ExemptReason, item.GeneralMilli, item.GameMilli}
		if actual != want[item.UserID] {
			t.Fatalf("user=%s got=%+v want=%+v", item.UserID, actual, want[item.UserID])
		}
	}
	if scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_audits`) != beforeAudits+1 || scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_runs`) != 0 || scalar(t, e.store.DB(), `SELECT count(*) FROM credit_operations`) != beforeOperations || e.balance(t, positive, ledger.General) != "10000" {
		t.Fatal("preview changed authoritative amounts or omitted its single audit")
	}
	// The batch decoder agrees with the worker's point reader inside one
	// snapshot, including nullable activity fields and current role state.
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ids := []int64{admin, positive, negative, floor, steward, banned, fresh}
	batch, err := readAccountStates(ctx, tx, ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		one, err := readAccount(ctx, tx, id)
		if err != nil || !reflect.DeepEqual(batch[id], one) {
			t.Fatalf("account state differs for %d: %v", id, err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// A short caller deadline still wins while waiting for the writer, and a
	// cancelled preview neither writes an audit nor poisons the next request.
	held, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err = e.s.Preview(short, input)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("writer wait ignored cancellation: %v", err)
	}
	response := httptest.NewRecorder()
	reply(response, nil, err)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("preview deadline HTTP status=%d", response.Code)
	}
	if err := held.Rollback(); err != nil {
		t.Fatal(err)
	}
	if scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_audits`) != beforeAudits+1 {
		t.Fatal("cancelled preview wrote an audit")
	}
	if _, err := e.s.Preview(ctx, input); err != nil {
		t.Fatal("preview did not recover after cancellation", err)
	}
	policy.Protection = ProtectionPolicy{true, days(45)}
	e.policy(t, policy)
	input.Update = Update{ExpectedRevision: revision + 1, Policy: policy}
	protected, err := e.s.Preview(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range protected.Data {
		if item.UserID == label(positive) && (item.Action != "protection" || item.GeneralMilli != "0" || item.GameMilli != "0") {
			t.Fatalf("protection precedence changed: %+v", item)
		}
	}
}
