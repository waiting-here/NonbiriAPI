package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
	bjconfig "github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

func readBlackjackHTTP(f *duelWireFixture, seat int) blackjack.Home {
	f.t.Helper()
	r := f.call(seat, "GET", "/api/games/blackjack/state", nil, false)
	var h blackjack.Home
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &h) != nil {
		f.t.Fatalf("blackjack state: %d %s", r.Code, r.Body.String())
	}
	return h
}
func newBlackjackHTTPFixture(t *testing.T) *duelWireFixture {
	t.Helper()
	f := newDuelWireFixture(t)
	if readBlackjackHTTP(f, 0).Config.Enabled {
		t.Fatal("new game must default closed")
	}
	f.admin("PATCH", "/admin/api/games/config", map[string]any{"expected_revision": "3", "blackjack": map[string]any{"enabled": true}}, 200)
	f.clock.Store((f.clock.Load()/30 + 1) * 30)
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, user := range f.users {
		wallet, err := ledger.UserAssetAccount(context.Background(), tx, user, ledger.General)
		if err != nil {
			t.Fatal(err)
		}
		external, err := ledger.CodedAccount(context.Background(), tx, "external")
		if err != nil {
			t.Fatal(err)
		}
		id, err := db.GenerateOpaqueID("op_")
		if err != nil {
			t.Fatal(err)
		}
		plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: id, ActorUserID: f.adminID, CreatedAt: f.clock.Load()}, wallet.ID, external.ID, ledger.AmountFromMilli(100_000_000), 0, ledger.Amount{}, "table funding")
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
	return f
}
func joinBlackjackHTTP(f *duelWireFixture, seat int) {
	f.t.Helper()
	h := readBlackjackHTTP(f, seat)
	r := f.call(seat, "POST", "/api/games/blackjack/queue", map[string]any{"stake": "5000", "config_hash": h.ConfigHash}, false)
	if r.Code != 201 {
		f.t.Fatalf("join: %d %s", r.Code, r.Body.String())
	}
}
func dealBlackjackHTTP(f *duelWireFixture) string {
	f.t.Helper()
	joinBlackjackHTTP(f, 0)
	joinBlackjackHTTP(f, 1)
	h := readBlackjackHTTP(f, 0)
	// Fix only the test's private shoe so lifecycle checks always start with
	// two unfinished hands instead of occasionally ending at the dealer peek.
	var deck [engine.DeckSize]engine.Card
	for i := range deck {
		deck[i] = engine.Card(i)
	}
	state, err := engine.Deal(deck, []int{0, 1}, int(h.Table.StartedAt/30%9))
	if err != nil {
		f.t.Fatal(err)
	}
	view, err := engine.Project(state)
	if err != nil {
		f.t.Fatal(err)
	}
	body, _ := json.Marshal(state)
	fact, _ := json.Marshal(blackjack.TableFact{Cards: &view, Seats: []blackjack.SeatTerms{
		{Seat: 0, Stake: "5000", Rake: bjconfig.Rates{Platform: 100, Welfare: 100, Thursday: 100}},
		{Seat: 1, Stake: "5000", Rake: bjconfig.Rates{Platform: 100, Welfare: 100, Thursday: 100}},
	}, Settlements: []blackjack.SeatSettlement{}})
	tx, err := f.store.DB().Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE game_blackjack_entries SET state='playing' WHERE session_id=?`, h.Table.ID); err != nil {
		f.t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE game_blackjack_sessions SET phase='decision',revision=revision+1,last_batch=?,state_json=?,view_json=? WHERE id=?`, h.Table.StartedAt+5, string(body), string(fact), h.Table.ID); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
	f.clock.Store(h.Table.StartedAt + 5)
	return h.Table.ID
}

func TestBlackjackHTTPRoutesPrivacyHistoryAndExport(t *testing.T) {
	f := newBlackjackHTTPFixture(t)
	id := dealBlackjackHTTP(f)
	h := readBlackjackHTTP(f, 0)
	if h.You == nil || h.YourSeat == nil || h.Table.Fact.Cards == nil || !h.Table.Fact.Cards.HoleHidden || len(h.Table.Fact.Cards.Dealer) != 1 {
		t.Fatal("private projection invalid")
	}
	public := wireBody(t, h.Table)
	for _, forbidden := range []string{"\"deck\"", "\"cursor\"", "\"pending_json\"", "\"user_id\"", "\"payment\""} {
		if strings.Contains(public, forbidden) {
			t.Fatal("private table field", forbidden)
		}
	}
	for _, body := range []json.RawMessage{[]byte(`{"hand":0,"hand":1,"revision":"1","action":"hit"}`), []byte(`{"Hand":0,"revision":"1","action":"hit"}`), []byte(`{"hand":0,"revision":"1","action":"hit","deck":[]}`)} {
		r := f.call(0, "POST", "/api/games/blackjack/sessions/"+id+"/actions", body, false)
		if r.Code != 400 {
			t.Fatalf("invalid action JSON: %d %s", r.Code, r.Body.String())
		}
	}
	for seat := range 2 {
		r := f.call(seat, "POST", "/api/games/blackjack/sessions/"+id+"/actions", map[string]any{"hand": 0, "revision": "1", "action": "stand"}, false)
		if r.Code != 202 {
			t.Fatalf("stand: %d %s", r.Code, r.Body.String())
		}
	}
	f.clock.Add(1)
	h = readBlackjackHTTP(f, 0)
	if h.Phase != "result" || h.NextRoundAt-h.Table.StartedAt != 30 || len(h.Table.Fact.Settlements) != 2 {
		t.Fatal("early result or fixed cadence missing")
	}
	for seat := range 2 {
		r := f.call(seat, "GET", "/api/games/blackjack/history?limit=1", nil, false)
		var page blackjack.HistoryPage
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Items) != 1 {
			t.Fatalf("history %d %s", r.Code, r.Body.String())
		}
		r = f.call(seat, "GET", "/api/games/blackjack/history/"+id, nil, false)
		if r.Code != 200 {
			t.Fatal("own detail missing", r.Code)
		}
		r = f.call(seat, "POST", "/api/account/export", nil, true)
		var exported lifecycle.ExportDocument
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &exported) != nil || exported.SchemaVersion != 10 || len(exported.Blackjack.History) != 1 {
			t.Fatalf("export %d %s", r.Code, r.Body.String())
		}
	}
	f.admin("GET", "/admin/api/games/active-counts", nil, 200)
	f.admin("GET", "/admin/api/games/blackjack/history?dataset=recent", nil, 200)
	f.admin("GET", "/admin/api/games/blackjack/history/"+id+"?dataset=recent", nil, 200)
	f.admin("POST", "/admin/api/games/blackjack/history/export", map[string]any{"dataset": "anonymous", "cursor": nil}, 200)
	f.checkLedger()
}

func TestBlackjackHTTPAccountLifecycleDoesNotCancelOtherSeats(t *testing.T) {
	for _, kind := range []string{"ban", "steward", "automatic", "delete", "maintenance"} {
		t.Run(kind, func(t *testing.T) {
			f := newBlackjackHTTPFixture(t)
			id := dealBlackjackHTTP(f)
			switch kind {
			case "ban":
				if _, err := f.store.DB().Exec(`CREATE TRIGGER reject_blackjack_ban BEFORE UPDATE OF is_banned ON users WHEN NEW.is_banned=1 BEGIN SELECT RAISE(ABORT,'rejected'); END`); err != nil {
					t.Fatal(err)
				}
				body := map[string]any{"expected_revision": f.userRevision(0), "reason": "Account restriction", "duration_seconds": 60}
				f.admin("POST", fmt.Sprintf("/admin/api/users/%d/ban", f.users[0]), body, 500)
				if readBlackjackHTTP(f, 0).Table.Fact.Seats[0].Stopped {
					t.Fatal("failed ban escaped rollback")
				}
				if _, err := f.store.DB().Exec(`DROP TRIGGER reject_blackjack_ban`); err != nil {
					t.Fatal(err)
				}
				f.admin("POST", fmt.Sprintf("/admin/api/users/%d/ban", f.users[0]), body, 204)
			case "steward":
				if _, err := f.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, f.users[1]); err != nil {
					t.Fatal(err)
				}
				r := f.call(1, "POST", fmt.Sprintf("/api/steward/users/%d/ban", f.users[0]), map[string]any{"expected_revision": f.userRevision(0), "reason": "Account restriction", "duration_seconds": 3600}, false)
				if r.Code != 204 {
					t.Fatalf("steward ban: %d %s", r.Code, r.Body.String())
				}
			case "automatic":
				if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key=?`, antiabuse.KeyRPMBanThreshold); err != nil {
					t.Fatal(err)
				}
				f.app.forward.abuse.RPMDenied(context.Background(), f.users[0], ratelimit.RPMUserLimit)
				var banned int
				if err := f.store.DB().QueryRow(`SELECT is_banned FROM users WHERE id=?`, f.users[0]).Scan(&banned); err != nil || banned != 1 {
					t.Fatal("automatic ban missing", banned, err)
				}
			case "delete":
				r := f.call(0, "POST", "/api/account/delete", map[string]string{"confirm": "DELETE"}, true)
				if r.Code != 204 {
					t.Fatalf("delete: %d %s", r.Code, r.Body.String())
				}
			case "maintenance":
				f.admin("POST", "/admin/api/maintenance/enable", map[string]any{"expected_revision": "2", "reason": "Maintenance validation", "confirmation": true}, 200)
				r := f.call(0, "POST", "/api/games/blackjack/sessions/"+id+"/actions", map[string]any{"hand": 0, "revision": "1", "action": "stand"}, false)
				if r.Code != 202 {
					t.Fatalf("maintenance continuation: %d %s", r.Code, r.Body.String())
				}
			}
			if readBlackjackHTTP(f, 1).Phase != "decision" {
				t.Fatal("other seat was cancelled")
			}
			r := f.call(1, "POST", "/api/games/blackjack/sessions/"+id+"/actions", map[string]any{"hand": 0, "revision": "1", "action": "stand"}, false)
			if r.Code != 202 {
				t.Fatalf("remaining player action: %d %s", r.Code, r.Body.String())
			}
			var accepted blackjack.ActionReceipt
			if err := json.Unmarshal(r.Body.Bytes(), &accepted); err != nil {
				t.Fatal(err)
			}
			// Account operations may advance the shared observation clock while
			// instrumented tests run. Follow the authoritative action receipt.
			f.clock.Store(max(f.clock.Load(), accepted.BatchAt))
			h := readBlackjackHTTP(f, 1)
			if h.Phase != "result" || len(h.Table.Fact.Settlements) != 2 {
				t.Fatal("table did not settle normally")
			}
			if kind == "delete" {
				var users, wallets, links int
				if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE id=?`, f.users[0]).Scan(&users); err != nil {
					t.Fatal(err)
				}
				if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE user_id=?`, f.users[0]).Scan(&wallets); err != nil {
					t.Fatal(err)
				}
				if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM game_blackjack_entries WHERE user_id=?`, f.users[0]).Scan(&links); err != nil {
					t.Fatal(err)
				}
				if users != 0 || wallets != 0 || links != 0 {
					t.Fatal("deleted identity revived")
				}
			}
			f.checkLedger()
		})
	}
}
