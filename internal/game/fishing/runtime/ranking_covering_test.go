package runtime

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

const activeRankingIndexSQL = `CREATE INDEX idx_fishing_rank_active_user ON game_fishing_rank_facts(user_id,expires_at,batch_id_text) WHERE aggregate_applied=1`

const unorderedRecentSingleLeaderboardSQL = `WITH candidates AS (
SELECT b.user_id,l.species_key,l.size_cm,l.blue_fat_fish_length_cm AS blue_length,l.milk_crab_length_cm AS milk_length,l.caught_at,b.public_tie_key
FROM game_fishing_best b
JOIN game_fishing_length_facts l ON l.batch_id_text=(
 SELECT candidate.batch_id_text FROM game_fishing_rank_facts fact
 CROSS JOIN game_fishing_length_facts candidate ON candidate.batch_id_text=fact.batch_id_text
 WHERE fact.user_id=b.user_id AND fact.aggregate_applied=1 AND fact.expires_at>?
 ORDER BY length(COALESCE(candidate.blue_fat_fish_length_cm,candidate.milk_crab_length_cm,CAST(candidate.size_cm AS TEXT))) DESC,
 COALESCE(candidate.blue_fat_fish_length_cm,candidate.milk_crab_length_cm,CAST(candidate.size_cm AS TEXT)) COLLATE BINARY DESC,
 candidate.caught_at ASC,candidate.batch_id_text ASC,candidate.ordinal ASC LIMIT 1)), ranked AS (
SELECT c.user_id,c.species_key,c.size_cm,c.blue_length,c.milk_length,ROW_NUMBER() OVER(ORDER BY
length(COALESCE(c.blue_length,c.milk_length,CAST(c.size_cm AS TEXT))) DESC,
COALESCE(c.blue_length,c.milk_length,CAST(c.size_cm AS TEXT)) COLLATE BINARY DESC,c.caught_at ASC,c.public_tie_key ASC) AS rank,
CASE WHEN u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) THEN 0 ELSE COALESCE(p.game_profile_public,u.game_profile_public) END AS game_profile_public,u.username,u.guild_nick,COALESCE(u.discord_id,'') AS discord_id,u.avatar,u.guild_avatar_url
FROM candidates c JOIN users u ON u.id=c.user_id LEFT JOIN game_user_preferences p ON p.user_id=u.id
WHERE u.is_admin=0)
SELECT user_id,species_key,size_cm,blue_length,milk_length,rank,game_profile_public,username,guild_nick,discord_id,avatar,guild_avatar_url FROM ranked WHERE rank<=20 OR user_id=? ORDER BY rank`

func TestRecentLeaderboardOrderProjectionPreservesRanking(t *testing.T) {
	database := recentRankingQueryFixture(t, 26, 80)
	if _, err := database.Exec(activeRankingIndexSQL); err != nil {
		t.Fatal(err)
	}
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
	if !strings.Contains(strings.Join(plan, "; "), "COVERING INDEX idx_fishing_recent_order") {
		t.Fatal("recent ranking does not use its covering order", plan)
	}
}

func BenchmarkRecentLeaderboardOrderedProjection(b *testing.B) {
	database := recentRankingQueryFixture(b, 100, 1000)
	if _, err := database.Exec(activeRankingIndexSQL); err != nil {
		b.Fatal(err)
	}
	for _, method := range []struct{ name, query string }{
		{"scan-and-sort", unorderedRecentSingleLeaderboardSQL},
		{"ordered-projection", recentSingleLeaderboardSQL},
	} {
		b.Run(method.name, func(b *testing.B) {
			tx, err := database.BeginTx(b.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			b.ResetTimer()
			for b.Loop() {
				if _, err := readRecentRankingQuery(b.Context(), tx, method.query, true, 100, fixtureNow); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
