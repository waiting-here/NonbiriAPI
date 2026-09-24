package ranking

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestNetProfitsOffsetLossesRetainExistingBoardsAndExpireAtSevenDays(t *testing.T) {
	f := newFixture(t)
	u, losing := f.user(false), f.user(true)
	e := f.epoch
	// Loss contains actual entry costs and after-fee returns; PositiveProfit is
	// the separate legacy pre-fee statistic and must not drive the new boards.
	f.add(u, e, -600, 0, "fishing")
	f.add(u, e+1, 200, 0, "fishing")
	f.add(u, e+2, 100, 900, "blackjack")
	f.add(u, e+3, -300, 300, "blackjack")
	f.add(u, e+4, -50, 0, "likes")
	f.add(losing, e+4, 50, 0, "fishing")
	for board, want := range map[string]string{"game_net_profit": "650", "fishing_net_profit": "400", "blackjack_net_profit": "200", "game_charity": "-650", "blackjack": "1200"} {
		if got, _, _ := f.total(u, board, "7d"); got != want {
			t.Fatalf("%s: %s != %s", board, got, want)
		}
	}
	for _, board := range netBoards {
		got := f.read(losing, board, "7d", 1, e+4)
		if len(got.Rows) != 1 || got.Me != nil || got.Rows[0].IsMe || got.Rows[0].Identity.Kind != "anonymous" {
			t.Fatal(board, got)
		}
	}
	f.advance(e + week)
	if got, _, _ := f.total(u, "fishing_net_profit", "7d"); got != "-200" {
		t.Fatal("expired positive contribution still counted", got)
	}
	if got := f.read(u, "fishing_net_profit", "7d", 1, e+week); len(got.Rows) != 0 || got.Me != nil {
		t.Fatal("nonpositive net profit exposed", got)
	}
	f.advance(e + week + 4)
	for _, board := range netBoards {
		if got, _, _ := f.total(u, board, "7d"); got != "0" {
			t.Fatal(board, got)
		}
	}
	if got, _, _ := f.total(u, "blackjack", "history"); got != "1200" {
		t.Fatal("legacy history changed", got)
	}
	if got, _, _ := f.total(u, "blackjack", "30d"); got != "1200" {
		t.Fatal("legacy month changed", got)
	}
}

func resetNetRebuild(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`DELETE FROM game_rank_totals WHERE board IN ('game_net_profit','fishing_net_profit','blackjack_net_profit')`,
		`DELETE FROM game_rank_net_rebuild_totals`,
		`UPDATE game_rank_net_rebuild SET phase=0,through_seq=NULL,last_seq=zeroblob(16) WHERE id=1`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNetProfitUpgradeBackfillIsBoundedResumableAndDoesNotDoubleCount(t *testing.T) {
	f := newFixture(t)
	u := f.user(false)
	f.tx(func(tx *sql.Tx) error {
		for i := range 600 {
			game, loss := "likes", int64(-1)
			if i%3 == 0 {
				game, loss = "fishing", -5
			}
			if i%3 == 1 {
				game, loss = "blackjack", 2
			}
			if err := RecordTx(context.Background(), tx, Contribution{u, game, fmt.Sprintf("legacy-%d", i), f.epoch, big.NewInt(loss), new(big.Int)}); err != nil {
				return err
			}
		}
		return nil
	})
	resetNetRebuild(t, f.db)
	f.tx(func(tx *sql.Tx) error {
		ready, used, err := advanceNetRebuild(context.Background(), tx, 3, time.Now().Add(time.Second))
		if ready || used != 3 || err != nil {
			t.Fatalf("unbounded rebuild: %v %d %v", ready, used, err)
		}
		return nil
	})
	// A failed live settlement cannot commit either itself or incidental
	// catch-up work. The next background transaction resumes the saved cursor.
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	c := Contribution{u, "fishing", "live-after-upgrade", f.epoch + 1, big.NewInt(-7), new(big.Int)}
	if err := RecordTx(context.Background(), tx, c); !errors.Is(err, ErrCatchingUp) {
		t.Fatal("settlement crossed incomplete backfill", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	f.advance(f.epoch + 1)
	for board, want := range map[string]string{"game_net_profit": "800", "fishing_net_profit": "1000", "blackjack_net_profit": "-400"} {
		if got, _, _ := f.total(u, board, "7d"); got != want {
			t.Fatal(board, got, want)
		}
	}
	f.tx(func(tx *sql.Tx) error { return RecordTx(context.Background(), tx, c) })
	f.advance(f.epoch + 1)
	if got, _, _ := f.total(u, "game_net_profit", "7d"); got != "807" {
		t.Fatal("live event counted twice", got)
	}
	var epoch, events, scratch int64
	if err := f.db.QueryRow(`SELECT started_at FROM game_statistics_epoch`).Scan(&epoch); err != nil || epoch != f.epoch {
		t.Fatal("statistics reset", epoch, err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM game_rank_events`).Scan(&events); err != nil || events != 601 {
		t.Fatal(events, err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM game_rank_net_rebuild_totals`).Scan(&scratch); err != nil || scratch != 0 {
		t.Fatal(scratch, err)
	}
	f.advance(f.epoch + week)
	if got, _, _ := f.total(u, "game_net_profit", "7d"); got != "7" {
		t.Fatal("backfilled expiry diverged", got)
	}
}

func TestNetProfitUpgradeResumesAnOldPartialExpiryGroup(t *testing.T) {
	f := newFixture(t)
	u := f.user(false)
	f.add(u, f.epoch, -10, 0, "fishing")
	f.add(u, f.epoch, 20, 0, "blackjack")
	f.add(u, f.epoch, -3, 0, "likes")
	f.add(u, f.epoch+1, -9, 0, "fishing")
	f.tx(func(tx *sql.Tx) error {
		group, err := nextGroup(context.Background(), tx, f.epoch+week)
		if err != nil {
			return err
		}
		_, err = advanceGroup(context.Background(), tx, group, 1)
		return err
	})
	// Older deployments had no net-profit scratch columns. The cleared first
	// event is no longer eligible for backfill or for a second subtraction.
	if _, err := f.db.Exec(`UPDATE game_rank_expiry_work SET net_game_delta_sign=0,net_game_delta_mag=zeroblob(32),net_fishing_delta_sign=0,net_fishing_delta_mag=zeroblob(32),net_blackjack_delta_sign=0,net_blackjack_delta_mag=zeroblob(32),net_fishing_last_seq=zeroblob(16),net_blackjack_last_seq=zeroblob(16)`); err != nil {
		t.Fatal(err)
	}
	resetNetRebuild(t, f.db)
	f.advance(f.epoch + week)
	for board, want := range map[string]string{"game_net_profit": "9", "fishing_net_profit": "9", "blackjack_net_profit": "0", "game_charity": "-9"} {
		if got, _, _ := f.total(u, board, "7d"); got != want {
			t.Fatal(board, got, want)
		}
	}
}

func TestNetProfitTopTwentyOwnRankPrivacyAndTieOrdering(t *testing.T) {
	f := newFixture(t)
	users := make([]int64, 24)
	for i := range users {
		users[i] = f.user(true, i == 2)
		f.add(users[i], f.epoch, -100, 0, "fishing")
	}
	if _, err := f.db.Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, users[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE users SET game_profile_public=0 WHERE id=?`, users[1]); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"game_net_profit", "fishing_net_profit"} {
		board := f.read(users[23], key, "7d", 1, f.epoch)
		if len(board.Rows) != 20 || board.Me == nil || board.Me.Rank != "23" || !board.Me.IsMe {
			t.Fatal(board)
		}
		if board.Rows[0].Identity.Kind != "anonymous" || board.Rows[1].Identity.Kind != "anonymous" || board.Rows[2].Identity.DisplayName != "player-7" {
			t.Fatal("privacy or sequence tie changed", board.Rows[:3])
		}
		raw, _ := json.Marshal(board.Rows[:2])
		if strings.Contains(string(raw), "player") || strings.Contains(string(raw), "user_id") || strings.Contains(string(raw), "avatar") {
			t.Fatal(string(raw))
		}
	}
}

type netRankRegistrar map[string]resources.AuthorizedUserHandler

func TestNetProfitExpiryTieUsesOnlyItsGameContributions(t *testing.T) {
	for _, game := range []string{"fishing", "blackjack"} {
		t.Run(game, func(t *testing.T) {
			f := newFixture(t)
			a, b := f.user(true), f.user(true)
			f.add(a, f.epoch, 100, 0, game)
			f.add(b, f.epoch, 100, 0, game)
			f.add(a, f.epoch, 1, 0, "likes")
			f.add(a, f.epoch+1, -200, 0, game)
			f.add(b, f.epoch+1, -200, 0, game)
			// Persist a partial group so each board's own sequence must survive
			// transaction boundaries while a later unrelated event is removed.
			f.tx(func(tx *sql.Tx) error {
				group, err := nextGroup(context.Background(), tx, f.epoch+week)
				if err != nil {
					return err
				}
				_, err = advanceGroup(context.Background(), tx, group, 1)
				return err
			})
			f.advance(f.epoch + week)
			board := f.read(a, game+"_net_profit", "7d", 1, f.epoch+week)
			if len(board.Rows) != 2 || !board.Rows[0].IsMe || board.Rows[0].Amount != "0.2" {
				t.Fatal("unrelated contribution reversed game tie", board)
			}
			global := f.read(a, "game_net_profit", "7d", 1, f.epoch+week)
			if len(global.Rows) != 2 || global.Rows[0].IsMe {
				t.Fatal("global tie did not include all games", global)
			}
		})
	}
}

func (r netRankRegistrar) RegisterUserRoute(method, path string, handler resources.AuthorizedUserHandler) error {
	r[method+" "+path] = handler
	return nil
}

func TestNetProfitRoutesOnlyAcceptSevenDayWindow(t *testing.T) {
	f := newFixture(t)
	u := f.user(false)
	s, err := New(f.db, allowRankUser{}, func() time.Time { return time.Unix(f.epoch, 0) })
	if err != nil {
		t.Fatal(err)
	}
	routes := netRankRegistrar{}
	if err := RegisterRoutes(routes, s); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/games/leaderboards/net-profit", "/api/games/fishing/net-profit", "/api/games/blackjack/net-profit"} {
		for _, query := range []string{"", "?window=7d", "?window=30d", "?window=history", "?window=7d&window=7d", "?page=1"} {
			response := httptest.NewRecorder()
			routes["GET "+path](response, httptest.NewRequest(http.MethodGet, path+query, nil), resources.UserPrincipal{UserID: u})
			want := http.StatusBadRequest
			if query == "" || query == "?window=7d" {
				want = http.StatusOK
			}
			if response.Code != want {
				t.Fatalf("%s%s: %d %s", path, query, response.Code, response.Body.String())
			}
		}
	}
}
