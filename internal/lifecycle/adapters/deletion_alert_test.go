package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"testing"
)

func TestDeletionAlertRetainsIdentityAndBothSignedBalances(t *testing.T) {
	for _, balances := range [][2]int64{{0, 0}, {1250, 3500}, {-2000, 1000}, {1000, -3000}} {
		t.Run(fmt.Sprint(balances), func(t *testing.T) {
			f := openAdapterFixture(t)
			tx := beginAdapterTx(t, f.store.DB())
			user := seedAdapterUser(t, tx, "deletion-alert", balances[0])
			if balances[1] != 0 {
				game, err := ledger.UserAssetAccount(context.Background(), tx, user.id, ledger.Game)
				if err != nil {
					t.Fatal(err)
				}
				external, err := ledger.CodedAssetAccount(context.Background(), tx, "external", ledger.Game)
				if err != nil {
					t.Fatal(err)
				}
				plan, err := ledger.NewAdminGameAdjustment(ledger.Meta{OperationID: mustAdapterID(t, "op_"), ActorUserID: user.id, CreatedAt: adapterTestNow}, game.ID, external.ID, ledger.AmountFromMilli(balances[1]), "fixture balance")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = ledger.Apply(context.Background(), tx, plan); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			deletion := beginAdapterTx(t, f.store.DB())
			if err := NewLedgerAdapter().ZeroAndDeleteAccount(context.Background(), deletion, lifecycle.DeleteRequest{UserID: user.id, DecisionNow: adapterTestNow + 10}, mustAdapterID(t, "op_")); err != nil {
				t.Fatal(err)
			}
			if err := deletion.Commit(); err != nil {
				t.Fatal(err)
			}
			var body string
			var resolved int
			var at, subject sql.NullInt64
			if err := f.store.DB().QueryRow(`SELECT d.snapshot_json,a.resolved,a.resolved_at,a.subject_user_id FROM admin_alerts a JOIN admin_account_deletions d ON d.alert_id=a.id WHERE a.kind='account_deleted'`).Scan(&body, &resolved, &at, &subject); err != nil {
				t.Fatal(err)
			}
			var snapshot adminalerts.AccountDeletion
			if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
				t.Fatal(err)
			}
			wantResolved := balances[0] >= 0 && balances[1] >= 0
			if snapshot.DiscordID != "adapter-deletion-alert" || snapshot.GeneralBalance != fmt.Sprint(float64(balances[0])/1000) || snapshot.GameBalance != fmt.Sprint(float64(balances[1])/1000) || (resolved == 1) != wantResolved || at.Valid != wantResolved || subject.Valid {
				t.Fatalf("snapshot=%s resolved=%d at=%v subject=%v", body, resolved, at, subject)
			}
		})
	}
}
func TestDeletionAlertFailureRollsBackIdentityAndWallet(t *testing.T) {
	f := openAdapterFixture(t)
	tx := beginAdapterTx(t, f.store.DB())
	user := seedAdapterUser(t, tx, "alert-rollback", -2000)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`CREATE TRIGGER reject_deletion_snapshot BEFORE INSERT ON admin_account_deletions BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	deletion := beginAdapterTx(t, f.store.DB())
	if err := NewLedgerAdapter().ZeroAndDeleteAccount(context.Background(), deletion, lifecycle.DeleteRequest{UserID: user.id, DecisionNow: adapterTestNow + 10}, mustAdapterID(t, "op_")); err == nil {
		t.Fatal("ignored failed alert")
	}
	if err := deletion.Rollback(); err != nil {
		t.Fatal(err)
	}
	var users, alerts int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM users WHERE id=?", user.id).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow("SELECT count(*) FROM admin_alerts WHERE kind='account_deleted'").Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if users != 1 || alerts != 0 {
		t.Fatalf("partial deletion %d %d", users, alerts)
	}
}
