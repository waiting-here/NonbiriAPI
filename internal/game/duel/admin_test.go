package duel_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

type adminContextKey struct{}
type adminAuthorization struct{ denied atomic.Bool }

func (a *adminAuthorization) AuthorizeAdminMutation(ctx context.Context, _ *sql.Tx) error {
	if ctx.Value(adminContextKey{}) != "admin" || a.denied.Load() {
		return authz.ErrForbidden
	}
	return nil
}
func adminContext() context.Context {
	return context.WithValue(context.Background(), adminContextKey{}, "admin")
}

func saveAdminWireFixture(t *testing.T, page duel.AdminExportPage, name string) {
	t.Helper()
	output := os.Getenv("DUEL_TEST_OUTPUT")
	if output == "" {
		return
	}
	page.Items = page.Items[:2]
	page.NextCursor = nil
	body, err := json.MarshalIndent(page, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, name+".json"), append(body, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func adminFixture(t *testing.T, kind string, override ...duel.Rules) *fixture {
	f := newFixture(t, kind, override...)
	zero := make([]byte, 16)
	_, err := f.db.Exec(`INSERT INTO users(username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('operator',1,?,?,?,?,?,?,?,?,?,?)`, zero, zero, zero, zero, zero, zero, zero, zero, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) terminalMatch(round bool, loser int) string {
	f.t.Helper()
	state := f.matched()
	if round {
		f.action(0, state, basicPlan)
		f.action(1, state, basicPlan)
		state = *f.read(0).Current
	}
	if _, err := f.s.Surrender(f.ctx, duel.ActionInput{Identity: f.identity(loser), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq}); err != nil {
		f.t.Fatal(err)
	}
	return state.ID
}

type adminRegistrar struct{ mux *http.ServeMux }

func (a adminRegistrar) RegisterAdminRoute(method, path string, handler http.Handler) error {
	a.mux.Handle(method+" "+path, handler)
	return nil
}

func TestAdminHTTPRefusesOtherRolesMalformedInputsAndActiveMatches(t *testing.T) {
	f := adminFixture(t, "likes")
	state := f.matched()
	mux := http.NewServeMux()
	if err := f.s.RegisterAdminRoutes(adminRegistrar{mux}); err != nil {
		t.Fatal(err)
	}
	request := func(ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	base := "/admin/api/games/likes/history"
	for _, role := range []string{"user", "steward", "caller", ""} {
		ctx := context.WithValue(context.Background(), adminContextKey{}, role)
		for _, path := range []string{base, base + "/" + state.ID, base + "/" + state.ID + "/rounds"} {
			if got := request(ctx, "GET", path, ""); got.Code != 403 {
				t.Fatal(role, path, got.Code)
			}
		}
		if got := request(ctx, "POST", base+"/export", `{"dataset":"recent","selection":{},"cursor":null}`); got.Code != 403 {
			t.Fatal(role, got.Code)
		}
	}
	for _, path := range []string{base + "/" + state.ID, base + "/" + state.ID + "/rounds"} {
		if got := request(adminContext(), "GET", path, ""); got.Code != 404 {
			t.Fatal("active match disclosed", got.Code)
		}
	}
	for _, query := range []string{"?limit=101", "?limit=01", "?mode=quick&mode=quick", "?user_id=1", "?dataset=anonymous&from=1", "?from=2&to=1", "?rules_version=0", "?cursor=bad", "?outcome=win", "?dataset=bad"} {
		if got := request(adminContext(), "GET", base+query, ""); got.Code != 400 {
			t.Fatal(query, got.Code)
		}
	}
	for _, body := range []string{`{}`, `{"dataset":"recent","selection":{}}`, `{"dataset":"recent","selection":null,"cursor":null}`, `{"dataset":"recent","selection":{"mode":"quick","mode":"quick"},"cursor":null}`, `{"dataset":"recent","selection":{"from":null},"cursor":null}`, `{"dataset":"recent","selection":{"user":1},"cursor":null}`, `{"dataset":"recent","selection":{},"cursor":123}`, `{"dataset":"recent","selection":{},"cursor":""}`, `{"Dataset":"recent","selection":{},"cursor":null}`} {
		if got := request(adminContext(), "POST", base+"/export", body); got.Code != 400 {
			t.Fatal(body, got.Code, got.Body.String())
		}
	}
	if got := request(adminContext(), "GET", base, ""); got.Code != 200 || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(got.Code)
	}
}

func TestAdminRecentExportOrderHighWaterRetryAndExpiry(t *testing.T) {
	f := adminFixture(t, "likes")
	var ids []string
	for i := range 51 {
		f.clock.Store(int64(100 + i*6))
		ids = append(ids, f.terminalMatch(true, i%2))
	}
	ctx := adminContext()
	list, err := f.s.AdminHistory(ctx, duel.AdminPageInput{Dataset: "recent", Limit: 1})
	if err != nil || len(list.Items) != 1 || list.NextCursor == nil || list.Items[0].Recent == nil {
		t.Fatal(list, err)
	}
	if list.Items[0].MatchRef != ids[0] {
		t.Fatal("unstable history order")
	}
	from, to, version := int64(106), int64(112), 1
	rangePage, err := f.s.AdminHistory(ctx, duel.AdminPageInput{Dataset: "recent", Selection: duel.AdminSelection{Mode: "quick", From: &from, To: &to, RulesVersion: &version}})
	if err != nil || len(rangePage.Items) != 2 || rangePage.Items[0].MatchRef != ids[1] || rangePage.Items[1].MatchRef != ids[2] {
		t.Fatal("inclusive time and version filters", len(rangePage.Items), err)
	}
	version = 2
	rangePage, err = f.s.AdminHistory(ctx, duel.AdminPageInput{Dataset: "recent", Selection: duel.AdminSelection{RulesVersion: &version}})
	if err != nil || len(rangePage.Items) != 0 {
		t.Fatal("unknown version matched", len(rangePage.Items), err)
	}
	for _, outcome := range []string{"normal", "draw", "system_cancelled"} {
		filtered, err := f.s.AdminHistory(ctx, duel.AdminPageInput{Dataset: "recent", Selection: duel.AdminSelection{Outcome: outcome}})
		if err != nil || outcome == "normal" && len(filtered.Items) != 20 || outcome != "normal" && len(filtered.Items) != 0 {
			t.Fatal("outcome filter", outcome, len(filtered.Items), err)
		}
		for _, item := range filtered.Items {
			if item.Outcome != outcome {
				t.Fatal("storage outcome leaked", item.Outcome)
			}
		}
	}
	if _, err := f.s.AdminRounds(ctx, ids[0], duel.AdminPageInput{Dataset: "recent", Cursor: *list.NextCursor}); !errors.Is(err, duel.ErrInvalidRequest) {
		t.Fatal("cross-kind cursor", err)
	}
	page, err := f.s.AdminExport(ctx, duel.AdminExportInput{Dataset: "recent"})
	if err != nil || len(page.Items) != 100 || page.NextCursor == nil {
		t.Fatal(len(page.Items), err)
	}
	saveAdminWireFixture(t, page, "recent")
	for i, raw := range page.Items {
		var item struct {
			Kind     string `json:"kind"`
			MatchRef string `json:"match_ref"`
			RoundNo  int    `json:"round_no"`
		}
		if json.Unmarshal(raw, &item) != nil {
			t.Fatal("invalid export")
		}
		if item.MatchRef != ids[i/2] || i%2 == 0 && item.Kind != "match" || i%2 == 1 && (item.Kind != "round" || item.RoundNo != 1) {
			t.Fatal("header/round ordering", i, item)
		}
	}
	f.clock.Add(6)
	later := f.terminalMatch(true, 1)
	input := duel.AdminExportInput{Dataset: "recent", Cursor: page.NextCursor}
	second, err := f.s.AdminExport(ctx, input)
	if err != nil || len(second.Items) != 2 || second.NextCursor != nil {
		t.Fatal(len(second.Items), err)
	}
	encoded, _ := json.Marshal(second)
	if strings.Contains(string(encoded), later) {
		t.Fatal("post-snapshot result leaked into export")
	}
	retry, err := f.s.AdminExport(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(retry)
	if string(encoded) != string(again) {
		t.Fatal("read-only retry drifted")
	}
	for _, changed := range []duel.AdminExportInput{{Dataset: "anonymous", Cursor: page.NextCursor}, {Dataset: "recent", Cursor: page.NextCursor, Selection: duel.AdminSelection{Mode: "quick"}}} {
		if _, err := f.s.AdminExport(ctx, changed); !errors.Is(err, duel.ErrInvalidRequest) {
			t.Fatal("cursor binding", err)
		}
	}
	f.admin.denied.Store(true)
	if _, err := f.s.AdminExport(ctx, input); !errors.Is(err, duel.ErrForbidden) {
		t.Fatal("permission not rechecked", err)
	}
	f.admin.denied.Store(false)
	f.clock.Add(3600)
	if _, err := f.s.AdminExport(ctx, input); !errors.Is(err, duel.ErrInvalidRequest) {
		t.Fatal("expired cursor", err)
	}
	// Start a new export immediately before retention closes the oldest match.
	f.clock.Store(100 + duel.RetentionSeconds - 1)
	page, err = f.s.AdminExport(ctx, duel.AdminExportInput{Dataset: "recent"})
	if err != nil || page.NextCursor == nil {
		t.Fatal(err)
	}
	f.clock.Store(500 + duel.RetentionSeconds)
	expired, err := f.s.AdminExport(ctx, duel.AdminExportInput{Dataset: "recent", Cursor: page.NextCursor})
	if err != nil || len(expired.Items) != 0 || expired.ExpiredSkipped < 1 {
		t.Fatal(expired, err)
	}
	if _, err := f.s.AdminDetail(ctx, "recent", ids[0]); !errors.Is(err, duel.ErrNotFound) {
		t.Fatal("expired identity read", err)
	}
	f.ledger()
}

func TestAnonymousExportHighWaterPrivacyAndCompleteRoundPages(t *testing.T) {
	f := adminFixture(t, "likes")
	original := f.terminalMatch(true, 0)
	f.clock.Store(100 + duel.RetentionSeconds)
	if _, err := f.s.Retain(f.ctx, f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	var header, round []byte
	var archive, mode, hash string
	if err := f.db.QueryRow(`SELECT archive_id,mode,content_hash,header_json FROM game_duel_anonymous`).Scan(&archive, &mode, &hash, &header); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT record_json FROM game_duel_anonymous_rounds WHERE archive_id=?`, archive).Scan(&round); err != nil {
		t.Fatal(err)
	}
	clone := func(id string) {
		t.Helper()
		tx, err := f.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`INSERT INTO game_duel_anonymous(archive_id,game_key,mode,content_hash,header_json)VALUES(?,'likes',?,?,?)`, id, mode, hash, string(header)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO game_duel_anonymous_rounds(archive_id,round_no,record_json)VALUES(?,1,?)`, id, string(round)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for range 100 {
		id, err := db.GenerateOpaqueID("dah_")
		if err != nil {
			t.Fatal(err)
		}
		clone(id)
	}
	ctx := adminContext()
	filtered, err := f.s.AdminHistory(ctx, duel.AdminPageInput{Dataset: "anonymous", Selection: duel.AdminSelection{Outcome: "normal"}})
	if err != nil || len(filtered.Items) != 20 || filtered.Items[0].Outcome != "normal" {
		t.Fatal("anonymous outcome filter", len(filtered.Items), err)
	}
	page, err := f.s.AdminExport(ctx, duel.AdminExportInput{Dataset: "anonymous"})
	if err != nil || len(page.Items) != 100 || page.NextCursor == nil {
		t.Fatal(len(page.Items), err)
	}
	saveAdminWireFixture(t, page, "anonymous")
	var last struct {
		MatchRef string `json:"match_ref"`
	}
	if json.Unmarshal(page.Items[len(page.Items)-1], &last) != nil {
		t.Fatal("bad item")
	}
	var inserted string
	for {
		inserted, err = db.GenerateOpaqueID("dah_")
		if err != nil {
			t.Fatal(err)
		}
		if inserted < last.MatchRef {
			break
		}
	}
	clone(inserted)
	all := append([]json.RawMessage{}, page.Items...)
	for page.NextCursor != nil {
		page, err = f.s.AdminExport(ctx, duel.AdminExportInput{Dataset: "anonymous", Cursor: page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Items...)
	}
	if len(all) != 202 {
		t.Fatal("snapshot high-water changed", len(all))
	}
	body, _ := json.Marshal(all)
	for _, private := range []string{inserted, original, "user_id", "display_name", "general_paid", "game_paid", "operation_id", "started_at", "terminal_at", "export_seq"} {
		if strings.Contains(string(body), private) {
			t.Fatal("anonymous data linked", private)
		}
	}
	detail, err := f.s.AdminDetail(ctx, "anonymous", archive)
	if err != nil || detail.Recent != nil {
		t.Fatal(err)
	}
	rounds, err := f.s.AdminRounds(ctx, archive, duel.AdminPageInput{Dataset: "anonymous"})
	if err != nil || len(rounds.Items) != 1 || rounds.NextCursor != nil {
		t.Fatal(rounds, err)
	}
	if _, err := f.s.AdminDetail(ctx, "recent", original); !errors.Is(err, duel.ErrNotFound) {
		t.Fatal("retained original still readable", err)
	}
	f.ledger()
}

func TestAdminExportOnePageAtATimeDoesNotAcquireWriterLock(t *testing.T) {
	f := adminFixture(t, "bidding")
	f.terminalMatch(false, 0)
	if _, err := f.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	f.audit = func(a duel.AdminAudit) {
		if a.Result != "ok" || a.Role != "administrator" || a.Dataset != "recent" || a.Records != 1 {
			t.Error("unsafe audit shape", a)
		}
		close(entered)
		<-release
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.s.AdminExport(adminContext(), duel.AdminExportInput{Dataset: "recent"})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("export did not reach page boundary")
	}
	defer close(release)
	if _, err := f.s.AdminExport(adminContext(), duel.AdminExportInput{Dataset: "recent"}); !errors.Is(err, duel.ErrRateLimited) {
		t.Fatal("concurrent page accepted", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := f.db.ExecContext(ctx, `UPDATE site_config SET value=value WHERE key='games_enabled'`); err != nil {
		t.Fatal("export blocked writer", err)
	}
	// Release before waiting, without closing the channel a second time.
	release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAdminExportByteBound(t *testing.T) {
	f := adminFixture(t, "likes")
	original := f.terminalMatch(true, 0)
	f.clock.Store(100 + duel.RetentionSeconds)
	if _, err := f.s.Retain(f.ctx, f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	var archive string
	if err := f.db.QueryRow(`SELECT archive_id FROM game_duel_anonymous`).Scan(&archive); err != nil {
		t.Fatal(err)
	}
	// Exercise the transport boundary with large structured facts, independently
	// of the current rules' much smaller ordinary records.
	large, _ := json.Marshal(map[string]any{"values": []string{strings.Repeat("x", 60000), strings.Repeat("y", 60000)}})
	var values []json.RawMessage
	for range 7 {
		values = append(values, large)
	}
	before, _ := json.Marshal(values)
	for n := 2; n <= 13; n++ {
		raw, err := duel.Encode(duel.RoundView{Round: n, Before: before, After: json.RawMessage(`{}`), Facts: json.RawMessage(`{}`), StartEvents: json.RawMessage(`[]`)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(`INSERT INTO game_duel_anonymous_rounds(archive_id,round_no,record_json)VALUES(?,?,?)`, archive, n, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	input := duel.AdminExportInput{Dataset: "anonymous"}
	count := 0
	for {
		page, err := f.s.AdminExport(adminContext(), input)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(page)
		if len(body) > 8<<20 || len(page.Items) > 100 || len(page.Items) == 0 {
			t.Fatal("page boundary", len(body), len(page.Items))
		}
		for _, raw := range page.Items {
			if len(raw) > 1<<20 || strings.Contains(string(raw), original) {
				t.Fatal("unsafe record")
			}
		}
		count += len(page.Items)
		if page.NextCursor == nil {
			break
		}
		input.Cursor = page.NextCursor
	}
	if count != 14 {
		t.Fatal("byte-bound pagination lost records", count)
	}
}

func TestAdminExportStopsPartialMatchAtRetentionBoundary(t *testing.T) {
	f := adminFixture(t, "likes")
	f.terminalMatch(false, 0)
	for n := range 50 {
		f.clock.Add(6)
		f.terminalMatch(true, n%2)
	}
	f.clock.Store(100 + duel.RetentionSeconds - 1)
	page, err := f.s.AdminExport(adminContext(), duel.AdminExportInput{Dataset: "recent"})
	if err != nil || len(page.Items) != 100 || page.NextCursor == nil {
		t.Fatal(len(page.Items), err)
	}
	var last struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(page.Items[99], &last) != nil || last.Kind != "match" {
		t.Fatal("fixture did not stop inside a match")
	}
	f.clock.Store(400 + duel.RetentionSeconds)
	page, err = f.s.AdminExport(adminContext(), duel.AdminExportInput{Dataset: "recent", Cursor: page.NextCursor})
	if err != nil || len(page.Items) != 0 || page.ExpiredSkipped != 1 || page.NextCursor != nil {
		t.Fatal("expired partial match disclosed", len(page.Items), page.ExpiredSkipped, err)
	}
}

func TestAdminHistoryLargeDataset(t *testing.T) {
	if os.Getenv("DUEL_HISTORY_SCALE") != "1" {
		t.Skip("large history dataset requires an explicit scale run")
	}
	f := adminFixture(t, "bidding")
	id := f.terminalMatch(false, 0)
	ctx := adminContext()
	match, err := f.s.AdminDetail(ctx, "recent", id)
	if err != nil {
		t.Fatal(err)
	}
	header, _ := duel.Encode(match.Facts)
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO game_duel_anonymous(archive_id,game_key,mode,content_hash,header_json)VALUES(?,'bidding','tier1',?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for n := range 100000 {
		archive := fmt.Sprintf("dah_%021dA", n)
		if _, err := stmt.Exec(archive, match.Facts.ContentHash, string(header)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Log("inserted 100000 anonymous headers")
	// Synthetic read-volume data copies a terminal game's shape. This fixture
	// measures indexed reads; settlement and ledger replay use separate tests.
	tx, err = f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	clone := func(table, predicate string, overrides map[string]string, args ...any) {
		t.Helper()
		rows, err := tx.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			t.Fatal(err)
		}
		var columns, values []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			value, ok := overrides[name]
			if ok && value == "" {
				continue
			}
			if !ok {
				value = "v." + name
			}
			columns = append(columns, name)
			values = append(values, value)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		query := `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<100000) INSERT INTO ` + table + `(` + strings.Join(columns, ",") + `) SELECT ` + strings.Join(values, ",") + ` FROM n CROSS JOIN ` + table + ` v WHERE ` + predicate
		if _, err := tx.Exec(query, args...); err != nil {
			t.Fatal(table, err)
		}
	}
	const matchID = `('bid_'||printf('%021dA',n.i))`
	const operationID = `('op_'||printf('%021dA',n.i))`
	clone("credit_accounts", `v.id IN (SELECT general_account_id FROM game_duel_sessions WHERE id=? UNION ALL SELECT game_account_id FROM game_duel_sessions WHERE id=?)`, map[string]string{"id": "", "code": `('duel-session:'||` + matchID + `)`}, id, id)
	var high int64
	if err := tx.QueryRow(`SELECT MAX(ledger_seq) FROM credit_operations`).Scan(&high); err != nil {
		t.Fatal(err)
	}
	clone("credit_operations", `v.id=?`, map[string]string{"id": operationID, "source_id": matchID, "ledger_seq": fmt.Sprintf("%d+n.i", high), "actor_user_id": "NULL"}, match.Recent.OperationID)
	clone("game_duel_sessions", `v.id=?`, map[string]string{
		"id": matchID, "terminal_operation_id": operationID,
		"general_account_id": `(SELECT id FROM credit_accounts WHERE code='duel-session:'||` + matchID + ` AND asset_type='general')`,
		"game_account_id":    `(SELECT id FROM credit_accounts WHERE code='duel-session:'||` + matchID + ` AND asset_type='game')`,
	}, id)
	clone("game_duel_seats", `v.session_id=?`, map[string]string{"session_id": matchID, "user_id": "NULL"}, id)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Log("inserted 100000 synthetic recent headers")
	for _, selection := range []duel.AdminSelection{{}, {Mode: "tier1"}, {Outcome: "normal"}, {Outcome: "draw"}} {
		started := time.Now()
		page, err := f.s.AdminHistory(ctx, duel.AdminPageInput{Dataset: "recent", Selection: selection})
		elapsed := time.Since(started)
		if err != nil || selection.Outcome != "draw" && len(page.Items) != 20 || selection.Outcome == "draw" && len(page.Items) != 0 || elapsed > 5*time.Second {
			t.Fatal("large recent list", len(page.Items), elapsed, err)
		}
		t.Logf("recent selection=%+v list=%s records=%d", selection, elapsed, len(page.Items))
	}
	startedRecent := time.Now()
	recent, err := f.s.AdminExport(ctx, duel.AdminExportInput{Dataset: "recent"})
	if err != nil || len(recent.Items) != 100 || recent.NextCursor == nil {
		t.Fatal("large recent export", len(recent.Items), err)
	}
	t.Logf("recent export=%s records=%d", time.Since(startedRecent), len(recent.Items))
	for _, selection := range []duel.AdminSelection{{}, {Mode: "tier1"}, {Outcome: "normal"}, {Outcome: "draw"}} {
		started := time.Now()
		page, err := f.s.AdminHistory(ctx, duel.AdminPageInput{Dataset: "anonymous", Selection: selection})
		elapsed := time.Since(started)
		if err != nil || selection.Outcome != "draw" && len(page.Items) != 20 || selection.Outcome == "draw" && len(page.Items) != 0 || elapsed > 5*time.Second {
			t.Fatal("large list", len(page.Items), elapsed, err)
		}
		t.Logf("selection=%+v list=%s records=%d", selection, elapsed, len(page.Items))
	}
	started := time.Now()
	page, err := f.s.AdminExport(ctx, duel.AdminExportInput{Dataset: "anonymous"})
	if err != nil || len(page.Items) != 100 || page.NextCursor == nil || time.Since(started) > 5*time.Second {
		t.Fatal("large export", len(page.Items), time.Since(started), err)
	}
	t.Logf("export=%s records=%d", time.Since(started), len(page.Items))
}
