package linklink

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const rankSummaryInsertSQL = `
INSERT INTO game_linklink_summaries(session_id,user_id,spec,price_milli,terminal_reason,started_at,deadline,terminal_at,pairs_removed,score,rules_version,assists_initial,assists_remaining)
VALUES(?,?,?,1000,?,?,?,?,?,?,?,?,?)`

func rankSummaryValues(id string, user int64, spec string, version int, reason string, at, elapsed int64, remaining int) []any {
	d, _ := resolveSpec(spec)
	initial, pairs := 0, d.totalPairs()
	if version == 2 {
		initial = d.assists()
	}
	var score any
	switch reason {
	case TerminalCompleted:
		score = int64(pairs*100) + d.Seconds - elapsed + int64(remaining*100)
	case TerminalTimedOut:
		pairs--
		elapsed = d.Seconds
		score = pairs * 100
	case TerminalAbandoned:
		pairs = 0
	}
	return []any{id, user, spec, reason, at - elapsed, at - elapsed + d.Seconds, at, pairs, score, version, initial, remaining}
}

func insertRankSummary(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
},
	id string, user int64, spec string, version int, reason string, at, elapsed int64, remaining int) error {
	_, err := exec.ExecContext(ctx, rankSummaryInsertSQL, rankSummaryValues(id, user, spec, version, reason, at, elapsed, remaining)...)
	return err
}

func (f *fixture) rankSummary(user int64, spec string, version int, reason string, at, elapsed int64, remaining int) {
	f.t.Helper()
	if err := insertRankSummary(context.Background(), f.database, f.mustID("ll_"), user, spec, version, reason, at, elapsed, remaining); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) rankUser(label string, tie byte) int64 {
	f.t.Helper()
	user := f.seedIdentity(label, false)
	if _, err := f.database.Exec("INSERT INTO game_user_preferences(user_id,linklink_public_tie_key,updated_at) VALUES(?,?,?)", user, bytes.Repeat([]byte{tie}, 32), testNow); err != nil {
		f.t.Fatal(err)
	}
	return user
}

func TestLeaderboardsUseWindowBestAndEarliestAchievement(t *testing.T) {
	for _, spec := range []string{"6x8", "8x8", "10x10"} {
		t.Run(spec, func(t *testing.T) {
			f := newFixture(t)
			earlier := f.rankUser("earlier", 255)
			later := f.rankUser("later", 1)
			edge := f.rankUser("edge", 3)
			def, _ := resolveSpec(spec)
			// Same score: achievement time must precede the anonymous tie key.
			f.rankSummary(earlier, spec, 2, TerminalCompleted, testNow-20, 20, 1)
			f.rankSummary(later, spec, 2, TerminalCompleted, testNow-10, 20, 1)
			// This personal best expires at the strict 7-day boundary. The next best must surface.
			f.rankSummary(earlier, spec, 2, TerminalCompleted, testNow-7*86400, 0, def.assists())
			// Future, failed and legacy records cannot affect either board.
			f.rankSummary(later, spec, 2, TerminalCompleted, testNow+1, 0, def.assists())
			f.rankSummary(later, spec, 1, TerminalCompleted, testNow, 0, 0)
			f.rankSummary(later, spec, 2, TerminalTimedOut, testNow, def.Seconds, def.assists())
			f.rankSummary(later, spec, 2, TerminalAbandoned, testNow, 0, def.assists())
			f.rankSummary(edge, spec, 2, TerminalCompleted, testNow-30*86400, 0, def.assists())
			for _, window := range []string{"7d", "30d"} {
				board, err := f.service.Leaderboard(context.Background(), earlier, spec, window)
				if err != nil || len(board.Rows) != 2 || !board.Rows[0].IsMe || board.Me != nil {
					t.Fatal(board, err)
				}
				wantAt := testNow - 20
				if window == "30d" {
					wantAt = testNow - 7*86400
				}
				if board.Rows[0].AchievedAt != wantAt || board.AsOf != testNow || board.Rows[0].Rank != "1" {
					t.Fatal(board)
				}
			}
			// Exactly equal timestamps fall back to the saved random key, not account order.
			same := f.rankUser("same-time", 0)
			f.rankSummary(same, spec, 2, TerminalCompleted, testNow-20, 20, 1)
			board, err := f.service.Leaderboard(context.Background(), same, spec, "7d")
			if err != nil || !board.Rows[0].IsMe || board.Rows[1].AchievedAt != testNow-20 {
				t.Fatal(board, err)
			}
		})
	}
}

func TestLeaderboardTopTwentyPrivacyDeletionAndTieKeyPersistence(t *testing.T) {
	f := newFixture(t)
	users := make([]int64, 25)
	for i := range users {
		users[i] = f.rankUser(fmt.Sprintf("rank-%02d", i), byte(i))
		f.rankSummary(users[i], "6x8", 2, TerminalCompleted, testNow-int64(25-i), 0, 2)
	}
	board, err := f.service.Leaderboard(context.Background(), users[24], "6x8", "7d")
	if err != nil || len(board.Rows) != 20 || board.Me == nil || board.Me.Rank != "25" {
		t.Fatal(board, err)
	}
	raw, _ := json.Marshal(board)
	if bytes.Contains(raw, []byte("user_id")) || bytes.Contains(raw, []byte("rank-")) || bytes.Contains(raw, []byte("avatar")) {
		t.Fatal("anonymous rank leaked profile", string(raw))
	}
	if _, err := f.database.Exec("UPDATE game_user_preferences SET game_profile_public=1 WHERE user_id=?", users[0]); err != nil {
		t.Fatal(err)
	}
	board, err = f.service.Leaderboard(context.Background(), users[24], "6x8", "7d")
	if err != nil || board.Rows[0].Identity.Kind != "public" || board.Rows[0].Identity.DisplayName != "rank-00" {
		t.Fatal(board, err)
	}
	if _, err := f.database.Exec("UPDATE game_user_preferences SET game_profile_public=0 WHERE user_id=?", users[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.Exec("DELETE FROM users WHERE id=?", users[1]); err != nil {
		t.Fatal(err)
	}
	board, err = f.service.Leaderboard(context.Background(), users[24], "6x8", "7d")
	if err != nil || board.Rows[0].Identity.Kind != "anonymous" || board.Me.Rank != "24" {
		t.Fatal(board, err)
	}

	user, binding := f.seedUser("first-game", testFunding)
	if _, err := f.database.Exec("UPDATE users SET game_profile_public=1 WHERE id=?", user); err != nil {
		t.Fatal(err)
	}
	state := f.startCurrent(user, binding, "6x8", 7600)
	var first, second []byte
	if err := f.database.QueryRow("SELECT linklink_public_tie_key FROM game_user_preferences WHERE user_id=?", user).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 || bytes.Equal(first, make([]byte, 32)) || f.scalar("SELECT game_profile_public FROM game_user_preferences WHERE user_id=?", user) != 1 {
		t.Fatal("tie key creation changed privacy or lacked randomness")
	}
	if _, err := f.service.Abandon(context.Background(), AbandonInput{UserID: user, SessionBinding: binding, SessionID: state.SessionID, ExpectedRevision: state.Revision, Confirmation: true, IdempotencyKey: f.key(7601)}); err != nil {
		t.Fatal(err)
	}
	f.startCurrent(user, binding, "8x8", 7602)
	if err := f.database.QueryRow("SELECT linklink_public_tie_key FROM game_user_preferences WHERE user_id=?", user).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("new board replaced public tie key")
	}
}

func TestLeaderboardHTTPRejectsUnboundedOrAmbiguousSelection(t *testing.T) {
	f := newFixture(t)
	user := f.rankUser("http-ranking", 1)
	api := &httpAPI{service: f.service}
	for _, query := range []string{"", "spec=6x8", "spec=6x8&window=7d&window=30d", "spec=bad&window=7d", "spec=6x8&window=all", "spec=6x8&window=7d&limit=1000"} {
		recorder := httptest.NewRecorder()
		api.leaderboard(recorder, httptest.NewRequest(http.MethodGet, RouteLeaderboard+"?"+query, nil), resources.UserPrincipal{UserID: user})
		if recorder.Code != http.StatusBadRequest {
			t.Fatal(query, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	api.leaderboard(recorder, httptest.NewRequest(http.MethodGet, RouteLeaderboard+"?spec=6x8&window=7d", nil), resources.UserPrincipal{UserID: user})
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
}

func TestLeaderboardHundredThousandSummariesUseWindowIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("large summary query")
	}
	f := newFixture(t)
	ctx := context.Background()
	instrumented := false
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-race" && setting.Value == "true" {
				instrumented = true
			}
		}
	}
	users := make([]int64, 100)
	for i := range users {
		users[i] = f.rankUser(fmt.Sprintf("large-%d", i), byte(i))
	}
	tx, err := f.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(ctx, rankSummaryInsertSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for i := 0; i < 100000; i++ {
		id, err := db.GenerateOpaqueID("ll_")
		if err != nil {
			t.Fatal(err)
		}
		spec := []string{"6x8", "8x8", "10x10"}[i%3]
		if _, err := statement.ExecContext(ctx, rankSummaryValues(id, users[i%len(users)], spec, 2, TerminalCompleted, testNow-int64(i%2400000), int64(i%50), 1)...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = f.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	plans, err := tx.QueryContext(ctx, "EXPLAIN QUERY PLAN "+leaderboardSQL, "6x8", testNow-7*86400, testNow, testNow, users[99])
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for plans.Next() {
		var a, b, c int
		var detail string
		if err := plans.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := plans.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(details, "\n"), "idx_linklink_leaderboard") {
		t.Fatal(details)
	}
	for _, spec := range []string{"6x8", "8x8", "10x10"} {
		for _, days := range []int{7, 30} {
			start := time.Now()
			// Race instrumentation measures correctness, not production latency.
			// The ordinary run retains the two-second budget for all six queries.
			queryCtx, cancel := ctx, func() {}
			if !instrumented {
				queryCtx, cancel = context.WithTimeout(ctx, 2*time.Second)
			}
			result, err := queryLeaderboard(queryCtx, tx, users[99], spec, days, testNow)
			cancel()
			if err != nil || len(result.Rows) != 20 {
				t.Fatal(result, err)
			}
			t.Logf("100000 summaries: %s/%dd query=%s returned=%d plus_me=%t race=%t", spec, days, time.Since(start), len(result.Rows), result.Me != nil, instrumented)
		}
	}
	t.Logf("query plan: %s", strings.Join(details, "; "))
}
