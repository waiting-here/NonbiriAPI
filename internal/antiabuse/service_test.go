package antiabuse

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type admission struct{ denied atomic.Bool }

func (a *admission) AuthorizeChatAcceptance(context.Context, *sql.Tx, int64, int64) error {
	if a.denied.Load() {
		return charityrouting.ErrUnavailable
	}
	return nil
}

type abuseFixture struct {
	t       *testing.T
	store   *db.Store
	service *Service
	clock   atomic.Int64
	gate    *admission
	claims  *claim.Service
	bans    atomic.Int64
}

func newAbuseFixture(t *testing.T) *abuseFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "abuse.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &abuseFixture{t: t, store: store, gate: &admission{}}
	f.clock.Store(time.Now().Unix())
	claims, err := claim.New(claim.Dependencies{DB: store.DB(), Secrets: vault, Acceptance: f.gate})
	if err != nil {
		t.Fatal(err)
	}
	if err := claims.InitializeUsageTotals(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.claims = claims
	flow, err := flowcontrol.New(flowcontrol.Config{UserLimits: flowcontrol.DBUserLimitResolver(store)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = flow.Close() })
	f.service, err = NewService(ServiceConfig{Database: store.DB(), Rejections: claims, Now: func() time.Time { return time.Unix(f.clock.Load(), 0) }, OnBan: func(int64) { f.bans.Add(1) },
		BeginUserRetirement: func(_ context.Context, userID int64) (Retirement, error) { return flow.BeginUserRetirement(userID) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.service.Close() })
	f.set("charity_enabled", "1")
	return f
}
func (f *abuseFixture) set(key, value string) {
	f.t.Helper()
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value=? WHERE key=?`, value, key); err != nil {
		f.t.Fatal(err)
	}
}
func (f *abuseFixture) user() int64 {
	f.t.Helper()
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	zero := db.EncodeU128(db.U128{})
	result, err := tx.Exec(`INSERT INTO users(username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
VALUES('Test account',?,?,?,?,?,?,?,?,?,?)`, zero, zero, zero, zero, zero, zero, zero, zero, f.clock.Load(), f.clock.Load())
	if err != nil {
		f.t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := ledger.CreateUserAccount(context.Background(), tx, id, f.clock.Load()); err != nil {
		f.t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(strconv.FormatInt(id, 10)))
	if _, err := tx.Exec(`INSERT INTO caller_keys(user_id,generation,key_hash,key_created_at,updated_at) VALUES(?,1,?,?,?)`, id, hash[:], f.clock.Load(), f.clock.Load()); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
	return id
}
func (f *abuseFixture) scalar(query string, args ...any) int64 {
	f.t.Helper()
	var value int64
	if err := f.store.DB().QueryRow(query, args...).Scan(&value); err != nil {
		f.t.Fatal(err)
	}
	return value
}
func (f *abuseFixture) history(userID int64) ledger.HistoryPage {
	f.t.Helper()
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	page, err := ledger.UserHistory(context.Background(), tx, userID, f.clock.Load(), ledger.HistoryFilter{Page: 1, PageSize: 100})
	if err != nil {
		f.t.Fatal(err)
	}
	return page
}

func TestShortRejectionPenaltyLogAndRestrictionsCommitTogether(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	other := f.user()
	for key, value := range map[string]string{KeyCharityViolationDeductMilli: "7", KeyCharityViolationBanSeconds: "15", KeyCharityViolationBanThreshold: "1", KeyCharityViolationWindowBanSeconds: "30", KeyCharitySuspendThreshold: "1", KeyCharitySuspendDurationSeconds: "60"} {
		f.set(key, value)
	}
	result, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 2)
	if err != nil || result == nil || result.Actual != 2 || result.Minimum != 20 || !db.ValidateOpaqueID(result.RequestID, "req_") {
		t.Fatalf("rejection=%+v err=%v", result, err)
	}
	page := f.history(user)
	if page.CurrentBalance != "-0.007" || len(page.Data) != 1 || page.Data[0].RequestID == nil || *page.Data[0].RequestID != result.RequestID {
		t.Fatalf("history=%+v", page)
	}
	if f.scalar(`SELECT banned_until FROM users WHERE id=?`, user) != f.clock.Load()+30 || f.scalar(`SELECT charity_suspended_until FROM users WHERE id=?`, user) != f.clock.Load()+60 || f.scalar(`SELECT generation FROM caller_keys WHERE user_id=?`, user) != 2 || f.bans.Load() != 1 {
		t.Fatal("restrictions or credential revocation missing")
	}
	if f.scalar(`SELECT COUNT(*) FROM dispatch_claims`) != 0 || f.scalar(`SELECT COUNT(*) FROM charity_reservations`) != 0 || f.scalar(`SELECT attempt_count FROM request_logs WHERE logical_request_id=?`, result.RequestID) != 0 {
		t.Fatal("rejected request reached dispatch/reservation")
	}
	if f.scalar(`SELECT COUNT(*) FROM request_logs WHERE logical_request_id=? AND caller_status=400 AND error_code='content_too_short' AND error_diag='content has 2 characters; minimum is 20' AND usage_unknown=0`, result.RequestID) != 1 {
		t.Fatal("safe rejection log missing")
	}
	if f.history(other).CurrentBalance != "0" || f.scalar(`SELECT is_banned FROM users WHERE id=?`, other) != 0 {
		t.Fatal("other account changed")
	}
	f.clock.Add(30 * 24 * 60 * 60)
	if f.history(user).Data[0].RequestID != nil {
		t.Fatal("expired request remains linked")
	}
}

func TestRejectedPolicyRollsBackLogPenaltyAndWindowOnFailure(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	f.set(KeyCharityViolationDeductMilli, "1000")
	f.set(KeyCharityViolationBanSeconds, "60")
	if _, err := f.store.DB().Exec(`CREATE TRIGGER fail_abuse_revoke BEFORE UPDATE ON caller_keys BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1); err == nil {
		t.Fatal("injected revocation failure accepted")
	}
	if f.scalar(`SELECT COUNT(*) FROM request_logs`) != 0 || f.scalar(`SELECT COUNT(*) FROM logical_requests`) != 0 || f.history(user).CurrentBalance != "0" || len(f.service.windows) != 0 || f.bans.Load() != 0 {
		t.Fatal("partial penalty, log or window survived rollback")
	}
	if _, err := f.store.DB().Exec(`DROP TRIGGER fail_abuse_revoke`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1); err != nil {
		t.Fatal(err)
	}
	if f.history(user).CurrentBalance != "-1" {
		t.Fatal("retry did not apply exactly once")
	}
}

func TestShortPolicyRechecksGatesThresholdChangesAndDeletion(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	f.set(KeyCharitySuspendThreshold, "2")
	f.set(KeyCharitySuspendWindowSeconds, "10")
	f.set(KeyCharitySuspendDurationSeconds, "30")
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(10)
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1); err != nil {
		t.Fatal(err)
	}
	if f.scalar(`SELECT COALESCE(charity_suspended_until,0) FROM users WHERE id=?`, user) != 0 {
		t.Fatal("exact window boundary retained expired event")
	}
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1); !errors.Is(err, charityrouting.ErrCharitySuspended) {
		t.Fatalf("suspended error=%v", err)
	}
	f.clock.Add(30)
	f.set(KeyCharityMinChars, "0")
	if rejected, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 0); err != nil || rejected != nil {
		t.Fatalf("disabled minimum=%+v %v", rejected, err)
	}
	f.set(KeyCharityMinChars, "20")
	f.set("charity_enabled", "0")
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1); !errors.Is(err, charityrouting.ErrFeatureDisabled) {
		t.Fatal(err)
	}
	f.set("charity_enabled", "1")
	f.gate.denied.Store(true)
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1); !errors.Is(err, charityrouting.ErrUnavailable) {
		t.Fatal(err)
	}
	f.gate.denied.Store(false)
	// Detach request/log identities through their normal deletion owner.
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.claims.PrepareAccountDeletion(context.Background(), tx, user, f.clock.Load()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM credit_accounts WHERE user_id=?`, user); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, user); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.service.ForgetUser(user)
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1); !errors.Is(err, charityrouting.ErrUnauthorized) {
		t.Fatal(err)
	}
	if len(f.service.windows) != 0 {
		t.Fatal("deleted account window recreated")
	}
}

func TestRPMOnlyCountsPersonalLimitAndRevokesAtThreshold(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	f.set(KeyRPMBanThreshold, "2")
	f.set(KeyRPMBanDurationSeconds, "90")
	for _, reason := range []ratelimit.RPMReason{ratelimit.RPMAllowed, ratelimit.RPMGlobalLimit, ratelimit.RPMCapacity} {
		f.service.RPMDenied(context.Background(), user, reason)
	}
	if len(f.service.windows) != 0 {
		t.Fatal("unrelated denials counted")
	}
	f.service.RPMDenied(context.Background(), user, ratelimit.RPMUserLimit)
	if f.scalar(`SELECT is_banned FROM users WHERE id=?`, user) != 0 {
		t.Fatal("premature ban")
	}
	f.service.RPMDenied(context.Background(), user, ratelimit.RPMUserLimit)
	if f.scalar(`SELECT banned_until FROM users WHERE id=?`, user) != f.clock.Load()+90 || f.bans.Load() != 1 || f.scalar(`SELECT COUNT(*) FROM request_logs`) != 0 {
		t.Fatal("RPM consequence mismatch")
	}
}

func TestConcurrentShortCallsKeepUniqueLogsExactBalanceAndConservation(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	f.set(KeyCharityViolationDeductMilli, "7")
	const count = 24
	var wg sync.WaitGroup
	failures := make(chan error, count)
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.service.RecordShort(context.Background(), user, "[公益]provider/model", 1)
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	page := f.history(user)
	if page.CurrentBalance != "-0.168" || page.Total != strconv.Itoa(count) || f.scalar(`SELECT COUNT(*) FROM request_logs`) != count || f.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='anti_abuse_penalty'`) != count {
		t.Fatalf("concurrent balance/count=%+v", page)
	}
	seen := map[string]bool{}
	for _, entry := range page.Data {
		if entry.RequestID == nil || seen[*entry.RequestID] {
			t.Fatal("missing/duplicate request association")
		}
		seen[*entry.RequestID] = true
	}
	if f.scalar(`SELECT COUNT(*) FROM credit_entries WHERE account_kind_snapshot='external' AND delta_sign=1`) != count {
		t.Fatal("external ledger conservation missing")
	}
}
