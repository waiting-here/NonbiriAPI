package blackjack_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestHistoryOwnershipPaginationExportAndAnonymousRetention(t *testing.T) {
	f := newFixture(t, 3)
	f.exec(`INSERT INTO users(username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) SELECT 'admin',1,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at FROM users WHERE id=?`, f.users[2].UserID)
	ids := []string{}
	for round := range 3 {
		start := int64(120 + 60*round)
		f.clock.Store(start)
		f.join(0)
		id := f.read(0).Table.ID
		proof, err := randomness.New("blackjack", id, "six-decks-s17-v1", nil)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := randomness.Insert(f.ctx, tx, proof); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		f.clock.Store(start + 5)
		home := f.read(0)
		ids = append(ids, home.Table.ID)
		f.clock.Store(start + 25)
		f.read(0)
	}
	page, err := f.s.History(f.ctx, f.users[0], blackjack.PageInput{Limit: 2})
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != ids[2] || page.NextCursor == nil {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	tail, err := f.s.History(f.ctx, f.users[0], blackjack.PageInput{Limit: 2, Cursor: *page.NextCursor})
	if err != nil || len(tail.Items) != 1 || tail.Items[0].ID != ids[0] || tail.NextCursor != nil {
		t.Fatalf("tail=%+v err=%v", tail, err)
	}
	if _, err := f.s.History(f.ctx, f.users[1], blackjack.PageInput{Cursor: *page.NextCursor}); !errors.Is(err, blackjack.ErrInvalid) {
		t.Fatalf("cross-user cursor=%v", err)
	}
	if _, err := f.s.HistoryDetail(f.ctx, f.users[1], ids[0]); !errors.Is(err, blackjack.ErrNotFound) {
		t.Fatalf("cross-user detail=%v", err)
	}
	detail, err := f.s.HistoryDetail(f.ctx, f.users[0], ids[0])
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(detail)
	for _, forbidden := range []string{"deck", "cursor", "state_json", "user_id", "operation_id", "pending_json"} {
		if strings.Contains(string(body), `"`+forbidden+`"`) {
			t.Fatalf("private field %s: %s", forbidden, body)
		}
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	exported, _, err := f.s.Module().ExportTx(f.ctx, tx, f.users[0].UserID, f.clock.Load(), 10)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.(blackjack.PersonalExport).History) != 3 {
		t.Fatal("export omitted games")
	}
	f.clock.Store(285 + 30*24*60*60 + 1)
	if _, err := f.s.HistoryDetail(f.ctx, f.users[0], ids[0]); !errors.Is(err, blackjack.ErrNotFound) {
		t.Fatalf("expired detail=%v", err)
	}
	retained, err := f.s.Retain(f.ctx, f.clock.Load(), 10, time.Now().Add(time.Minute))
	if err != nil || retained.Processed != 3 {
		t.Fatalf("retain=%+v err=%v", retained, err)
	}
	archive, err := f.s.AdminHistory(f.ctx, blackjack.PageInput{Dataset: "anonymous", Limit: 2})
	if err != nil || len(archive.Items) != 2 || archive.NextCursor == nil {
		t.Fatalf("archive=%+v err=%v", archive, err)
	}
	dump, err := f.s.AdminExport(f.ctx, blackjack.PageInput{Dataset: "anonymous"})
	if err != nil || len(dump.Items) != 3 {
		t.Fatalf("dump=%+v err=%v", dump, err)
	}
	body, _ = json.Marshal(dump)
	for _, id := range ids {
		if strings.Contains(string(body), id) {
			t.Fatal("original game ID in anonymous export")
		}
	}
	for _, forbidden := range []string{"started_at", "terminal_at", "emote_at", "user_id", "payment", "operations", "deck", "seed", "commitment", "streams", "randomness"} {
		if strings.Contains(string(body), `"`+forbidden+`"`) {
			t.Fatalf("anonymous field %s", forbidden)
		}
	}
	for _, table := range []string{"game_blackjack_entries", "game_blackjack_payments", "game_blackjack_events", "game_blackjack_sessions", "game_random_proofs"} {
		var n int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("retained %s=%d err=%v", table, n, err)
		}
	}
	tx, err = f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := ledger.ValidateRecovery(f.ctx, tx); err != nil {
		t.Fatal(err)
	}
}

func TestClosingGameDrainsQueueAndLetsDealtTableSettle(t *testing.T) {
	f := newFixture(t, 10)
	for i := range 10 {
		f.join(i)
	}
	f.clock.Store(125)
	home := f.read(0)
	f.exec(`UPDATE site_config SET value='0' WHERE key='game_blackjack_enabled'`)
	f.clock.Store(126)
	home = f.read(0)
	if home.Table.Phase != "decision" || home.QueueCount != "0" {
		t.Fatalf("closed prematurely: %+v", home)
	}
	if f.read(9).You != nil {
		t.Fatal("waiting entry retained while closed")
	}
	f.clock.Store(145)
	home = f.read(0)
	if home.Table.Phase != "result" {
		t.Fatal("closed game did not settle")
	}
	var reserved int
	f.db.QueryRow(`SELECT COUNT(*) FROM game_blackjack_payments WHERE state='reserved'`).Scan(&reserved)
	if reserved != 0 {
		t.Fatalf("holds=%d", reserved)
	}
}

func TestEmptySeatingTableIsNotPersisted(t *testing.T) {
	f := newFixture(t, 1)
	q := f.join(0)
	if _, err := f.s.Leave(f.ctx, f.users[0], f.id("op_"), q.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM game_blackjack_sessions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("empty games=%d err=%v", n, err)
	}
}
