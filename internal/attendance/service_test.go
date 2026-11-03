package attendance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
	"github.com/waiting-here/NonbiriAPI/internal/rewards"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type authority struct {
	admin       int64
	maintenance atomic.Bool
}

func (a *authority) AuthorizeAdmin(_ context.Context, _ *sql.Tx, user int64) error {
	if user != a.admin {
		return authz.ErrForbidden
	}
	return nil
}
func (a *authority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	var alive bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0 AND is_banned=0 AND NOT EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=users.id))", user).Scan(&alive); err != nil {
		return err
	}
	if !alive {
		return authz.ErrForbidden
	}
	return nil
}
func (a *authority) AuthorizeUserActivity(context.Context, *sql.Tx, int64) error {
	if a.maintenance.Load() {
		return maintenance.ErrMaintenanceOn
	}
	return nil
}

type fixture struct {
	store              *db.Store
	s                  *Service
	a                  *authority
	admin, user, other int64
	now, keys          atomic.Int64
}

func setup(t *testing.T) *fixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vault.Close() })
	path := filepath.Join(t.TempDir(), "attendance.sqlite")
	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f := &fixture{store: store}
	f.now.Store(time.Date(2028, 2, 26, 12, 0, 0, 0, time.UTC).Unix())
	f.admin = f.add(t, true)
	f.user = f.add(t, false)
	f.other = f.add(t, false)
	f.a = &authority{admin: f.admin}
	f.exec(t, "INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','480',0) ON CONFLICT(key) DO UPDATE SET value='480'")
	f.exec(t, "UPDATE site_config SET value='1' WHERE key='activities_enabled'")
	f.s, err = New(Config{Database: store.DB(), ReadDatabase: store.ReadDB(), Users: f.a, Admins: f.a, Gate: f.a, Keys: vault, Now: func() time.Time { return time.Unix(f.now.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) add(t *testing.T, admin bool) int64 {
	t.Helper()
	z := make([]byte, 16)
	r, err := f.store.DB().Exec("INSERT INTO users(is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", admin, z, z, z, z, z, z, z, z, f.now.Load(), f.now.Load())
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
		if _, err = ledger.CreateUserAssetAccount(t.Context(), tx, id, asset, f.now.Load()); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return id
}
func (f *fixture) key() string { return fmt.Sprintf("attendance-request-%022d", f.keys.Add(1)) }
func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.store.DB().Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.store.DB().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func (f *fixture) balance(t *testing.T, asset ledger.Asset) string {
	t.Helper()
	tx, err := f.store.ReadDB().BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	a, err := ledger.UserAssetAccount(t.Context(), tx, f.user, asset)
	if err != nil {
		t.Fatal(err)
	}
	return a.Balance.Decimal()
}
func input(mode string) Input {
	return Input{Title: "Daily rewards", Description: "Join each day", StartDate: "2028-02-27", EndDate: "2028-03-05", Mode: mode, Published: true, Tiers: []TierInput{{Days: 1, Rewards: []rewardasset.Item{{Asset: "general", Quantity: "2"}}}, {Days: 3, Rewards: []rewardasset.Item{{Asset: "general", Quantity: "3"}, {Asset: "game", Quantity: "4"}}}}}
}
func (f *fixture) create(t *testing.T, in Input) Period {
	t.Helper()
	p, err := f.s.Create(t.Context(), f.admin, f.key(), in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (f *fixture) sign(t *testing.T, p Period) SignInResult {
	t.Helper()
	out, err := f.s.SignIn(t.Context(), f.user, p.ID, f.key(), SignInInput{siteDate(f.now.Load(), p.TimezoneOffsetMinutes)})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAutomaticAwardsIndependentPeriodsAndBestStreak(t *testing.T) {
	f := setup(t)
	cumulative := f.create(t, input("cumulative"))
	consecutive := f.create(t, input("consecutive"))
	f.now.Store(cumulative.startsAt)
	for _, p := range []Period{cumulative, consecutive} {
		r := f.sign(t, p)
		if len(r.Awards) != 1 || r.Awards[0].Days != 1 || r.Period.TotalDays != 1 {
			t.Fatal(r)
		}
	}
	f.now.Add(86400)
	r := f.sign(t, cumulative)
	if len(r.Awards) != 0 {
		t.Fatal(r)
	}
	f.now.Add(86400)
	r = f.sign(t, cumulative)
	if r.Period.TotalDays != 3 || len(r.Awards) != 1 || len(r.Awards[0].Results) != 2 {
		t.Fatal(r)
	}
	r = f.sign(t, consecutive)
	if r.Period.TotalDays != 2 || r.Period.CurrentStreak != 1 || r.Period.BestStreak != 1 || len(r.Awards) != 0 {
		t.Fatal("broken streak", r)
	}
	f.now.Add(86400)
	f.sign(t, consecutive)
	f.now.Add(86400)
	r = f.sign(t, consecutive)
	if r.Period.BestStreak != 3 || r.Period.TotalDays != 4 || len(r.Awards) != 1 || r.Awards[0].Days != 3 {
		t.Fatal(r)
	}
	f.now.Add(2 * 86400)
	r = f.sign(t, consecutive)
	if r.Period.BestStreak != 3 || r.Period.CurrentStreak != 1 || r.Period.EligibleDays != 3 || len(r.Awards) != 0 {
		t.Fatal("earned progress lost or repeated", r)
	}
	if f.balance(t, ledger.General) != "10000" || f.balance(t, ledger.Game) != "8000" {
		t.Fatal("bundle delivery differs", f.balance(t, ledger.General), f.balance(t, ledger.Game))
	}
	if f.count(t, "SELECT count(*) FROM checkins") != 0 {
		t.Fatal("period attendance changed daily check-in")
	}
	future := input("cumulative")
	future.StartDate = siteDate(f.now.Load()+86400, 480)
	future.EndDate = siteDate(f.now.Load()+8*86400, 480)
	f.create(t, future)
	page, err := f.s.List(t.Context(), f.user, false, 1, 1)
	if err != nil || page.Total != 3 || len(page.Items) != 1 || page.Items[0].Status != "active" {
		t.Fatal("upcoming period displaced ongoing attendance", page, err)
	}
}

func TestSignInRetryAcrossMidnightAndConcurrentAward(t *testing.T) {
	f := setup(t)
	p := f.create(t, input("cumulative"))
	f.now.Store(p.startsAt + 86390)
	key, date := f.key(), siteDate(f.now.Load(), p.TimezoneOffsetMinutes)
	first, err := f.s.SignIn(t.Context(), f.user, p.ID, key, SignInInput{date})
	if err != nil {
		t.Fatal(err)
	}
	f.now.Add(20)
	replay, err := f.s.SignIn(t.Context(), f.user, p.ID, key, SignInInput{date})
	if err != nil || !reflect.DeepEqual(first.SignInReceipt, replay.SignInReceipt) || replay.Period.SignedToday {
		t.Fatal(replay, err)
	}
	retry, err := f.s.SignIn(t.Context(), f.user, p.ID, f.key(), SignInInput{date})
	if err != nil || !retry.AlreadySigned || retry.SignedAt != first.SignedAt {
		t.Fatal(retry, err)
	}
	if _, err = f.s.SignIn(t.Context(), f.user, p.ID, key, SignInInput{siteDate(f.now.Load(), 480)}); !errors.Is(err, ErrConflict) {
		t.Fatal("key accepted a new date", err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := f.s.SignIn(t.Context(), f.user, p.ID, f.key(), SignInInput{siteDate(f.now.Load(), 480)})
			if e != nil {
				failures <- e
			} else if r.Period.TotalDays != 2 || len(r.Awards) != 0 {
				failures <- fmt.Errorf("unexpected concurrent result: %+v", r)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Error(e)
	}
	if f.count(t, "SELECT count(*) FROM attendance_sign_ins WHERE user_id=?", f.user) != 2 || f.count(t, "SELECT count(*) FROM attendance_awards WHERE user_id=?", f.user) != 1 || f.balance(t, ledger.General) != "2000" {
		t.Fatal("retry produced duplicate facts")
	}
	other, err := f.s.SignIn(t.Context(), f.other, p.ID, f.key(), SignInInput{siteDate(f.now.Load(), 480)})
	if err != nil || other.Period.TotalDays != 1 || len(other.Awards) != 1 {
		t.Fatal("another user's progress leaked", other, err)
	}
	f.now.Add(86400)
	date = siteDate(f.now.Load(), 480)
	var winners atomic.Int64
	failures = make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := f.s.SignIn(t.Context(), f.user, p.ID, f.key(), SignInInput{date})
			if e != nil {
				failures <- e
			} else if !r.AlreadySigned {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Error(e)
	}
	if winners.Load() != 1 || f.count(t, "SELECT count(*) FROM attendance_awards WHERE user_id=?", f.user) != 2 || f.balance(t, ledger.Game) != "4000" {
		t.Fatal("concurrent threshold delivered twice", winners.Load())
	}
}

func TestRewardBundleCapacityRollsBackSignInAndCanRetry(t *testing.T) {
	f := setup(t)
	in := input("cumulative")
	in.Tiers = []TierInput{{Days: 1, Rewards: []rewardasset.Item{{Asset: "general", Quantity: "1"}, {Asset: "gacha:limited", Quantity: "1"}}}}
	p := f.create(t, in)
	f.now.Store(p.startsAt)
	f.exec(t, "INSERT INTO gacha_profiles(user_id,limited,month,entered_at,updated_at) VALUES(?,9007199254740991,'2028-02',1,1)", f.user)
	key := f.key()
	date := siteDate(f.now.Load(), 480)
	if _, err := f.s.SignIn(t.Context(), f.user, p.ID, key, SignInInput{date}); !errors.Is(err, rewards.ErrCapacity) {
		t.Fatal(err)
	}
	for _, table := range []string{"attendance_progress", "attendance_sign_ins", "attendance_awards"} {
		if f.count(t, "SELECT count(*) FROM "+table) != 0 {
			t.Fatal("failed delivery persisted", table)
		}
	}
	if f.balance(t, ledger.General) != "0" || f.count(t, "SELECT count(*) FROM idempotency_records WHERE scope='activity'") != 0 {
		t.Fatal("partial bundle or failed replay committed")
	}
	f.exec(t, "UPDATE gacha_profiles SET limited=0 WHERE user_id=?", f.user)
	r, err := f.s.SignIn(t.Context(), f.user, p.ID, key, SignInInput{date})
	if err != nil || len(r.Awards) != 1 || f.balance(t, ledger.General) != "1000" {
		t.Fatal(r, err)
	}
}

func TestCalendarBoundariesLeapDayAndTimezoneFreeze(t *testing.T) {
	for _, offset := range []int{-720, 0, 330, 840} {
		t.Run(fmt.Sprint(offset), func(t *testing.T) {
			f := setup(t)
			f.exec(t, "UPDATE site_config SET value=? WHERE key='site_timezone_offset_minutes'", fmt.Sprint(offset))
			in := input("consecutive")
			in.StartDate = "2028-02-28"
			in.EndDate = "2028-03-01"
			p := f.create(t, in)
			if err := f.store.SetSiteTimezoneOffsetMinutes(0); !errors.Is(err, db.ErrConflict) {
				t.Fatal("period did not freeze timezone", err)
			}
			f.now.Store(p.startsAt - 1)
			if _, err := f.s.SignIn(t.Context(), f.user, p.ID, f.key(), SignInInput{siteDate(f.now.Load(), offset)}); !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
			for day := range 3 {
				f.now.Store(p.startsAt + int64(day)*86400)
				r := f.sign(t, p)
				if r.Period.CurrentStreak != day+1 {
					t.Fatal(r)
				}
				if day == 1 && r.SiteDate != "2028-02-29" {
					t.Fatal("leap day", r)
				}
			}
			f.now.Store(p.endsAt)
			if _, err := f.s.SignIn(t.Context(), f.user, p.ID, f.key(), SignInInput{siteDate(f.now.Load(), offset)}); !errors.Is(err, ErrClosed) {
				t.Fatal("end was inclusive", err)
			}
		})
	}
}

func TestPeriodEditingPermissionsAndReaderIsolation(t *testing.T) {
	f := setup(t)
	in := input("cumulative")
	invalid := in
	invalid.StartDate = siteDate(f.now.Load(), 480)
	if _, err := f.s.Create(t.Context(), f.admin, f.key(), invalid); !errors.Is(err, ErrInvalid) {
		t.Fatal("same-day start", err)
	}
	invalid = in
	invalid.EndDate = invalid.StartDate
	if _, err := f.s.Create(t.Context(), f.admin, f.key(), invalid); !errors.Is(err, ErrInvalid) {
		t.Fatal("unreachable tier", err)
	}
	if _, err := f.s.Create(t.Context(), f.user, f.key(), in); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("nonadmin configured period", err)
	}
	p := f.create(t, in)
	draft := in
	draft.Published = false
	f.create(t, draft)
	writer, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	page, err := f.s.List(ctx, f.user, false, 1, 1)
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	admin, err := f.s.List(ctx, f.admin, true, 2, 1)
	if err != nil || admin.Total != 2 || len(admin.Items) != 1 {
		t.Fatal(admin, err)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	f.now.Store(p.startsAt)
	f.sign(t, p)
	edit := Edit{Input: in, ExpectedRevision: p.Revision}
	edit.Title = "Updated title"
	edit.Description = "Changed\r\ncopy"
	updated, err := f.s.Update(t.Context(), f.admin, p.ID, f.key(), edit)
	if err != nil || updated.ConfigEditable || updated.Description != "Changed\ncopy" {
		t.Fatal(updated, err)
	}
	edit.ExpectedRevision = updated.Revision
	edit.Tiers = append([]TierInput(nil), in.Tiers...)
	edit.Tiers[0].Days = 2
	if _, err = f.s.Update(t.Context(), f.admin, p.ID, f.key(), edit); !errors.Is(err, ErrConflict) {
		t.Fatal("reward rights rewritten", err)
	}
	f.a.maintenance.Store(true)
	page, err = f.s.List(t.Context(), f.user, false, 1, 20)
	if err != nil || page.Available || page.UnavailableReason != "maintenance" {
		t.Fatal(page, err)
	}
	f.a.maintenance.Store(false)
	f.exec(t, "UPDATE site_config SET value='0' WHERE key='activities_enabled'")
	page, err = f.s.List(t.Context(), f.user, false, 1, 20)
	if err != nil || page.Available || page.Items[0].CanSignIn {
		t.Fatal(page, err)
	}
	if _, err = f.s.SignIn(t.Context(), f.user, p.ID, f.key(), SignInInput{siteDate(f.now.Load(), 480)}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestExportAndDeletionSerializeAgainstSignIn(t *testing.T) {
	f := setup(t)
	p := f.create(t, input("cumulative"))
	f.now.Store(p.startsAt)
	f.sign(t, p)
	tx, err := f.store.ReadDB().BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.s.ExportUserTx(t.Context(), tx, f.user, f.now.Load(), 10)
	if err != nil || len(out.SignIns) != 1 || len(out.Periods) != 1 || !out.Periods[0].Tiers[0].Awarded {
		t.Fatal(out, err)
	}
	other, err := f.s.ExportUserTx(t.Context(), tx, f.other, f.now.Load(), 10)
	if err != nil || len(other.SignIns) != 0 {
		t.Fatal(other, err)
	}
	tx.Rollback()
	f.now.Add(86400)
	f.sign(t, p)
	limited, err := f.store.ReadDB().BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ExportUserTx(t.Context(), limited, f.user, f.now.Load(), 1); !errors.Is(err, ErrExportLimit) {
		t.Fatal("export silently truncated attendance", err)
	}
	limited.Rollback()
	deletion, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer deletion.Rollback()
	if _, err = deletion.ExecContext(t.Context(), "INSERT INTO user_deletion_markers(user_id) VALUES(?)", f.user); err != nil {
		t.Fatal(err)
	}
	if err = DeleteUserTx(t.Context(), deletion, f.user); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, e := f.s.SignIn(t.Context(), f.user, p.ID, f.key(), SignInInput{siteDate(f.now.Load(), 480)})
		done <- e
	}()
	if err = deletion.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("late sign-in survived deletion", err)
	}
	for _, table := range []string{"attendance_progress", "attendance_sign_ins", "attendance_awards"} {
		if f.count(t, "SELECT count(*) FROM "+table+" WHERE user_id=?", f.user) != 0 {
			t.Fatal("retained deleted account", table)
		}
	}
	if f.count(t, "SELECT count(*) FROM idempotency_records WHERE scope='activity'") != 0 {
		t.Fatal("deleted user's replay remained")
	}
}

func TestLargePeriodConfigurationReplayKeepsCompactReceipts(t *testing.T) {
	f := setup(t)
	in := input("cumulative")
	in.Description = strings.Repeat("文", 2000)
	in.EndDate = "2029-02-26"
	in.Tiers = nil
	for day := 1; day <= MaxTiers; day++ {
		items := []rewardasset.Item{{Asset: "general", Quantity: "9000000000000"}, {Asset: "game", Quantity: "9000000000000"}}
		if day <= 292 {
			items = append(items, rewardasset.Item{Asset: "sketch_paper", Quantity: "9000000000000"})
		}
		in.Tiers = append(in.Tiers, TierInput{Days: day, Rewards: items})
	}
	key := f.key()
	p, err := f.s.Create(t.Context(), f.admin, key, in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) <= idempotency.MaxResponseBytes {
		t.Fatal("fixture must exceed the shared replay body budget", len(raw), err)
	}
	replay, err := f.s.Create(t.Context(), f.admin, key, in)
	if err != nil || replay.ID != p.ID || len(replay.Tiers) != MaxTiers {
		t.Fatal("large create replay", replay.ID, err)
	}
	edit := Edit{Input: in, ExpectedRevision: p.Revision}
	edit.Title = "Edited future period"
	editKey := f.key()
	updated, err := f.s.Update(t.Context(), f.admin, p.ID, editKey, edit)
	if err != nil || updated.Revision != p.Revision+1 {
		t.Fatal(updated.Revision, err)
	}
	updated, err = f.s.Update(t.Context(), f.admin, p.ID, editKey, edit)
	if err != nil || updated.Revision != p.Revision+1 {
		t.Fatal("edit replay repeated configuration", updated.Revision, err)
	}
	edit.Title = "Conflicting request"
	if _, err = f.s.Update(t.Context(), f.admin, p.ID, editKey, edit); !errors.Is(err, ErrConflict) {
		t.Fatal("changed body reused a replay key", err)
	}
}
