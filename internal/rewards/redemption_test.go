package rewards

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (f *fixture) set(t *testing.T, key string, value int64) {
	t.Helper()
	r, err := f.db.Exec("UPDATE site_config SET value=? WHERE key=?", strconv.FormatInt(value, 10), key)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("missing configuration %s: %d %v", key, n, err)
	}
}
func (f *fixture) scalar(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := f.db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
func (f *fixture) violations(t *testing.T, user int64) int64 {
	return f.scalar(t, "SELECT count(*) FROM abuse_window_events WHERE user_id=? AND violation_kind='redemption_code'", user)
}
func (f *fixture) restart(t *testing.T) {
	t.Helper()
	if err := f.abuse.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.abuse, err = antiabuse.NewService(f.abuseConfig)
	if err != nil {
		t.Fatal(err)
	}
	f.s, err = New(f.s.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.AttachAbuse(f.abuse); err != nil {
		t.Fatal(err)
	}
}

func TestRedemptionFifthViolationBansOnceAndRevokesAuthority(t *testing.T) {
	f := setup(t)
	if _, err := f.db.Exec("INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at) VALUES('synthetic-session',?,?,?,?,?)", f.user, f.at.Load(), f.at.Load()+3600, f.at.Load()+7200, f.at.Load()); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"MISSING-ONE", "?", "MISSING-TWO", "x"} {
		if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), code); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if f.violations(t, f.user) != 4 || f.retirements.Load() != 0 || f.cancellations.Load() != 0 {
		t.Fatal("ordinary submissions retired account activity")
	}
	c := f.create(t, input("code"), "success")
	key := f.nextKey()
	first, err := f.s.Redeem(t.Context(), f.user, key, c.Code)
	if err != nil || first.AlreadyClaimed {
		t.Fatal(first, err)
	}
	if _, err := f.s.History(t.Context(), f.user, ""); err != nil {
		t.Fatal(err)
	}
	replay, err := f.s.Redeem(t.Context(), f.user, key, strings.ToLower(c.Code))
	if err != nil || replay.AlreadyClaimed || replay.ID != first.ID || f.violations(t, f.user) != 4 {
		t.Fatalf("success or replay changed abuse window: %+v %v", replay, err)
	}
	result, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), c.Code)
	if err != nil || result.BannedUntil == nil || *result.BannedUntil != f.at.Load()+86400 {
		t.Fatalf("fifth violation: %+v %v", result, err)
	}
	if f.bans.Load() != 1 || f.retirements.Load() != 1 || f.cancellations.Load() != 1 || f.finalized.Load() != 1 {
		t.Fatal("ban effects were not applied exactly once")
	}
	if f.scalar(t, "SELECT generation FROM caller_keys WHERE user_id=?", f.user) != 2 || f.scalar(t, "SELECT is_banned FROM users WHERE id=?", f.user) != 1 {
		t.Fatal("account authority survived ban")
	}
	if f.scalar(t, "SELECT count(*) FROM sessions WHERE user_id=?", f.user) != 0 {
		t.Fatal("browser session survived ban")
	}
	if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), "MISSING-THREE"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("banned caller admitted: %v", err)
	}
	if f.violations(t, f.user) != 5 || f.scalar(t, "SELECT count(*) FROM abuse_cases WHERE reason_code='redemption_code'") != 1 || f.scalar(t, "SELECT count(*) FROM abuse_evidence WHERE violation_kind='redemption_code' AND content_chars IS NULL") != 5 {
		t.Fatal("wrong evidence or duplicate ban")
	}
	var rules, statistics string
	if err := f.db.QueryRow("SELECT rules_json,statistics_json FROM abuse_actions WHERE reason_code='redemption_code'").Scan(&rules, &statistics); err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(rules), &snapshot); err != nil || snapshot["version"] != float64(2) || snapshot["redemption_ban_threshold"] != float64(5) {
		t.Fatal(rules, err)
	}
	if !strings.Contains(statistics, `"rule":"redemption_window"`) || strings.Contains(rules+statistics, c.Code) || strings.Contains(rules+statistics, "MISSING") {
		t.Fatal("incorrect or private evidence", statistics)
	}
	if f.scalar(t, "SELECT count(*) FROM request_logs") != 0 || f.balance(t, f.user, ledger.General) != "10125" || f.violations(t, f.other) != 0 {
		t.Fatal("unrelated account, forward log or balance changed")
	}
}

func TestRedemptionBusinessRefusalGraceIsDurablePerAccountAndCode(t *testing.T) {
	for _, refusal := range []string{"disabled", "expired", "not_started", "quota", "conditions"} {
		t.Run(refusal, func(t *testing.T) {
			f := setup(t)
			in := input("code")
			want := ErrUnavailable
			switch refusal {
			case "disabled":
				in.Enabled = false
			case "expired":
				end := f.at.Load()
				in.EndsAt = &end
			case "not_started":
				start := f.at.Load() + 100
				in.StartsAt = &start
			case "quota":
				quota := int64(1)
				in.Quota = &quota
			case "conditions":
				in.Conditions = Condition{Op: "compare", Field: "level", Compare: "eq", Value: "4"}
				want = ErrConditions
			}
			c := f.create(t, in, "grace")
			if refusal == "quota" {
				if _, err := f.s.Redeem(t.Context(), f.other, f.nextKey(), c.Code); err != nil {
					t.Fatal(err)
				}
			}
			key := f.nextKey()
			if _, err := f.s.Redeem(t.Context(), f.user, key, c.Code); !errors.Is(err, want) {
				t.Fatal(err)
			}
			if f.violations(t, f.user) != 0 || f.scalar(t, "SELECT count(*) FROM identity_continuity_facts WHERE scope='redemption_grace' AND window_key=?", c.ID) != 1 {
				t.Fatal("first refusal did not get one durable exemption")
			}
			f.restart(t)
			if _, err := f.s.Redeem(t.Context(), f.user, key, c.Code); !errors.Is(err, want) {
				t.Fatal(err)
			}
			if f.violations(t, f.user) != 0 {
				t.Fatal("network retry counted")
			}
			if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), c.Code); !errors.Is(err, want) {
				t.Fatal(err)
			}
			if f.violations(t, f.user) != 1 || f.retirements.Load() != 0 {
				t.Fatal("new repeated refusal did not count once")
			}
			if f.balance(t, f.user, ledger.General) != "0" || f.scalar(t, "SELECT count(*) FROM reward_claims WHERE user_id=?", f.user) != 0 {
				t.Fatal("refused reward was partly delivered")
			}
			// A separate code and account each receive their own first-refusal exemption.
			otherInput := input("code")
			otherInput.Enabled = false
			other := f.create(t, otherInput, "other-code")
			for _, user := range []int64{f.user, f.other} {
				if _, err := f.s.Redeem(t.Context(), user, f.nextKey(), other.Code); !errors.Is(err, ErrUnavailable) {
					t.Fatal(err)
				}
			}
			if f.violations(t, f.user) != 1 || f.violations(t, f.other) != 0 {
				t.Fatal("grace leaked between accounts or codes")
			}
		})
	}
}

func TestRedemptionViolationAndGraceWindowBoundaries(t *testing.T) {
	for _, grace := range []bool{false, true} {
		for _, delta := range []int64{-1, 0, 1} {
			t.Run(strconv.FormatBool(grace)+"/"+strconv.FormatInt(delta, 10), func(t *testing.T) {
				f := setup(t)
				f.set(t, antiabuse.KeyRedemptionBanThreshold, 2)
				f.set(t, antiabuse.KeyRedemptionBanDurationSeconds, 7)
				code := "MISSING-CODE"
				if grace {
					in := input("code")
					in.Enabled = false
					code = f.create(t, in, "boundary").Code
				}
				if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), code); !errors.Is(err, ErrUnavailable) {
					t.Fatal(err)
				}
				f.at.Add(86400 + delta)
				f.restart(t)
				out, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), code)
				if !grace && delta < 0 {
					if err != nil || out.BannedUntil == nil || *out.BannedUntil != f.at.Load()+7 {
						t.Fatal(out, err)
					}
				} else if !errors.Is(err, ErrUnavailable) {
					t.Fatal(err)
				}
				want := int64(1)
				if grace && delta >= 0 {
					want = 0
				} else if !grace && delta < 0 {
					want = 2
				}
				if got := f.violations(t, f.user); got != want {
					t.Fatalf("events %d, want %d", got, want)
				}
			})
		}
	}
}

func TestRedemptionReplayCoversInitialWindowWithoutExtendingIt(t *testing.T) {
	f := setup(t)
	f.set(t, antiabuse.KeyRedemptionBanWindowSeconds, 172800)
	start := f.at.Load()
	key := f.nextKey()
	if _, err := f.s.Redeem(t.Context(), f.user, key, "UNKNOWN-CODE"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	f.at.Add(86401)
	f.restart(t)
	if _, err := f.s.Redeem(t.Context(), f.user, key, "UNKNOWN-CODE"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if f.violations(t, f.user) != 1 || f.scalar(t, "SELECT expires_at FROM idempotency_records WHERE scope='reward_redemption'") != start+172800 {
		t.Fatal("24-hour retry counted or extended lifetime")
	}
	if _, err := f.s.Redeem(t.Context(), f.user, key, "DIFFERENT-CODE"); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatal("same key with different code accepted", err)
	}
	f.set(t, antiabuse.KeyRedemptionBanWindowSeconds, 345600)
	f.at.Store(start + 172800)
	if _, err := f.s.Redeem(t.Context(), f.user, key, "UNKNOWN-CODE"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if f.violations(t, f.user) != 1 || f.scalar(t, "SELECT occurred_at FROM abuse_window_events WHERE user_id=?", f.user) != f.at.Load() {
		t.Fatal("expired fact reappeared after window expansion")
	}
}

func TestRedemptionDisabledRuleKeepsReplayAndMinuteLimit(t *testing.T) {
	f := setup(t)
	f.set(t, antiabuse.KeyRedemptionBanThreshold, 0)
	first := f.nextKey()
	for i := 0; i < 12; i++ {
		key := first
		if i > 0 {
			key = f.nextKey()
		}
		if _, err := f.s.Redeem(t.Context(), f.user, key, "UNKNOWN-CODE"); !errors.Is(err, ErrUnavailable) {
			t.Fatal(i, err)
		}
	}
	if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), "UNKNOWN-CODE"); !errors.Is(err, ErrRate) {
		t.Fatal(err)
	}
	f.set(t, antiabuse.KeyRedemptionBanThreshold, 5)
	if _, err := f.s.Redeem(t.Context(), f.user, first, "UNKNOWN-CODE"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if f.violations(t, f.user) != 0 || f.retirements.Load() != 0 || f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 12 {
		t.Fatal("disabled or rate-limited attempt changed abuse state")
	}
	f.at.Add(60)
	if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), "UNKNOWN-CODE"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if f.violations(t, f.user) != 1 {
		t.Fatal("enabled new submission not counted")
	}
}

func TestRedemptionConcurrentRetriesAndThresholdApplyOnce(t *testing.T) {
	f := setup(t)
	key := f.nextKey()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := f.s.Redeem(t.Context(), f.user, key, "UNKNOWN-CODE"); !errors.Is(err, ErrUnavailable) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if f.violations(t, f.user) != 1 || f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 1 {
		t.Fatal("concurrent retries counted twice")
	}
	for range 8 {
		wg.Go(func() {
			_, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), "UNKNOWN-CODE")
			if err != nil && !errors.Is(err, ErrUnavailable) && !errors.Is(err, authz.ErrForbidden) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if f.violations(t, f.user) != 5 || f.bans.Load() != 1 || f.finalized.Load() != 1 || f.scalar(t, "SELECT count(*) FROM abuse_actions WHERE action='trigger'") != 1 {
		t.Fatal("concurrent threshold did not commit once")
	}
}

func TestRedemptionTrialRechecksConfigAndPublishesNoEffects(t *testing.T) {
	f := setup(t)
	f.set(t, antiabuse.KeyRedemptionBanThreshold, 1)
	begin := f.abuseConfig.BeginUserRetirement
	f.abuseConfig.BeginUserRetirement = func(ctx context.Context, user int64) (antiabuse.Retirement, error) {
		if f.violations(t, user) != 0 || f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 0 || f.cancellations.Load() != 0 {
			t.Error("trial published state")
		}
		f.set(t, antiabuse.KeyRedemptionBanThreshold, 0)
		return begin(ctx, user)
	}
	f.restart(t)
	if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), "UNKNOWN-CODE"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if f.violations(t, f.user) != 0 || f.bans.Load() != 0 || f.cancellations.Load() != 0 || f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 1 {
		t.Fatal("stale trial policy was applied")
	}
}

func TestRedemptionBanFailureRollsBackSubmissionAndWindow(t *testing.T) {
	f := setup(t)
	f.set(t, antiabuse.KeyRedemptionBanThreshold, 1)
	if _, err := f.db.Exec("CREATE TRIGGER reject_revoke BEFORE UPDATE ON caller_keys BEGIN SELECT RAISE(ABORT,'injected'); END"); err != nil {
		t.Fatal(err)
	}
	key := f.nextKey()
	if _, err := f.s.Redeem(t.Context(), f.user, key, "UNKNOWN-CODE"); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if f.violations(t, f.user) != 0 || f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 0 || f.scalar(t, "SELECT is_banned FROM users WHERE id=?", f.user) != 0 || f.bans.Load() != 0 || f.finalized.Load() != 0 {
		t.Fatal("partial effects survived failed ban")
	}
	if _, err := f.db.Exec("DROP TRIGGER reject_revoke"); err != nil {
		t.Fatal(err)
	}
	out, err := f.s.Redeem(t.Context(), f.user, key, "UNKNOWN-CODE")
	if err != nil || out.BannedUntil == nil || f.violations(t, f.user) != 1 || f.bans.Load() != 1 || f.finalized.Load() != 1 {
		t.Fatal(out, err)
	}
}

func TestRedemptionRefusedAfterDeletionRecheck(t *testing.T) {
	f := setup(t)
	f.set(t, antiabuse.KeyRedemptionBanThreshold, 1)
	begin := f.abuseConfig.BeginUserRetirement
	f.abuseConfig.BeginUserRetirement = func(ctx context.Context, user int64) (antiabuse.Retirement, error) {
		if _, err := f.db.Exec("INSERT INTO user_deletion_markers(user_id) VALUES(?)", user); err != nil {
			t.Fatal(err)
		}
		return begin(ctx, user)
	}
	f.restart(t)
	if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), "UNKNOWN-CODE"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal(err)
	}
	if f.violations(t, f.user) != 0 || f.bans.Load() != 0 || f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 0 {
		t.Fatal("deleted account retained trial effects")
	}
}

func TestRedemptionGraceAndWindowSurviveSameIdentityReRegistration(t *testing.T) {
	f := setup(t)
	in := input("code")
	in.Enabled = false
	c := f.create(t, in, "continuity")
	for range 2 {
		if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), c.Code); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	var discord string
	if err := f.db.QueryRow("SELECT discord_id FROM users WHERE id=?", f.user).Scan(&discord); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := f.abuse.PreserveWindowTx(t.Context(), tx, f.user, f.at.Load()); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeleteUserTx(t.Context(), tx, f.user); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("DELETE FROM credit_accounts WHERE user_id=?", f.user); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("DELETE FROM users WHERE id=?", f.user); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.abuse.ForgetUser(f.user)
	if f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 0 {
		t.Fatal("deleted account kept submission receipts")
	}
	user := f.add(t, false)
	tx, err = f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM user_continuity_identities WHERE user_id=?", user); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE users SET discord_id=? WHERE id=?", discord, user); err != nil {
		t.Fatal(err)
	}
	if _, err := f.continuity.BindUserTx(t.Context(), tx, user); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	if _, err := f.s.Redeem(t.Context(), user, f.nextKey(), c.Code); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if f.violations(t, user) != 2 || f.scalar(t, "SELECT count(*) FROM identity_continuity_facts WHERE scope='redemption_grace'") != 1 {
		t.Fatal("new account bypassed prior exemption or violations")
	}
}

func TestRedemptionExpiredGraceDoesNotReappearWhenWindowGrows(t *testing.T) {
	f := setup(t)
	f.set(t, antiabuse.KeyRedemptionBanWindowSeconds, 10)
	in := input("code")
	in.Enabled = false
	c := f.create(t, in, "grace-expiry")
	first := f.at.Load()
	if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), c.Code); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	f.at.Add(11)
	f.set(t, antiabuse.KeyRedemptionBanWindowSeconds, 100)
	if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), c.Code); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if f.violations(t, f.user) != 0 || f.scalar(t, "SELECT occurred_at FROM identity_continuity_facts WHERE scope='redemption_grace'") != first+11 {
		t.Fatal("expired grace became a violation after window growth")
	}
	if _, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), c.Code); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if f.violations(t, f.user) != 1 || f.scalar(t, "SELECT expires_at FROM identity_continuity_facts WHERE scope='redemption_grace'") != first+111 {
		t.Fatal("repeat extended grace expiry")
	}
}

func TestRedemptionCancellationRollsBackTrial(t *testing.T) {
	f := setup(t)
	f.set(t, antiabuse.KeyRedemptionBanThreshold, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.abuseConfig.BeginUserRetirement = func(ctx context.Context, _ int64) (antiabuse.Retirement, error) { cancel(); return nil, ctx.Err() }
	f.restart(t)
	if _, err := f.s.Redeem(ctx, f.user, f.nextKey(), "UNKNOWN-CODE"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if f.violations(t, f.user) != 0 || f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 0 || f.bans.Load() != 0 || f.cancellations.Load() != 0 {
		t.Fatal("canceled submission retained effects")
	}
}

func TestRedemptionReplayRemainsBoundToAuthorizedAccount(t *testing.T) {
	f := setup(t)
	key := f.nextKey()
	for _, user := range []int64{f.user, f.other} {
		if _, err := f.s.Redeem(t.Context(), user, key, "UNKNOWN-CODE"); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if f.violations(t, f.user) != 1 || f.violations(t, f.other) != 1 || f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 2 {
		t.Fatal("different accounts shared a replay identity")
	}
	if _, err := f.s.Redeem(t.Context(), f.other, key, "DIFFERENT-CODE"); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("INSERT INTO user_deletion_markers(user_id) VALUES(?)", f.user); err != nil {
		t.Fatal(err)
	}
	for _, user := range []int64{f.user, f.admin} {
		if _, err := f.s.Redeem(t.Context(), user, key, "UNKNOWN-CODE"); !errors.Is(err, authz.ErrForbidden) {
			t.Fatal(err)
		}
	}
	if f.violations(t, f.user) != 1 || f.violations(t, f.other) != 1 || f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 2 {
		t.Fatal("authorization or conflict failure counted")
	}
}

func TestRedemptionRepeatedCapacityRefusalNeverCountsAsAbuse(t *testing.T) {
	f := setup(t)
	c := f.create(t, input("code"), "capacity")
	if _, err := f.db.Exec("INSERT INTO gacha_profiles(user_id,limited,month,entered_at,updated_at) VALUES(?,?,'2027-01',1,1)", f.user, maxCount); err != nil {
		t.Fatal(err)
	}
	firstKey := f.nextKey()
	for attempt := range 6 {
		key := firstKey
		if attempt > 0 {
			key = f.nextKey()
		}
		for range 2 {
			if _, err := f.s.Redeem(t.Context(), f.user, key, c.Code); !errors.Is(err, ErrCapacity) {
				t.Fatal(err)
			}
		}
	}
	if f.violations(t, f.user) != 0 || f.scalar(t, "SELECT count(*) FROM identity_continuity_facts WHERE scope='redemption_grace'") != 0 || f.bans.Load() != 0 || f.retirements.Load() != 0 {
		t.Fatal("capacity refusal consumed grace or counted as abuse")
	}
	if f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 6 || f.scalar(t, "SELECT claimed FROM reward_campaigns WHERE id=?", c.ID) != 0 || f.balance(t, f.user, ledger.General) != "0" {
		t.Fatal("capacity replay or bundle rollback failed")
	}
	if _, err := f.db.Exec("UPDATE gacha_profiles SET limited=0 WHERE user_id=?", f.user); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Redeem(t.Context(), f.user, firstKey, c.Code); !errors.Is(err, ErrCapacity) {
		t.Fatal("capacity retry lost its immutable refusal", err)
	}
	if f.balance(t, f.user, ledger.General) != "0" {
		t.Fatal("old refused submission was delivered")
	}
	out, err := f.s.Redeem(t.Context(), f.user, f.nextKey(), c.Code)
	if err != nil || out.AlreadyClaimed || f.balance(t, f.user, ledger.General) != "10125" || f.scalar(t, "SELECT claimed FROM reward_campaigns WHERE id=?", c.ID) != 1 {
		t.Fatal(out, err)
	}
}
