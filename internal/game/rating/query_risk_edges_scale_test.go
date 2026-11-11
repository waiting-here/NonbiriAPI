package rating

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
)

const ratingEdgesNow int64 = 1800000000

// A read-query fixture with the production result keys/indexes and current
// identity projection. This intentionally does not claim settlement coverage.
func ratingEdgesDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "rating.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(2)
	t.Cleanup(func() { database.Close() })
	_, err = database.Exec(`PRAGMA journal_mode=WAL;
CREATE TABLE users(id INTEGER PRIMARY KEY,is_admin INTEGER NOT NULL DEFAULT 0,is_banned INTEGER NOT NULL DEFAULT 0,banned_until INTEGER,game_profile_public INTEGER NOT NULL DEFAULT 1,username TEXT NOT NULL,guild_nick TEXT NOT NULL DEFAULT '',discord_id TEXT NOT NULL DEFAULT '',avatar TEXT NOT NULL DEFAULT '',guild_avatar_url TEXT NOT NULL DEFAULT '');
CREATE TABLE game_user_preferences(user_id INTEGER PRIMARY KEY,game_profile_public INTEGER NOT NULL);
CREATE TABLE game_duel_results(session_id TEXT NOT NULL,game_key TEXT NOT NULL CHECK(game_key='gwent'),user_id INTEGER NOT NULL,result INTEGER NOT NULL CHECK(result IN(0,1,2)),rating_before INTEGER NOT NULL,rating_after INTEGER NOT NULL,settled_at INTEGER NOT NULL,PRIMARY KEY(session_id,user_id)) STRICT, WITHOUT ROWID;
CREATE INDEX idx_duel_results_window ON game_duel_results(game_key,settled_at,user_id);
CREATE INDEX idx_duel_results_user ON game_duel_results(user_id,game_key,settled_at,session_id);
CREATE TABLE game_duel_ratings(game_key TEXT NOT NULL,user_id INTEGER NOT NULL,rating INTEGER NOT NULL,played INTEGER NOT NULL,updated_at INTEGER NOT NULL,PRIMARY KEY(game_key,user_id)) STRICT, WITHOUT ROWID;
WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100) INSERT INTO users(id,username) SELECT x,printf('user-%d',x) FROM n;`)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

const ratingEdgesScores = `WITH scores AS (
 SELECT user_id,sum(result=2) wins,count(*) played,sum(result=1) draws,max(CASE WHEN result=2 THEN settled_at END) achieved_at
 FROM game_duel_results WHERE game_key=? AND settled_at>? AND settled_at<=? GROUP BY user_id HAVING wins>0
), ranked AS (
 SELECT *,ROW_NUMBER() OVER(ORDER BY wins DESC,played,achieved_at,user_id) rank FROM scores
)`

const ratingEdgesColumns = `SELECT r.user_id,r.rank,r.wins,r.played,r.draws,
CASE WHEN u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) THEN 0 ELSE COALESCE(p.game_profile_public,u.game_profile_public) END,
u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url`

const ratingEdgesOldQuery = ratingEdgesScores + ratingEdgesColumns + `
FROM ranked r JOIN users u ON u.id=r.user_id LEFT JOIN game_user_preferences p ON p.user_id=r.user_id
WHERE u.is_admin=0 AND (r.rank<=20 OR r.user_id=?) ORDER BY r.rank`

const ratingEdgesSelectedQuery = ratingEdgesScores + `, selected AS MATERIALIZED (SELECT * FROM ranked WHERE rank<=20 OR user_id=?)
` + ratingEdgesColumns + ` FROM selected r JOIN users u ON u.id=r.user_id LEFT JOIN game_user_preferences p ON p.user_id=r.user_id
WHERE u.is_admin=0 ORDER BY r.rank`

func ratingEdgesLegacyRead(ctx context.Context, tx *sql.Tx, days, user int64) (Board, error) {
	board := Board{AsOf: ratingEdgesNow, Window: fmt.Sprintf("%dd", days), Rows: []Row{}}
	rows, err := tx.QueryContext(ctx, ratingEdgesOldQuery, "gwent", ratingEdgesNow-days*86400, ratingEdgesNow, ratingEdgesNow, user)
	if err != nil {
		return board, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, place, wins, played, draws int64
		var public bool
		var name, nick, discord, avatar, guild string
		if err := rows.Scan(&id, &place, &wins, &played, &draws, &public, &name, &nick, &discord, &avatar, &guild); err != nil {
			return board, err
		}
		row := Row{Rank: strconv.FormatInt(place, 10), Wins: strconv.FormatInt(wins, 10), Played: strconv.FormatInt(played, 10), Draws: strconv.FormatInt(draws, 10), IsMe: id == user, Identity: ranking.ProfileIdentity(public, name, nick, discord, avatar, guild)}
		if place > 20 {
			board.Me = &row
		} else {
			board.Rows = append(board.Rows, row)
		}
	}
	return board, rows.Err()
}

func TestQueryRiskEdgesRatingBoundariesAndCurrentIdentity(t *testing.T) {
	database := ratingEdgesDatabase(t)
	for i, fact := range []struct{ user, result, at int64 }{
		{1, 2, ratingEdgesNow - 30*86400}, {1, 2, ratingEdgesNow - 7*86400}, {1, 2, ratingEdgesNow - 7*86400 + 1}, {1, 2, ratingEdgesNow + 1},
		{2, 2, ratingEdgesNow - 2}, {3, 2, ratingEdgesNow - 2}, {3, 1, ratingEdgesNow - 1}, {4, 0, ratingEdgesNow},
		{5, 2, ratingEdgesNow - 2}, {6, 2, ratingEdgesNow},
	} {
		if _, err := database.Exec(`INSERT INTO game_duel_results VALUES(?,'gwent',?,?,1500,1516,?)`, fmt.Sprint(i), fact.user, fact.result, fact.at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`UPDATE users SET is_banned=1,banned_until=? WHERE id=5; INSERT INTO game_user_preferences VALUES(6,0)`, ratingEdgesNow+1); err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"7d", "30d"} {
		tx, err := database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		got, err := ReadTx(t.Context(), tx, "gwent", 3, window, ratingEdgesNow)
		tx.Rollback()
		if err != nil || len(got.Rows) != 5 || got.Me != nil {
			t.Fatal(got, err)
		}
		wins := "1"
		if window == "30d" {
			wins = "2"
		}
		if got.Rows[0].Identity.DisplayName != "user-1" || got.Rows[0].Wins != wins || got.Rows[1].Identity.DisplayName != "user-2" || got.Rows[2].Identity.Kind != "anonymous" || got.Rows[3].Identity.Kind != "anonymous" || !got.Rows[4].IsMe || got.Rows[4].Played != "2" || got.Rows[4].Draws != "1" {
			t.Fatalf("exact edges, tie order or current privacy changed: %+v", got)
		}
	}
	tx, err := database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before, err := ReadTx(t.Context(), tx, "gwent", 3, "7d", ratingEdgesNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE users SET is_banned=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	after, err := ReadTx(t.Context(), tx, "gwent", 3, "7d", ratingEdgesNow)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rank and identity escaped the same read snapshot", after, err)
	}
	tx.Rollback()
	tx, err = database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	current, err := ReadTx(t.Context(), tx, "gwent", 3, "7d", ratingEdgesNow)
	if err != nil || current.Rows[0].Identity.Kind != "anonymous" {
		t.Fatal("new snapshot retained a newly banned public identity", current, err)
	}
}

func TestQueryRiskEdgesRatingScale(t *testing.T) {
	if os.Getenv("NONBIRI_QUERY_RISK_EDGES") != "1" {
		t.Skip("opt-in rating read-query workload")
	}
	for _, count := range []int{10000, 100000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			database := ratingEdgesDatabase(t)
			start := time.Now()
			_, err := database.Exec(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?)
INSERT INTO game_duel_results SELECT printf('gwt_%021dA',x),'gwent',x%100+1,CASE WHEN x%7=0 THEN 1 WHEN x%3=0 THEN 0 ELSE 2 END,1500,1516,?-(x*7919)%(40*86400) FROM n`, count, ratingEdgesNow)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("setup result_facts=%d users=100 elapsed=%s", count, time.Since(start))
			for _, days := range []int64{7, 30} {
				var currentUser int64
				if err := database.QueryRowContext(t.Context(), ratingEdgesScores+` SELECT user_id FROM ranked ORDER BY rank DESC LIMIT 1`, "gwent", ratingEdgesNow-days*86400, ratingEdgesNow).Scan(&currentUser); err != nil {
					t.Fatal(err)
				}
				var expected Board
				for _, mode := range []string{"legacy-first", "selected-first", "selected-warm", "legacy-warm"} {
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					start := time.Now()
					tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
					if err != nil {
						cancel()
						t.Fatal(err)
					}
					var got Board
					if strings.HasPrefix(mode, "legacy") {
						got, err = ratingEdgesLegacyRead(ctx, tx, days, currentUser)
					} else {
						got, err = ReadTx(ctx, tx, "gwent", currentUser, fmt.Sprintf("%dd", days), ratingEdgesNow)
					}
					tx.Rollback()
					cancel()
					t.Logf("window=%dd mode=%s elapsed=%s top=%d has_me=%t error=%v", days, mode, time.Since(start), len(got.Rows), got.Me != nil, err)
					if err != nil {
						t.Fatal(err)
					}
					if len(got.Rows) != 20 || got.Me == nil || !got.Me.IsMe || got.Me.Rank != "100" {
						t.Fatal("top page or separate own rank changed", got)
					}
					if mode == "legacy-first" {
						expected = got
					} else if !reflect.DeepEqual(got, expected) {
						t.Fatal("ranking changed", got, expected)
					}
				}
				rows, err := database.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+ratingEdgesSelectedQuery, "gwent", ratingEdgesNow-days*86400, ratingEdgesNow, currentUser, ratingEdgesNow)
				if err != nil {
					t.Fatal(err)
				}
				var plan []string
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
						t.Fatal(err)
					}
					plan = append(plan, detail)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				t.Logf("window=%dd selected_plan=%s", days, strings.Join(plan, "; "))
			}
		})
	}
}
