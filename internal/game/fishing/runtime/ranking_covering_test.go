package runtime

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

const activeRankingIndexSQL = `CREATE INDEX idx_fishing_rank_active_user ON game_fishing_rank_facts(user_id,expires_at,batch_id_text) WHERE aggregate_applied=1`

func TestRecentLeaderboardActiveIndexPreservesRanking(t *testing.T) {
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
	if !strings.Contains(strings.Join(plan, "; "), "COVERING INDEX idx_fishing_rank_active_user") {
		t.Fatal("active ranking still reads the fact table", plan)
	}
}

func BenchmarkRecentLeaderboardActiveIndex(b *testing.B) {
	database := recentRankingQueryFixture(b, 40, 2000)
	for _, indexed := range []bool{false, true} {
		name := "existing"
		if indexed {
			name = "covering"
			if _, err := database.Exec(activeRankingIndexSQL); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(name, func(b *testing.B) {
			tx, err := database.BeginTx(b.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			b.ResetTimer()
			for b.Loop() {
				if _, err := readRecentRankingQuery(b.Context(), tx, recentSingleLeaderboardSQL, true, 40, fixtureNow); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
