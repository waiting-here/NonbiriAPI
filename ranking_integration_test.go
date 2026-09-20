package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
)

func TestRankingsFollowRealDuelFinanceAndPrivateAuthorization(t *testing.T) {
	for _, game := range []string{"bidding", "likes"} {
		t.Run(game, func(t *testing.T) {
			f := newDuelWireFixture(t)
			var epoch int64
			if err := f.store.DB().QueryRow(`SELECT started_at FROM game_statistics_epoch`).Scan(&epoch); err != nil {
				t.Fatal(err)
			}
			f.clock.Store(max(f.clock.Load(), epoch+1))
			mode := "tier1"
			if game == "likes" {
				mode = "quick"
			}
			s := f.matched(game, mode)
			r := f.call(0, http.MethodPost, "/api/games/"+game+"/sessions/"+s.ID+"/surrender", map[string]string{"phase_seq": s.PhaseSeq}, false)
			if r.Code != 200 {
				t.Fatal(r.Code, r.Body)
			}
			var ticket, prize int64
			if err := f.store.DB().QueryRow(`SELECT ticket_milli,prize_milli FROM game_duel_sessions WHERE id=?`, s.ID).Scan(&ticket, &prize); err != nil {
				t.Fatal(err)
			}
			for seat, user := range f.users {
				var sign int
				var mag, profit []byte
				if err := f.store.DB().QueryRow(`SELECT loss_sign,loss_mag,positive_profit FROM game_rank_events WHERE user_id=? AND source_id=?`, user, s.ID).Scan(&sign, &mag, &profit); err != nil {
					t.Fatal(err)
				}
				loss, err := db.NewSM128(sign, mag)
				if err != nil {
					t.Fatal(err)
				}
				want := ticket
				if seat == 1 {
					want = -prize
				}
				if loss.Big().Int64() != want {
					t.Fatal("reward polluted loss", seat, loss.Decimal(), want)
				}
				gain, err := db.DecodeU128(profit)
				if err != nil {
					t.Fatal(err)
				}
				want = 0
				if game == "bidding" && seat == 1 {
					want = ticket
				}
				if gain.Big().Int64() != want {
					t.Fatal("gross profit", gain, want)
				}
			}
			f.checkLedger()
			// All four routes are wired and share the user-station session boundary.
			for _, path := range []string{"/api/charity/leaderboard", "/api/games/leaderboards/charity", "/api/games/bidding/leaderboard?window=history", "/api/games/blackjack/leaderboard"} {
				response := f.call(0, http.MethodGet, path, nil, false)
				if response.Code != 200 {
					t.Fatal(path, response.Code, response.Body)
				}
				var board ranking.Board
				if err := json.Unmarshal(response.Body.Bytes(), &board); err != nil {
					t.Fatal(err)
				}
				unauthorized := testApplicationRequest(t, f.app.handler, http.MethodGet, auditUserHost, path, "", nil, nil)
				if unauthorized.Code != 401 {
					t.Fatal("unauthenticated rank read", path, unauthorized.Code)
				}
			}
			for _, query := range []string{"?window=all", "?window=7d&window=30d", "?owner=1"} {
				if response := f.call(0, http.MethodGet, "/api/games/bidding/leaderboard"+query, nil, false); response.Code != 400 {
					t.Fatal(query, response.Code)
				}
			}
			if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1,game_profile_public=1,banned_reason='test' WHERE id=?`, f.users[0]); err != nil {
				t.Fatal(err)
			}
			response := f.call(1, http.MethodGet, "/api/games/leaderboards/charity", nil, false)
			var board ranking.Board
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &board) != nil || len(board.Rows) != 1 || board.Rows[0].Identity.Kind != "anonymous" {
				t.Fatal(response.Code, response.Body)
			}
			if denied := f.call(0, http.MethodGet, "/api/games/leaderboards/charity", nil, false); denied.Code != 403 {
				t.Fatal("ban login gate", denied.Code)
			}
			if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=0 WHERE id=?`, f.users[0]); err != nil {
				t.Fatal(err)
			}
			response = f.call(0, http.MethodPost, "/api/account/delete", map[string]string{"confirm": "DELETE"}, true)
			if response.Code != 204 {
				t.Fatal(response.Code, response.Body)
			}
			for _, table := range []string{"game_rank_events", "game_rank_totals", "game_rank_expiry_work"} {
				var n int
				if err := f.store.DB().QueryRow(fmt.Sprintf("SELECT count(*) FROM %s WHERE user_id=?", table), f.users[0]).Scan(&n); err != nil || n != 0 {
					t.Fatal(table, n, err)
				}
			}
		})
	}
}
