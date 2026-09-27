package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/blacklist"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestDeletionProtectionSourcesAndOriginalPenaltyMatrix(t *testing.T) {
	for _, source := range []lifecycle.DeleteSource{lifecycle.DeleteSelf, lifecycle.DeleteAdmin, lifecycle.DeleteSystem} {
		for _, state := range []struct {
			name             string
			ban, pause, debt bool
			expiryOffset     int64
		}{
			{"clean", false, false, false, 0}, {"ban", true, false, false, 1},
			{"pause", false, true, false, 1}, {"debt", false, false, true, 0},
			{"both", true, true, true, 1}, {"expired", true, true, false, 0},
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
					until = now + state.expiryOffset
				}
				if state.pause {
					pause = now + state.expiryOffset
				}
				if _, err := tx.Exec(`UPDATE users SET discord_id='100000000000000123',is_banned=?,banned_until=?,banned_reason='Original penalty',charity_suspended_until=?,level=5 WHERE id=?`, state.ban, until, pause, user.id); err != nil {
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
				activePenalty := (state.ban || state.pause) && state.expiryOffset > 0
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
				if snapshot.Ban.ActiveAtDeletion == nil || *snapshot.Ban.ActiveAtDeletion != (state.ban && state.expiryOffset > 0) || snapshot.CharityPause.ActiveAtDeletion == nil || *snapshot.CharityPause.ActiveAtDeletion != (state.pause && state.expiryOffset > 0) {
					t.Fatalf("original penalties changed: %s", body)
				}
				if state.debt && snapshot.GeneralBalance != "-2" {
					t.Fatalf("debt snapshot=%s", body)
				}
				if wantBlock {
					var codes string
					if err := f.store.DB().QueryRow(`SELECT reason_codes_json FROM discord_blacklist_events`).Scan(&codes); err != nil {
						t.Fatal(err)
					}
					var reasons []string
					if err := json.Unmarshal([]byte(codes), &reasons); err != nil {
						t.Fatal(err)
					}
					want := 0
					if state.debt {
						want++
					}
					if activePenalty {
						want++
					}
					if len(reasons) != want {
						t.Fatalf("reasons=%v", reasons)
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
	if _, err := tx.Exec(`UPDATE users SET discord_id='100000000000000124',is_banned=1,banned_until=NULL WHERE id=?`, user.id); err != nil {
		t.Fatal(err)
	}
	actor := int64(987)
	if _, err := blacklist.AppendTx(context.Background(), tx, blacklist.Event{DiscordID: "100000000000000124", OperationKey: "first", ActorKind: blacklist.Admin, ActorUserID: &actor, Note: "First reason", At: adapterTestNow}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`CREATE TRIGGER reject_snapshot BEFORE INSERT ON admin_account_deletions BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	deletion := beginAdapterTx(t, f.store.DB())
	request := capturedDeleteRequest(t, deletion, user.id, adapterTestNow+10, lifecycle.DeleteSelf)
	if err := NewLedgerAdapter().ZeroAndDeleteAccount(context.Background(), deletion, request, mustAdapterID(t, "op_")); err == nil {
		t.Fatal("accepted failed snapshot")
	}
	_ = deletion.Rollback()
	var events int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM discord_blacklist_events`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("partial append=%d err=%v", events, err)
	}
	if _, err := f.store.DB().Exec(`DROP TRIGGER reject_snapshot`); err != nil {
		t.Fatal(err)
	}
	deletion = beginAdapterTx(t, f.store.DB())
	request = capturedDeleteRequest(t, deletion, user.id, adapterTestNow+10, lifecycle.DeleteSelf)
	if err := NewLedgerAdapter().ZeroAndDeleteAccount(context.Background(), deletion, request, mustAdapterID(t, "op_")); err != nil {
		t.Fatal(err)
	}
	if err := deletion.Commit(); err != nil {
		t.Fatal(err)
	}
	var reason, kind, action string
	var firstActor int64
	if err := f.store.DB().QueryRow(`SELECT b.reason,o.first_actor_kind,o.first_actor_user_id,d.blacklist_action FROM discord_blacklist b JOIN discord_blacklist_origins o USING(discord_id) JOIN admin_account_deletions d USING(discord_id)`).Scan(&reason, &kind, &firstActor, &action); err != nil {
		t.Fatal(err)
	}
	if reason != "First reason" || kind != "admin" || firstActor != actor || action != "appended" {
		t.Fatalf("first event replaced: %s/%s/%d/%s", reason, kind, firstActor, action)
	}
	if _, err := f.store.DB().Exec(`DELETE FROM discord_blacklist`); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT blacklist_action FROM admin_account_deletions`).Scan(&action); err != nil || action != "appended" {
		t.Fatalf("historical action changed: %s %v", action, err)
	}
}
