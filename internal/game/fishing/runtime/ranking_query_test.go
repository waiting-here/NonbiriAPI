package runtime

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The window query is the independent reference for exact ordering and ties.
const windowRecentRankingSQL = `WITH candidates AS (
SELECT f.user_id,l.species_key,l.size_cm,l.blue_fat_fish_length_cm AS blue_length,l.milk_crab_length_cm AS milk_length,l.caught_at,b.public_tie_key,
ROW_NUMBER() OVER(PARTITION BY f.user_id ORDER BY
length(COALESCE(l.blue_fat_fish_length_cm,l.milk_crab_length_cm,CAST(l.size_cm AS TEXT))) DESC,
COALESCE(l.blue_fat_fish_length_cm,l.milk_crab_length_cm,CAST(l.size_cm AS TEXT)) COLLATE BINARY DESC,
l.caught_at ASC,l.batch_id_text ASC,l.ordinal ASC) AS pick
FROM game_fishing_length_facts l JOIN game_fishing_rank_facts f ON f.batch_id_text=l.batch_id_text
JOIN game_fishing_best b ON b.user_id=f.user_id
WHERE f.aggregate_applied=1 AND f.settled_at>?), ranked AS (
SELECT c.user_id,c.species_key,c.size_cm,c.blue_length,c.milk_length,ROW_NUMBER() OVER(ORDER BY
length(COALESCE(c.blue_length,c.milk_length,CAST(c.size_cm AS TEXT))) DESC,
COALESCE(c.blue_length,c.milk_length,CAST(c.size_cm AS TEXT)) COLLATE BINARY DESC,c.caught_at ASC,c.public_tie_key ASC) AS rank,
CASE WHEN u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) THEN 0 ELSE COALESCE(p.game_profile_public,u.game_profile_public) END AS game_profile_public,u.username,u.guild_nick,COALESCE(u.discord_id,'') AS discord_id,u.avatar,u.guild_avatar_url
FROM candidates c JOIN users u ON u.id=c.user_id LEFT JOIN game_user_preferences p ON p.user_id=u.id
WHERE c.pick=1 AND u.is_admin=0)
SELECT user_id,species_key,size_cm,blue_length,milk_length,rank,game_profile_public,username,guild_nick,discord_id,avatar,guild_avatar_url FROM ranked WHERE rank<=20 OR user_id=? ORDER BY rank`

// This small relational fixture isolates query work; business settlement,
// deletion and privacy are also exercised by the ordinary runtime tests.
func recentRankingQueryFixture(t testing.TB, users, batches int) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ranking.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE users(id INTEGER PRIMARY KEY,is_admin INTEGER,is_banned INTEGER,banned_until INTEGER,game_profile_public INTEGER,username TEXT,guild_nick TEXT,discord_id TEXT,avatar TEXT,guild_avatar_url TEXT);
CREATE TABLE game_user_preferences(user_id INTEGER PRIMARY KEY,game_profile_public INTEGER);
CREATE TABLE game_fishing_best(user_id INTEGER PRIMARY KEY,public_tie_key BLOB);
CREATE TABLE game_fishing_rank_facts(batch_id_text TEXT PRIMARY KEY,user_id INTEGER,settled_at INTEGER,expires_at INTEGER,aggregate_applied INTEGER,CHECK(expires_at=settled_at+2592000));
CREATE INDEX idx_fishing_rank_facts_user ON game_fishing_rank_facts(user_id,expires_at,batch_id_text);
CREATE TABLE game_fishing_length_facts(batch_id_text TEXT PRIMARY KEY,ordinal INTEGER,species_key TEXT,size_cm INTEGER,blue_fat_fish_length_cm TEXT,milk_crab_length_cm TEXT,caught_at INTEGER);`)
	for user := 1; user <= users; user++ {
		exec(`INSERT INTO users VALUES(?,0,?,NULL,1,?,'','','','')`, user, user%5 == 0, fmt.Sprintf("angler-%d", user))
		exec(`INSERT INTO game_fishing_best VALUES(?,?)`, user, []byte(fmt.Sprintf("%032d", user)))
		if user%3 == 0 {
			exec(`INSERT INTO game_user_preferences VALUES(?,0)`, user)
		}
		for batch := 0; batch < batches; batch++ {
			id := fmt.Sprintf("fb_%010d%011dA", user, batch)
			settled := fixtureNow - int64(batch%7)
			if batch%11 == 0 {
				settled = fixtureNow - int64(rankWindow.Seconds()) - int64(batch%3)
			}
			var blue, milk *string
			size := (user + batch) % 201
			if batch%9 == 0 {
				length := strings.Repeat("9", 3+user%126)
				if user%2 == 0 {
					blue = &length
				} else {
					milk = &length
				}
				size = 137
			}
			exec(`INSERT INTO game_fishing_rank_facts VALUES(?,?,?,?,?)`, id, user, settled, settled+int64(rankWindow.Seconds()), batch%13 != 0)
			species := "boot"
			if blue != nil || milk != nil {
				species = "koi"
			}
			exec(`INSERT INTO game_fishing_length_facts VALUES(?,0,?,?,?,?,?)`, id, species, size, blue, milk, fixtureNow-int64(batch%5))
		}
	}
	exec(`UPDATE users SET is_admin=1 WHERE id=2`)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return database
}

func readRecentRankingQuery(ctx context.Context, tx *sql.Tx, query string, indexed bool, user, now int64) (FishingLeaderboard, error) {
	window := now - int64(rankWindow.Seconds())
	first := window
	if indexed {
		first = now
	}
	rows, err := tx.QueryContext(ctx, query, first, now, user)
	return scanLengthLeaderboard(rows, err, user, "recent_single", &window)
}

func TestRecentLeaderboardIndexedSelectionMatchesWindowRanking(t *testing.T) {
	database := recentRankingQueryFixture(t, 26, 80)
	tx, err := database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, now := range []int64{fixtureNow, fixtureNow + 1, fixtureNow + int64(rankWindow.Seconds()) - 1, fixtureNow + int64(rankWindow.Seconds())} {
		for _, user := range []int64{1, 2, 5, 26} {
			want, err := readRecentRankingQuery(t.Context(), tx, windowRecentRankingSQL, false, user, now)
			if err != nil {
				t.Fatal(err)
			}
			got, err := readRecentRankingQuery(t.Context(), tx, recentSingleLeaderboardSQL, true, user, now)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(want)
			b, _ := json.Marshal(got)
			if !bytes.Equal(a, b) {
				t.Fatalf("user=%d time=%d: got=%s want=%s", user, now, b, a)
			}
		}
	}
	rows, err := tx.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+recentSingleLeaderboardSQL, fixtureNow, fixtureNow, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
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
	if !strings.Contains(strings.Join(plan, "; "), "idx_fishing_rank_facts_user (user_id=? AND expires_at>?)") {
		t.Fatal("missing per-user expiry range", plan)
	}
}

func BenchmarkRecentSingleLeaderboardSelection(b *testing.B) {
	database := recentRankingQueryFixture(b, 40, 500)
	for _, method := range []struct {
		name, query string
		indexed     bool
	}{{"window", windowRecentRankingSQL, false}, {"indexed", recentSingleLeaderboardSQL, true}} {
		b.Run(method.name, func(b *testing.B) {
			tx, err := database.BeginTx(b.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			b.ResetTimer()
			for b.Loop() {
				if _, err := readRecentRankingQuery(b.Context(), tx, method.query, method.indexed, 40, fixtureNow); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
