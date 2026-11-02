package runtime

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPondBoardsUseReaderPoolAndCastsProgressWithReader(t *testing.T) {
	f := newGameFixture(t, nil)
	user := f.seedUser("pond-pool-user", fixtureFunding)
	pondCatch(t, f, user, 970, 0)
	tx, err := f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE game_fishing_pond_queue_state SET item_count=item_count WHERE id=1`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	board, err := f.service.PondLeaderboard(ctx, user, "cleanup", "7d")
	tx.Rollback()
	if err != nil || len(board.Entries) != 1 {
		t.Fatalf("board behind held writer: %#v %v", board, err)
	}
	reader, err := f.store.ReadDB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var before int64
	if err = reader.QueryRow(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='cleanup'`, user).Scan(&before); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	pondCatch(t, f, user, 971, 1)
	if _, err = f.database.Exec(`UPDATE maintenance_state SET changed_at=changed_at WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("held reader blocked normal writers: %s", elapsed)
	}
	var stillBefore int64
	if err = reader.QueryRow(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='cleanup'`, user).Scan(&stillBefore); err != nil || stillBefore != before {
		t.Fatalf("reader snapshot changed: %d %v", stillBefore, err)
	}
}

// Run explicitly with NONBIRI_POND_SCALE=1. The one private fixture is grown
// in place and removed by t.TempDir, avoiding large database copies.
func TestPondScaling(t *testing.T) {
	if os.Getenv("NONBIRI_POND_SCALE") != "1" {
		t.Skip("explicit scaling evidence only")
	}
	f := newGameFixture(t, nil)
	donor := f.seedUser("scale-donor", fixtureFunding)
	receiver := f.seedUser("scale-recipient", fixtureFunding)
	f.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: receiver, Bait: "worm", Count: 10, IdempotencyKey: validTestKey(980)})
	if err != nil || pending == nil {
		t.Fatalf("reserve: %#v %v", pending, err)
	}
	f.service.beforeSettlement = nil
	f.service.pondRandom = &scriptedSource{}
	var record batchRecord
	record, err = scanBatch(f.database.QueryRow(batchSelect, pending.BatchID))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1000, 100000} {
		seedStart := time.Now()
		_, err = f.database.Exec(`WITH RECURSIVE seq(n) AS (SELECT COALESCE(MAX(id),0)+1 FROM game_fishing_pond_queue UNION ALL SELECT n+1 FROM seq WHERE n<?)
INSERT INTO game_fishing_pond_queue(donor_user_id,source_batch_id,source_ordinal,species_key,donor_display_name,returned_at)
SELECT ?,printf('fb_%021dA',n),0,'boot','scale-donor',? FROM seq`, size, donor, fixtureNow)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("queue=%d generated in %s", size, time.Since(seedStart))
		var total, maximum time.Duration
		for iteration := 0; iteration < 5; iteration++ {
			tx, err := f.database.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			err = f.service.settlePondTx(context.Background(), tx, record, fixtureNow)
			elapsed := time.Since(started)
			tx.Rollback()
			if err != nil {
				t.Fatal(err)
			}
			total += elapsed
			maximum = max(maximum, elapsed)
		}
		t.Logf("queue=%d pond terminal writer occupancy mean=%s max=%s (10 gifts, rollback per observation)", size, total/5, maximum)
		if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != int64(size) {
			t.Fatal("counter drift")
		}
	}
	for _, test := range []struct{ name, sql string }{
		{"queue count", `SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`},
		{"queue heads", `SELECT id,donor_user_id,species_key,donor_display_name,donor_avatar_url FROM game_fishing_pond_queue ORDER BY id LIMIT 10`},
		{"window counts", `SELECT user_id,SUM(item_count) FROM game_fishing_pond_counts WHERE board='cleanup' AND occurred_at>1997408000 AND occurred_at<=2000000000 GROUP BY user_id`},
	} {
		rows, err := f.database.Query(`EXPLAIN QUERY PLAN ` + test.sql)
		if err != nil {
			t.Fatal(err)
		}
		var plans []string
		for rows.Next() {
			var a, b, c int
			var detail string
			if err = rows.Scan(&a, &b, &c, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			plans = append(plans, detail)
		}
		rows.Close()
		t.Logf("%s plan: %s", test.name, strings.Join(plans, " | "))
	}
	for _, size := range []int{100000, 1000000} {
		seedStart := time.Now()
		_, err = f.database.Exec(`WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<?)
INSERT OR IGNORE INTO game_fishing_pond_counts(user_id,board,occurred_at,item_count) SELECT ?,'cleanup',?-n,1 FROM seq`, size, donor, fixtureNow)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.database.Exec(`INSERT INTO game_fishing_pond_totals(user_id,board,item_count,achieved_at,public_tie_key) VALUES(?,'cleanup',?,?,zeroblob(32)) ON CONFLICT(user_id,board) DO UPDATE SET item_count=excluded.item_count`, donor, size, fixtureNow)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.database.Exec(`INSERT INTO game_fishing_pond_daily(user_id,board,day_start,item_count,last_occurred_at)
SELECT user_id,board,occurred_at/86400*86400,SUM(item_count),MAX(occurred_at) FROM game_fishing_pond_counts GROUP BY user_id,board,occurred_at/86400
ON CONFLICT(user_id,board,day_start) DO UPDATE SET item_count=excluded.item_count,last_occurred_at=excluded.last_occurred_at`)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("facts=%d generated in %s", size, time.Since(seedStart))
		for _, period := range []string{"7d", "30d", "all"} {
			var total, maximum time.Duration
			beforeWait := f.store.ReadDB().Stats().WaitDuration
			for iteration := 0; iteration < 3; iteration++ {
				start := time.Now()
				board, err := f.service.PondLeaderboard(context.Background(), donor, "cleanup", period)
				elapsed := time.Since(start)
				if err != nil || len(board.Entries) != 1 {
					t.Fatalf("facts%d/%s: %#v %v", size, period, board, err)
				}
				total += elapsed
				maximum = max(maximum, elapsed)
			}
			t.Logf("facts=%d period=%s read latency mean=%s max=%s readpool wait=%s", size, period, total/3, maximum, f.store.ReadDB().Stats().WaitDuration-beforeWait)
			window, _ := pondWindow(period, fixtureNow)
			if size == 1000000 && period == "30d" {
				planSQL, planArgs := pondScoresSQL("cleanup", window, fixtureNow, 0)
				rows, err := f.store.ReadDB().Query(`EXPLAIN QUERY PLAN `+planSQL, planArgs...)
				if err != nil {
					t.Fatal(err)
				}
				var plans []string
				for rows.Next() {
					var a, b, c int
					var detail string
					if err = rows.Scan(&a, &b, &c, &detail); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					plans = append(plans, detail)
				}
				rows.Close()
				t.Logf("final30d aggregate plan: %s", strings.Join(plans, " | "))
			}
			ownerSQL, ownerArgs := pondScoresSQL("cleanup", window, fixtureNow, donor)
			ownerStart := time.Now()
			var ownerCount int64
			if err = f.store.ReadDB().QueryRow(`SELECT COALESCE(SUM(item_count),0) FROM (`+ownerSQL+`)`, ownerArgs...).Scan(&ownerCount); err != nil {
				t.Fatal(err)
			}
			t.Logf("facts=%d period=%s export own score latency=%s count=%d", size, period, time.Since(ownerStart), ownerCount)
		}
	}
}
