package runtime

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// Run explicitly with NONBIRI_POND_NO_SELF_SCALE=1. One private database is
// grown in place and removed by the fixture cleanup.
func TestPondNoSelfScaling(t *testing.T) {
	if os.Getenv("NONBIRI_POND_NO_SELF_SCALE") != "1" {
		t.Skip("explicit scaling evidence only")
	}
	f := newGameFixture(t, nil)
	receiver := f.seedUser("scale-self", fixtureFunding)
	donor := f.seedUser("scale-other", fixtureFunding)
	f.service.pondRandom = &scriptedSource{}
	var sqliteVersion string
	if err := f.database.QueryRow(`SELECT sqlite_version()`).Scan(&sqliteVersion); err != nil {
		t.Fatal(err)
	}
	const readsPerObservation = 20
	t.Logf("SQLite=%s; each query observation averages %d complete Rows consumptions; settle includes transaction acquisition, all terminal work and commit", sqliteVersion, readsPerObservation)
	seed := func(user int64, count int) {
		t.Helper()
		startID := f.scalar(`SELECT COALESCE(MAX(id),0) FROM game_fishing_pond_queue`)
		_, err := f.database.Exec(`WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<?)
INSERT INTO game_fishing_pond_queue(donor_user_id,source_batch_id,source_ordinal,species_key,donor_display_name,returned_at)
SELECT ?,printf('fb_%021dA',?+n),0,'boot','Scale donor',? FROM seq`, count, user, startID, fixtureNow)
		if err != nil {
			t.Fatal(err)
		}
	}
	consume := func(query string, args ...any) {
		t.Helper()
		rows, err := f.database.Query(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var id, donor int64
			var species, name string
			var avatar sql.NullString
			if err := rows.Scan(&id, &donor, &species, &name, &avatar); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if count != 10 {
			t.Fatalf("candidate count=%d", count)
		}
	}
	previous, key := 0, 1200
	for _, size := range []int{1000, 100000} {
		seedStart := time.Now()
		seed(receiver, size-previous)
		t.Logf("self-prefix=%d seed=%s", size, time.Since(seedStart))
		previous = size
		var naiveTotal, indexedTotal, settleTotal, maximum time.Duration
		for iteration := 0; iteration < 5; iteration++ {
			seed(donor, 10)
			started := time.Now()
			for range readsPerObservation {
				consume(`SELECT id,donor_user_id,species_key,donor_display_name,donor_avatar_url FROM game_fishing_pond_queue WHERE donor_user_id<>? ORDER BY id LIMIT ?`, receiver, 10)
			}
			naiveElapsed := time.Since(started) / readsPerObservation
			naiveTotal += naiveElapsed
			started = time.Now()
			for range readsPerObservation {
				consume(pondGiftCandidatesSQL, receiver, 10, 10, 10)
			}
			indexedElapsed := time.Since(started) / readsPerObservation
			indexedTotal += indexedElapsed
			f.random.mu.Lock()
			f.random.values = nil
			for range 10 {
				f.random.values = append(f.random.values, 0, 7)
			}
			f.random.mu.Unlock()
			f.service.beforeSettlement = func(string) error { return errInjected }
			_, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: receiver, Bait: "worm", Count: 10, IdempotencyKey: validTestKey(key)})
			f.service.beforeSettlement = nil
			key++
			if err != nil || pending == nil {
				t.Fatalf("reserve: %#v %v", pending, err)
			}
			started = time.Now()
			result, err := f.service.settle(context.Background(), pending.BatchID, receiver, fixtureNow, false)
			elapsed := time.Since(started)
			if err != nil || result == nil {
				t.Fatal("settle", err)
			}
			for _, outcome := range result.Outcomes {
				if outcome.Pond == nil || outcome.Pond.ReturnState != "gift" {
					t.Fatal("did not consume ten gifts")
				}
			}
			settleTotal += elapsed
			maximum = max(maximum, elapsed)
			t.Logf("self-prefix=%d iteration=%d naive_rows=%s indexed_rows=%s settle_commit=%s", size, iteration+1, naiveElapsed, indexedElapsed, elapsed)
		}
		t.Logf("self-prefix=%d mean_naive_rows=%s mean_indexed_rows=%s mean_settle_commit=%s max_settle_commit=%s", size, naiveTotal/5, indexedTotal/5, settleTotal/5, maximum)
		if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != int64(size) || f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_queue WHERE donor_user_id=?`, receiver) != int64(size) {
			t.Fatal("own queue changed or counter drifted")
		}
		assertPondHeadsMatchQueue(t, f)
	}
}
