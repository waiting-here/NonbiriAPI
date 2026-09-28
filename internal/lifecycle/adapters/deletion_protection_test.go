package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/blacklist"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestDeletionProtectionSourcesAndOriginalPenaltyMatrix(t *testing.T) {
	for _, source := range []lifecycle.DeleteSource{lifecycle.DeleteSelf, lifecycle.DeleteAdmin, lifecycle.DeleteSystem} {
		for _, state := range []struct {
			name                         string
			ban, pause, debt             bool
			banOffset, pauseExpiryOffset int64
		}{
			{name: "clean"}, {name: "ban", ban: true, banOffset: 1},
			{name: "pause", pause: true, pauseExpiryOffset: 1}, {name: "debt", debt: true},
			{name: "both", ban: true, pause: true, debt: true, banOffset: 1, pauseExpiryOffset: 1},
			{name: "expired", ban: true, pause: true},
			{name: "expired_ban_active_pause", ban: true, pause: true, banOffset: -1, pauseExpiryOffset: 1},
		} {
			t.Run(fmt.Sprintf("%s/%s", source, state.name), func(t *testing.T) {
				f := openAdapterFixture(t)
				tx := beginAdapterTx(t, f.store.DB())
				balance := int64(1500)
				if state.debt {
					balance = -2000
				}
				user := seedAdapterUser(t, tx, "protected-deletion", balance)
				now := adapterTestNow + 10
				var until, pause any
				if state.ban {
					until = now + state.banOffset
				}
				if state.pause {
					pause = now + state.pauseExpiryOffset
				}
				const originalReason = "Original penalty\nSecond line"
				if _, err := tx.Exec(`UPDATE users SET discord_id='100000000000000123',is_banned=?,banned_until=?,banned_reason=?,charity_suspended_until=?,level=5 WHERE id=?`, state.ban, until, originalReason, pause, user.id); err != nil {
					t.Fatal(err)
				}
				request := capturedDeleteRequest(t, tx, user.id, now, source)
				// A domain hook can end penalties; the deletion decision must use
				// the captured state at its original timestamp.
				if _, err := tx.Exec(`UPDATE users SET is_banned=0,banned_until=NULL,banned_reason='',charity_suspended_until=NULL WHERE id=?`, user.id); err != nil {
					t.Fatal(err)
				}
				if err := NewLedgerAdapter().ZeroAndDeleteAccount(context.Background(), tx, request, mustAdapterID(t, "op_")); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				var body, kind, action string
				var resolved, blocked int
				var resolvedAt sql.NullInt64
				if err := f.store.DB().QueryRow(`SELECT d.snapshot_json,d.blacklist_action,a.resolved,a.resolved_at,a.resolution_kind FROM admin_account_deletions d JOIN admin_alerts a ON a.id=d.alert_id WHERE d.former_user_id=?`, user.id).Scan(&body, &action, &resolved, &resolvedAt, &kind); err != nil {
					t.Fatal(err)
				}
				var snapshot adminalerts.AccountDeletion
				if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
					t.Fatal(err)
				}
				activeBan := state.ban && state.banOffset > 0
				activePause := state.pause && state.pauseExpiryOffset > 0
				activePenalty := activeBan || activePause
				wantBlock := source == lifecycle.DeleteSelf && (state.debt || activePenalty)
				if err := f.store.DB().QueryRow(`SELECT count(*) FROM discord_blacklist`).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if (blocked == 1) != wantBlock || (action == "added") != wantBlock {
					t.Fatalf("block/action=%d/%s want %v", blocked, action, wantBlock)
				}
				wantResolved := wantBlock || !state.debt
				if (resolved == 1) != wantResolved || resolvedAt.Valid != wantResolved || (kind == "automatic_blacklist") != wantBlock {
					t.Fatalf("resolution=%d/%v/%s", resolved, resolvedAt, kind)
				}
				if snapshot.Source != string(source) || snapshot.EffectiveLevel == nil || *snapshot.EffectiveLevel != 5 || snapshot.RegisteredAt == nil || snapshot.DeletedAt == nil || *snapshot.DeletedAt != now || snapshot.SnapshotVersion != 2 {
					t.Fatalf("snapshot metadata=%s", body)
				}
				if snapshot.Ban.ActiveAtDeletion == nil || *snapshot.Ban.ActiveAtDeletion != activeBan || snapshot.CharityPause.ActiveAtDeletion == nil || *snapshot.CharityPause.ActiveAtDeletion != activePause || snapshot.Ban.Reason == nil || *snapshot.Ban.Reason != originalReason {
					t.Fatalf("original penalties changed: %s", body)
				}
				if state.debt && snapshot.GeneralBalance != "-2" {
					t.Fatalf("debt snapshot=%s", body)
				}
				if wantBlock {
					var codes, note, firstNote string
					if err := f.store.DB().QueryRow(`SELECT e.reason_codes_json,e.safe_note,b.reason FROM discord_blacklist_events e JOIN discord_blacklist b USING(discord_id)`).Scan(&codes, &note, &firstNote); err != nil {
						t.Fatal(err)
					}
					var reasons []string
					if err := json.Unmarshal([]byte(codes), &reasons); err != nil {
						t.Fatal(err)
					}
					var wantCodes []string
					if activePenalty {
						wantCodes = append(wantCodes, blacklist.PenaltyEvasion)
					}
					if state.debt {
						wantCodes = append(wantCodes, blacklist.DebtEvasion)
					}
					if !slices.Equal(reasons, wantCodes) || !slices.Equal(snapshot.BlacklistReasonCodes, wantCodes) {
						t.Fatalf("reasons=%v", reasons)
					}
					wantNote := "Self-deletion while subject to an active penalty."
					if state.debt {
						wantNote = "Self-deletion with outstanding credit debt."
					}
					if activePenalty && state.debt {
						wantNote = "Self-deletion while subject to an active penalty and with outstanding credit debt."
					}
					if activeBan {
						wantNote = "试图通过删号逃避处罚\n封禁原因：" + originalReason
						if state.debt {
							wantNote += "\n试图通过删号逃避负债"
						}
					}
					if note != wantNote || firstNote != wantNote {
						t.Fatalf("event/first note=%q/%q want=%q", note, firstNote, wantNote)
					}
				}
			})
		}
	}
}

func TestAutomaticBlacklistPreservesFirstActorAndRollsBackWithDeletion(t *testing.T) {
	f := openAdapterFixture(t)
	tx := beginAdapterTx(t, f.store.DB())
	user := seedAdapterUser(t, tx, "blacklist-append", -2000)
	originalReason := "First\n" + strings.Repeat("<", 1018)
	if _, err := tx.Exec(`UPDATE users SET discord_id='100000000000000124',is_banned=1,banned_until=NULL,banned_reason=? WHERE id=?`, originalReason, user.id); err != nil {
		t.Fatal(err)
	}
	actor := int64(987)
	if _, err := blacklist.AppendTx(context.Background(), tx, blacklist.Event{DiscordID: "100000000000000124", OperationKey: "first", ActorKind: blacklist.Admin, ActorUserID: &actor, Note: "First reason", At: adapterTestNow}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var originalBalance string
	if err := f.store.DB().QueryRow(`SELECT hex(balance_mag) FROM credit_accounts WHERE id=?`, user.wallet.ID).Scan(&originalBalance); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"INSERT ON admin_account_deletions", "DELETE ON users"} {
		if _, err := f.store.DB().Exec(`CREATE TRIGGER reject_deletion BEFORE ` + stage + ` BEGIN SELECT RAISE(ABORT,'injected deletion failure'); END`); err != nil {
			t.Fatal(err)
		}
		deletion := beginAdapterTx(t, f.store.DB())
		request := capturedDeleteRequest(t, deletion, user.id, adapterTestNow+10, lifecycle.DeleteSelf)
		err := NewLedgerAdapter().ZeroAndDeleteAccount(context.Background(), deletion, request, mustAdapterID(t, "op_"))
		if err == nil || !strings.Contains(err.Error(), "injected deletion failure") {
			t.Fatalf("failure at %s: %v", stage, err)
		}
		if err := deletion.Rollback(); err != nil {
			t.Fatal(err)
		}
		var events, snapshots, alerts int
		if err := f.store.DB().QueryRow(`SELECT (SELECT count(*) FROM discord_blacklist_events),(SELECT count(*) FROM admin_account_deletions),(SELECT count(*) FROM admin_alerts)`).Scan(&events, &snapshots, &alerts); err != nil || events != 1 || snapshots != 0 || alerts != 0 {
			t.Fatalf("partial deletion at %s: events=%d snapshots=%d alerts=%d err=%v", stage, events, snapshots, alerts, err)
		}
		var reason, balance string
		var banned, sign int
		var until sql.NullInt64
		if err := f.store.DB().QueryRow(`SELECT u.banned_reason,u.is_banned,u.banned_until,a.balance_sign,hex(a.balance_mag) FROM users u JOIN credit_accounts a ON a.user_id=u.id WHERE u.id=? AND a.id=?`, user.id, user.wallet.ID).Scan(&reason, &banned, &until, &sign, &balance); err != nil {
			t.Fatal(err)
		}
		if reason != originalReason || banned != 1 || until.Valid || sign != -1 || balance != originalBalance {
			t.Fatalf("account or wallet changed after rollback at %s", stage)
		}
		if _, err := f.store.DB().Exec(`DROP TRIGGER reject_deletion`); err != nil {
			t.Fatal(err)
		}
	}
	deletion := beginAdapterTx(t, f.store.DB())
	request := capturedDeleteRequest(t, deletion, user.id, adapterTestNow+10, lifecycle.DeleteSelf)
	if err := NewLedgerAdapter().ZeroAndDeleteAccount(context.Background(), deletion, request, mustAdapterID(t, "op_")); err != nil {
		t.Fatal(err)
	}
	if err := deletion.Commit(); err != nil {
		t.Fatal(err)
	}
	var reason, kind, action string
	var firstActor, firstAt int64
	if err := f.store.DB().QueryRow(`SELECT b.reason,b.created_at,o.first_actor_kind,o.first_actor_user_id,d.blacklist_action FROM discord_blacklist b JOIN discord_blacklist_origins o USING(discord_id) JOIN admin_account_deletions d USING(discord_id)`).Scan(&reason, &firstAt, &kind, &firstActor, &action); err != nil {
		t.Fatal(err)
	}
	if reason != "First reason" || firstAt != adapterTestNow || kind != "admin" || firstActor != actor || action != "appended" {
		t.Fatalf("first event replaced: %s/%s/%d/%s", reason, kind, firstActor, action)
	}
	var note, codes, body string
	var automaticActor sql.NullInt64
	if err := f.store.DB().QueryRow(`SELECT e.safe_note,e.reason_codes_json,e.actor_kind,e.actor_user_id,d.snapshot_json FROM discord_blacklist_events e JOIN admin_account_deletions d USING(discord_id) WHERE e.operation_key!='first'`).Scan(&note, &codes, &kind, &automaticActor, &body); err != nil {
		t.Fatal(err)
	}
	wantNote := "试图通过删号逃避处罚\n封禁原因：" + originalReason + "\n试图通过删号逃避负债"
	if note != wantNote || codes != `["deletion_penalty_evasion","deletion_debt_evasion"]` || kind != "automatic" || automaticActor.Valid {
		t.Fatalf("automatic event lost original reason or penalty context: %q/%s/%s/%v", note, codes, kind, automaticActor)
	}
	var snapshot adminalerts.AccountDeletion
	if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Ban.Reason == nil || *snapshot.Ban.Reason != originalReason || snapshot.Ban.ActiveAtDeletion == nil || !*snapshot.Ban.ActiveAtDeletion || snapshot.Ban.Until != nil || snapshot.GeneralBalance != "-2" {
		t.Fatalf("original permanent ban and debt changed: %s", body)
	}
	if _, err := f.store.DB().Exec(`DELETE FROM discord_blacklist`); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT blacklist_action FROM admin_account_deletions`).Scan(&action); err != nil || action != "appended" {
		t.Fatalf("historical action changed: %s %v", action, err)
	}
}
