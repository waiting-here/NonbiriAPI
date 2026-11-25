package companion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/program"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
)

type testPrograms struct {
	programHost
	run func(context.Context, program.Binding, Raw, Raw) (Raw, error)
}

func (p testPrograms) ResolveTx(_ context.Context, _ *sql.Tx, owner int64, g, n string, d int) (program.Binding, error) {
	var pid int64
	if n != "official" {
		pid = 17
	}
	return program.Binding{ProgramID: pid, Game: g, Name: n, Difficulty: d, OwnerID: owner, Official: n == "official", Version: 7}, nil
}
func (p testPrograms) Decide(ctx context.Context, b program.Binding, q, c Raw) (Raw, error) {
	return p.run(ctx, b, q, c)
}
func fixture(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "game.sqlite")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	sqlFile, err := os.ReadFile("../../db/companion_matches.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE users(id INTEGER PRIMARY KEY,is_admin INTEGER DEFAULT 0,discord_id TEXT DEFAULT 'player',is_banned INTEGER DEFAULT 0,banned_until INTEGER);INSERT INTO users(id) VALUES(1),(2),(3),(4);CREATE TABLE game_duel_user_slots(user_id INTEGER,game_key TEXT);CREATE TABLE maintenance_state(id INTEGER,enabled INTEGER);INSERT INTO maintenance_state VALUES(1,0);CREATE TABLE site_config(key TEXT PRIMARY KEY,value TEXT);INSERT INTO site_config VALUES('games_enabled','1'),('game_bidding_enabled','1'),('game_likes_enabled','1'),('game_gwent_enabled','1');" + string(sqlFile))
	if err != nil {
		t.Fatal(err)
	}
	limiter, _ := game.NewStartLimiter(game.StartLimiterConfig{Now: time.Now})
	s := New(host.Services{Limiter: limiter, Database: db, ReadDatabase: db, Now: time.Now})
	s.programs = testPrograms{}
	return s
}
func createGame(t *testing.T, s *Service, user int64, g, p, key string) match {
	t.Helper()
	tx, err := s.shared.Database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	m, err := s.create(t.Context(), tx, user, g, key, start{Mode: "standard", Program: p, Difficulty: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if m.startReservation != nil {
		m.startReservation.Commit()
	}
	return m
}
func loadGame(t *testing.T, s *Service, m match) match {
	t.Helper()
	tx, err := s.shared.Database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	v, err := readMatch(t.Context(), tx, m.ID, m.Game, m.User)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestDurableSimultaneousAndLateDecision(t *testing.T) {
	s := fixture(t)
	ctx := t.Context()
	key := "fixture-create-000000000001"
	m := createGame(t, s, 1, "gwent", "official", key)
	if second := createGame(t, s, 1, "gwent", "official", key); second.ID != m.ID {
		t.Fatal("retried creation opened a second match")
	}
	tx, _ := s.shared.Database.Begin()
	_, err := s.create(ctx, tx, 1, "gwent", key+"x", start{Mode: "standard", Program: "official", Difficulty: 1})
	tx.Rollback()
	if !errors.Is(err, errConflict) {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	s.programs = testPrograms{run: func(ctx context.Context, b program.Binding, wire, c Raw) (Raw, error) {
		var q struct {
			ID      string            `json:"decision_id"`
			Space   space             `json:"space"`
			View    Raw               `json:"view"`
			Phase   string            `json:"phase"`
			History map[string]source `json:"history"`
		}
		if err := json.Unmarshal(wire, &q); err != nil {
			return nil, err
		}
		if string(q.View) != "{}" || q.Phase != "setup" || q.History["global"].Snapshot == "" || q.History["personal"].Snapshot != "" || b.Version != 7 {
			return nil, errors.New("incorrect program projection")
		}
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return encode(reply{Protocol: protocol, ID: q.ID, Action: q.Space.Examples[0]}), nil
	}}
	go func() { finished <- s.runOne(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("program did not start")
	}
	m = loadGame(t, s, m)
	if m.AIState != "thinking" {
		t.Fatal(m.AIState)
	}
	tx, _ = s.shared.Database.Begin()
	defer tx.Rollback()
	rules, _ := rulesFor(m.Game, m.Mode)
	window, _ := rules.observe(m.Frame, &m.Record, m.Seat, false)
	if err = m.apply(rules, m.Seat, example(t, window)); err != nil {
		t.Fatal(err)
	}
	if err = m.update(ctx, tx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	m = loadGame(t, s, m)
	if m.Status != "active" || len(m.Record.Actions) != 1 || m.Frame.Gwent == nil {
		t.Fatal("simultaneous setup failed", m.Status, m.Reason)
	}
	count, err := s.drain(ctx, "gwent", time.Now().Unix(), 10, true)
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err = s.finish(ctx, m, reply{}, "timeout", time.Second); err != nil {
		t.Fatal(err)
	}
	ended := loadGame(t, s, m)
	if ended.Status != "cancelled" || ended.Reason != "server_restart" {
		t.Fatal(ended.result)
	}
	count, err = s.drain(ctx, "gwent", time.Now().Unix(), 10, true)
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestSnapshotsUseConsentOwnershipAndPerOpponentIdentity(t *testing.T) {
	s := fixture(t)
	ctx := t.Context()
	db := s.shared.Database
	for _, x := range []struct {
		user         int64
		name, status string
		consent      bool
	}{{1, "official", "completed", true}, {2, "official", "completed", true}, {3, "official", "completed", false}, {1, "my-program", "completed", true}, {1, "official", "technical", true}} {
		m := createGame(t, s, x.user, "bidding", x.name, token(20))
		_, err := db.Exec("UPDATE companion_matches SET status=?,ended_at=?,contribute=?,ai_state='idle' WHERE id=?", x.status, time.Now().Unix(), x.consent, m.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := db.Exec("INSERT INTO companion_preferences VALUES(1,'bidding','opponent',1)")
	if err != nil {
		t.Fatal(err)
	}
	m := createGame(t, s, 1, "bidding", "my-program", token(20))
	src, err := s.historySources(ctx, &m)
	if err != nil {
		t.Fatal(err)
	}
	if len(src) != 1 || src["personal"].Count != 1 {
		t.Fatal(src)
	}
	call := s.historyReader(&m, src)
	if _, err = call(ctx, "aggregate", encode(Obj{"snapshot": src["personal"].Snapshot})); err == nil {
		t.Fatal("private program read aggregate")
	}
	list, err := call(ctx, "list", encode(Obj{"snapshot": src["personal"].Snapshot}))
	if err != nil {
		t.Fatal(err)
	}
	var page struct{ Items []struct{ Record string } }
	json.Unmarshal(list, &page)
	if len(page.Items) != 1 || page.Items[0].Record != "1" {
		t.Fatal(string(list))
	}
	_, err = db.Exec("DELETE FROM companion_matches WHERE id=?", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	m = createGame(t, s, 1, "bidding", "official", token(20))
	src, err = s.historySources(ctx, &m)
	if err != nil {
		t.Fatal(err)
	}
	if src["global"].Count != 2 || src["aggregate"].Count != 2 || src["personal"].Count != 1 {
		t.Fatal(src)
	}
	var uniques int
	if err = db.QueryRow("SELECT COUNT(*) FROM companion_history_snapshot WHERE match_id=?", m.ID).Scan(&uniques); err != nil || uniques != 2 {
		t.Fatal(uniques, err)
	}
	_, err = db.Exec("DELETE FROM users WHERE id=2")
	if err != nil {
		t.Fatal(err)
	}
	src, err = s.historySources(ctx, &m)
	if err != nil || src["global"].Count != 1 {
		t.Fatal(src, err)
	}
}
