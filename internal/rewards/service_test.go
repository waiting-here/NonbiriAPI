package rewards

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/gacha"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type authority struct{ admin int64 }

func (a authority) AuthorizeAdmin(_ context.Context, _ *sql.Tx, user int64) error {
	if user != a.admin {
		return authz.ErrForbidden
	}
	return nil
}
func (a authority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	var ok bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0 AND is_banned=0 AND NOT EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=users.id))", user).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return authz.ErrForbidden
	}
	return nil
}
func (a authority) AuthorizeUserActivity(context.Context, *sql.Tx, int64) error { return nil }

type fixture struct {
	db                 *sql.DB
	s                  *Service
	at                 atomic.Int64
	admin, user, other int64
	vault              *secret.Vault
	continuity         *continuity.Service
	abuse              *antiabuse.Service
	abuseConfig        antiabuse.ServiceConfig
	flow               *flowcontrol.Controller
	keys               atomic.Int64
	retirements, bans  atomic.Int64
	cancellations      atomic.Int64
	finalized          atomic.Int64
}

type unusedRejections struct{}

func (unusedRejections) RecordCharityRejectionTx(context.Context, *sql.Tx, int64, string, string, int, int, int64) error {
	return errors.New("unexpected charity rejection")
}
func (unusedRejections) RecordRejectionTx(context.Context, *sql.Tx, int64, requestattempt.Fact, int64) error {
	return errors.New("unexpected forwarded request rejection")
}

func setup(t *testing.T) *fixture {
	t.Helper()
	vault, e := secret.New(make([]byte, secret.MasterKeyBytes))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { vault.Close() })
	p := filepath.Join(t.TempDir(), "rewards.sqlite")

	dbfixture.Materialize(t, p, vault)
	store, e := db.Open(p, vault)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	f := &fixture{db: store.DB(), vault: vault}
	f.at.Store(1800000000)
	f.continuity, e = continuity.New(f.db, vault)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.continuity.Close() })
	f.admin = f.add(t, true)
	f.user = f.add(t, false)
	f.other = f.add(t, false)
	a := authority{f.admin}
	f.s, e = New(Config{Database: f.db, Users: a, Admins: a, Gate: a, Keys: vault, Now: func() time.Time { return time.Unix(f.at.Load(), 0) }})
	if e != nil {
		t.Fatal(e)
	}
	f.flow, e = flowcontrol.New(flowcontrol.Config{UserLimits: flowcontrol.DBUserLimitResolver(store)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.flow.Close() })
	f.abuseConfig = antiabuse.ServiceConfig{Database: f.db, Continuity: f.continuity, Rejections: unusedRejections{}, Now: f.s.config.Now,
		BeginUserRetirement: func(_ context.Context, user int64) (antiabuse.Retirement, error) {
			f.retirements.Add(1)
			return f.flow.BeginUserRetirement(user)
		},
		OnBan: func(int64) { f.bans.Add(1) },
		CancelUserDuelsTx: func(context.Context, *sql.Tx, int64, string, int64) (func(bool), error) {
			f.cancellations.Add(1)
			return func(committed bool) {
				if committed {
					f.finalized.Add(1)
				}
			}, nil
		},
	}
	f.abuse, e = antiabuse.NewService(f.abuseConfig)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.abuse.Close() })
	if e := f.s.AttachAbuse(f.abuse); e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec("INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','480',0) ON CONFLICT(key) DO UPDATE SET value='480'"); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *fixture) add(t *testing.T, admin bool) int64 {
	t.Helper()
	z := make([]byte, 16)
	r, e := f.db.Exec("INSERT INTO users(is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", admin, z, z, z, z, z, z, z, z, f.at.Load(), f.at.Load())
	if e != nil {
		t.Fatal(e)
	}
	id, e := r.LastInsertId()
	if e != nil {
		t.Fatal(e)
	}
	tx, e := f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if !admin {
		discord := "test-" + strconv.FormatInt(id, 10)
		if _, e := tx.Exec("UPDATE users SET discord_id=? WHERE id=?", discord, id); e != nil {
			t.Fatal(e)
		}
		if _, e := f.continuity.BindUserTx(t.Context(), tx, id); e != nil {
			t.Fatal(e)
		}
		hash := sha256.Sum256([]byte(strconv.FormatInt(id, 10)))
		if _, e := tx.Exec("INSERT INTO caller_keys(user_id,generation,key_hash,key_created_at,updated_at) VALUES(?,1,?,?,?)", id, hash[:], f.at.Load(), f.at.Load()); e != nil {
			t.Fatal(e)
		}
	}
	for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
		if _, e = ledger.CreateUserAssetAccount(t.Context(), tx, id, asset, f.at.Load()); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return id
}
func (f *fixture) nextKey() string {
	return "reward-redemption-key-" + strconv.FormatInt(f.keys.Add(1), 10)
}
func input(kind string) Input {
	return Input{Kind: kind, Title: "Welcome", Enabled: true, Conditions: Condition{Op: "all"}, Rewards: []rewardasset.Item{{Asset: "general", Quantity: "10.125"}, {Asset: "sketch_paper", Quantity: "2"}, {Asset: "gacha:limited", Quantity: "3"}}}
}
func (f *fixture) create(t *testing.T, in Input, key string) Campaign {
	t.Helper()
	c, e := f.s.Create(t.Context(), f.admin, "reward-create-key-"+key, in)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func (f *fixture) balance(t *testing.T, user int64, asset ledger.Asset) string {
	t.Helper()
	tx, e := f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	a, e := ledger.UserAssetAccount(t.Context(), tx, user, asset)
	if e != nil {
		t.Fatal(e)
	}
	return a.Balance.Decimal()
}
func TestBroadcastFreezesRecipientsAndRecoversExactlyOnce(t *testing.T) {
	f := setup(t)
	in := input("broadcast")
	in.Conditions = Condition{Op: "and", Children: []Condition{{Op: "compare", Field: "level", Compare: "gte", Value: "1"}, {Op: "not", Children: []Condition{{Op: "compare", Field: "banned", Compare: "eq", Value: "true"}}}}}
	if _, e := f.db.Exec("UPDATE users SET is_banned=1 WHERE id=?", f.other); e != nil {
		t.Fatal(e)
	}
	c := f.create(t, in, "broadcast")
	if c.Matched != 1 {
		t.Fatal(c.Matched)
	}
	late := f.add(t, false)
	if _, e := f.db.Exec("UPDATE users SET is_banned=0 WHERE id=?", f.other); e != nil {
		t.Fatal(e)
	}
	if n, e := f.s.ProcessBatch(t.Context(), 1); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	replay := f.create(t, in, "broadcast")
	if replay.ID != c.ID {
		t.Fatal("duplicate campaign")
	}
	if n, e := f.s.ProcessBatch(t.Context(), 10); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if f.balance(t, f.user, ledger.General) != "10125" || f.balance(t, f.other, ledger.General) != "0" || f.balance(t, late, ledger.General) != "0" {
		t.Fatal("recipient or amount drift")
	}
}
func TestCodeQuotaConcurrencyAndExpiredReplay(t *testing.T) {
	f := setup(t)
	one := int64(1)
	end := f.at.Load() + 1
	in := input("code")
	in.Code = "gift-test"
	in.Quota = &one
	in.EndsAt = &end
	c := f.create(t, in, "quota")
	var wg sync.WaitGroup
	var success atomic.Int64
	for _, user := range []int64{f.user, f.other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if result, e := f.s.Redeem(t.Context(), user, f.nextKey(), "  gift-test  "); e == nil {
				if result.AlreadyClaimed {
					t.Error("first redemption reported as already claimed")
				}
				success.Add(1)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal("quota success", success.Load())
	}
	var winner int64
	if e := f.db.QueryRow("SELECT user_id FROM reward_claims WHERE campaign_id=?", c.ID).Scan(&winner); e != nil {
		t.Fatal(e)
	}
	f.at.Add(5)
	r, e := f.s.Redeem(t.Context(), winner, f.nextKey(), c.Code)
	if e != nil || len(r.Results) != 3 || !r.AlreadyClaimed {
		t.Fatal(r, e)
	}
	if f.balance(t, winner, ledger.General) != "10125" {
		t.Fatal("replay issued twice")
	}
	loser := f.user
	if loser == winner {
		loser = f.other
	}
	if _, e = f.s.Redeem(t.Context(), loser, f.nextKey(), c.Code); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
}
func TestBundleRollbackLeavesQuotaAndEligibilityIntact(t *testing.T) {
	f := setup(t)
	in := input("code")
	c := f.create(t, in, "rollback")
	if _, e := f.db.Exec("INSERT INTO gacha_profiles(user_id,limited,month,entered_at,updated_at) VALUES(?,?,'2027-01',1,1)", f.user, maxCount); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.Redeem(t.Context(), f.user, f.nextKey(), c.Code); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
	if f.balance(t, f.user, ledger.General) != "0" {
		t.Fatal("partial bundle issued")
	}
	var claimed int
	if e := f.db.QueryRow("SELECT claimed FROM reward_campaigns WHERE id=?", c.ID).Scan(&claimed); e != nil || claimed != 0 {
		t.Fatal(claimed, e)
	}
	if _, e := f.db.Exec("UPDATE gacha_profiles SET limited=0 WHERE user_id=?", f.user); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.Redeem(t.Context(), f.user, f.nextKey(), c.Code); e != nil {
		t.Fatal(e)
	}
}
func TestDonationConditionAndCardDuplicateDoNotGrantDonation(t *testing.T) {
	f := setup(t)
	in := input("code")
	in.Rewards = []rewardasset.Item{{Asset: "gacha:card:a-chatgpt", Quantity: "2"}}
	// Select a real catalog identity instead of coupling this regression to a name.
	for _, a := range gacha.RewardAssets() {
		if a.Group == "cards" {
			in.Rewards[0].Asset = a.ID
			break
		}
	}
	in.Conditions = Condition{Op: "or", Children: []Condition{{Op: "compare", Field: "donation", Compare: "gte", Value: "0.001"}, {Op: "compare", Field: "asset", Asset: "general", Compare: "eq", Value: "0"}}}
	c := f.create(t, in, "cards")
	r, e := f.s.Redeem(t.Context(), f.user, f.nextKey(), c.Code)
	if e != nil || r.Results[0].Granted != "1" || r.Results[0].Converted == nil || r.Results[0].Converted.Quantity != "1" {
		t.Fatal(r, e)
	}
	var donated string
	if e = f.db.QueryRow("SELECT hex(donation_credit_mag) FROM users WHERE id=?", f.user).Scan(&donated); e != nil || donated != "00000000000000000000000000000000" {
		t.Fatal(donated, e)
	}
	a := authority{f.admin}
	g, e := gacha.New(gacha.Config{Database: f.db, Users: a, Admins: a, Gate: a, Keys: f.vault, Now: func() time.Time { return time.Unix(f.at.Load(), 0) }})
	if e != nil {
		t.Fatal(e)
	}
	entry, e := g.Entry(t.Context(), f.user)
	if e != nil || entry.Entered {
		t.Fatal("reward allocation counted as a visit", entry, e)
	}
	first, e := g.Act(t.Context(), f.user, "first-entry-reward-test", gacha.Action{Action: "enter"})
	if e != nil || first.Snapshot.Tickets["permanent"] != 10 {
		t.Fatal(first, e)
	}
	for _, action := range []string{"check-in", "limited-check-in"} {
		if _, e = g.Act(t.Context(), f.user, "first-"+action+"-reward-test", gacha.Action{Action: action}); e != nil {
			t.Fatal(e)
		}
		if _, e = g.Act(t.Context(), f.user, "second-"+action+"-reward-test", gacha.Action{Action: action}); !errors.Is(e, gacha.ErrClaimed) {
			t.Fatal(e)
		}
	}
	state, e := g.State(t.Context(), f.user)
	if e != nil || state.Tickets["limited"] != 1 || state.Tickets["permanent"] != 11 {
		t.Fatal(state, e)
	}
}
func TestDeleteBeforeDeliveryAndPrivateExport(t *testing.T) {
	f := setup(t)
	c := f.create(t, input("broadcast"), "delete")
	tx, e := f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.DeleteUserTx(t.Context(), tx, f.other); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec("INSERT INTO user_deletion_markers(user_id) VALUES(?)", f.other); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.ProcessBatch(t.Context(), 10); e != nil {
		t.Fatal(e)
	}
	tx, e = f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	rows, e := f.s.ExportUserTx(t.Context(), tx, f.user, 1)
	if e != nil || len(rows) != 1 || rows[0].CampaignID != c.ID {
		t.Fatal(rows, e)
	}
	var skipped int
	if e = tx.QueryRow("SELECT skipped FROM reward_campaigns WHERE id=?", c.ID).Scan(&skipped); e != nil || skipped != 1 {
		t.Fatal(skipped, e)
	}
}
func TestConditionTreesRejectAmbiguityAndRespectNegation(t *testing.T) {
	for i, c := range []Condition{{Op: "not"}, {Op: "compare", Field: "asset", Asset: "donation", Compare: "gte", Value: "1"}, {Op: "compare", Field: "level", Compare: "gte", Value: "1e2"}, {Op: "all", Value: "true"}} {
		if e := validateCondition(c); e == nil {
			t.Fatal("invalid condition accepted", strconv.Itoa(i))
		}
	}
	c := Condition{Op: "not", Children: []Condition{{Op: "or", Children: []Condition{{Op: "compare", Field: "level", Compare: "lt", Value: "2"}, {Op: "compare", Field: "banned", Compare: "eq", Value: "true"}}}}}
	if e := validateCondition(c); e != nil || !matches(c, facts{level: 3}) || matches(c, facts{level: 1}) {
		t.Fatal(e)
	}
}
