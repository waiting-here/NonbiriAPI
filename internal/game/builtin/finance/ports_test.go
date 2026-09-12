package finance

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	_ "modernc.org/sqlite"
)

func TestFinancialPortsRejectForeignSourcesBeforeLedgerMutation(t *testing.T) {
	// Deliberately omit ledger tables. A failed guard that reaches a ledger
	// command produces a SQL error instead of the required invalid-plan error.
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "source-guards.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, ddl := range []string{
		`CREATE TABLE game_fishing_batches(id TEXT,user_id INTEGER,entry_total_milli INTEGER,payout_total_milli INTEGER,state TEXT,operation_id TEXT,rules_version INTEGER,game_paid_milli INTEGER)`,
		`CREATE TABLE game_linklink_sessions(id TEXT,user_id INTEGER,price_milli INTEGER,operation_id TEXT,state TEXT,rules_version INTEGER,game_paid_milli INTEGER)`,
		`CREATE TABLE game_rps_queue(id TEXT,user_id INTEGER,account_id INTEGER,reserved BLOB,reservation_operation_id TEXT,rules_version INTEGER,game_paid BLOB)`,
		`CREATE TABLE game_rps_sessions(id TEXT,account_id INTEGER,state TEXT,terminal_operation_id TEXT,player_pool BLOB,rules_version INTEGER)`,
		`CREATE TABLE game_rps_seats(session_id TEXT,user_id INTEGER,deletion_state TEXT,game_remaining BLOB)`,
		`CREATE TABLE shared_pools(account_id INTEGER,pool_type TEXT,state TEXT)`,
	} {
		if _, err := database.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	id := func(prefix string) string { return prefix + strings.Repeat("A", 22) }
	operation := id("op_")
	amount, err := db.ParseU128Decimal("1000")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO game_fishing_batches VALUES(?,1,1000,1200,'reserved',?,1,0)`, []any{id("fb_"), operation}},
		{`INSERT INTO game_linklink_sessions VALUES(?,1,1000,?,'active',1,0)`, []any{id("ll_"), operation}},
		{`INSERT INTO game_rps_queue VALUES(?,1,10,?,?,1,X'00000000000000000000000000000000')`, []any{id("rpsq_"), db.EncodeU128(amount), operation}},
		{`INSERT INTO game_rps_sessions VALUES(?,11,'started',?,?,1)`, []any{id("rps_"), operation, db.EncodeU128(amount)}},
		{`INSERT INTO game_rps_seats VALUES(?,1,'active',X'00000000000000000000000000000000')`, []any{id("rps_")}},
		{`INSERT INTO shared_pools VALUES(12,'welfare','open')`, nil},
	} {
		if _, err := database.Exec(query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	base := ports.Entry{Meta: ledger.Meta{OperationID: operation, ActorUserID: 1, CreatedAt: 2_000_000_000}, UserID: 1, Amount: ledger.AmountFromMilli(1000)}
	for _, module := range []struct{ id, prefix string }{{"fishing", "fb_"}, {"linklink", "ll_"}, {"rps", "rpsq_"}} {
		for _, scenario := range []string{"foreign owner", "foreign actor", "wrong prefix", "wrong amount", "negative amount", "wrong asset amount", "negative game amount", "excess game amount", "invalid operation"} {
			t.Run(module.id+"/"+scenario, func(t *testing.T) {
				input := base
				input.ResourceID = id(module.prefix)
				switch scenario {
				case "foreign owner":
					input.UserID, input.Meta.ActorUserID = 2, 2
				case "foreign actor":
					input.Meta.ActorUserID = 2
				case "wrong prefix":
					input.ResourceID = id("other_")
				case "wrong amount":
					input.Amount = ledger.AmountFromMilli(999)
				case "negative amount":
					input.Amount = ledger.AmountFromMilli(-1)
				case "wrong asset amount":
					input.GamePaid = ledger.AmountFromMilli(1)
				case "negative game amount":
					input.GamePaid = ledger.AmountFromMilli(-1)
				case "excess game amount":
					input.GamePaid = ledger.AmountFromMilli(1001)
				case "invalid operation":
					input.Meta.OperationID = "invalid"
				}
				tx, err := database.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				called := false
				write := func(context.Context, *sql.Tx) error { called = true; return nil }
				capability, err := ForModule(module.id)
				if err != nil {
					t.Fatal(err)
				}
				switch module.id {
				case "fishing":
					err = capability.Fishing.Settle(context.Background(), tx, ports.FishingSettlement{Entry: input, Payout: ledger.AmountFromMilli(1200)}, write)
				case "linklink":
					err = capability.LinkLink.Entry(context.Background(), tx, input)
				case "rps":
					err = capability.RPS.QueueRelease(context.Background(), tx, input, write)
				}
				if !errors.Is(err, ledger.ErrInvalidPlan) || called {
					t.Fatalf("error=%v callback=%v", err, called)
				}
			})
		}
	}
	for _, scenario := range []string{"fishing operation", "fishing payout", "linklink operation", "round participant", "terminal state"} {
		t.Run(scenario, func(t *testing.T) {
			tx, err := database.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			input := base
			write := func(context.Context, *sql.Tx) error { t.Fatal("unexpected mutation"); return nil }
			switch scenario {
			case "fishing operation", "fishing payout":
				input.ResourceID = id("fb_")
				payout := ledger.AmountFromMilli(1200)
				if scenario == "fishing operation" {
					input.Meta.OperationID = "op_" + strings.Repeat("B", 21) + "A"
				} else {
					payout = ledger.AmountFromMilli(1199)
				}
				err = (fishingPort{}).Settle(context.Background(), tx, ports.FishingSettlement{Entry: input, Payout: payout}, write)
			case "linklink operation":
				input.ResourceID, input.Meta.OperationID = id("ll_"), "op_"+strings.Repeat("B", 21)+"A"
				err = (linkLinkPort{}).Entry(context.Background(), tx, input)
			case "round participant":
				input.Meta.ActorUserID = 2
				err = (rpsPort{}).RoundCut(context.Background(), tx, ports.RoundCut{Meta: input.Meta, SessionID: id("rps_")}, write)
			case "terminal state":
				input.Meta.ActorUserID = 0
				err = (rpsPort{}).Terminal(context.Background(), tx, ports.Terminal{Meta: input.Meta, SessionID: id("rps_"), WelfareAccountID: 12, Carry: input.Amount}, write)
			}
			if !errors.Is(err, ledger.ErrInvalidPlan) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	for _, name := range []string{"", "free", "new-game", "Fishing"} {
		if _, err := ForModule(name); !errors.Is(err, ledger.ErrInvalidPlan) {
			t.Fatalf("unregistered %q: %v", name, err)
		}
	}
}
