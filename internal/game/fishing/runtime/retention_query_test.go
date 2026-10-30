package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIdleTerminalBatchCleanupDoesNotAcquireWriter(t *testing.T) {
	f := newGameFixture(t, nil)
	held, err := f.database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := f.database.Stats().WaitCount
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if n, err := f.service.cleanupTerminalBatches(ctx, fixtureNow); err != nil || n != 0 {
		t.Fatalf("idle cleanup: %d, %v", n, err)
	}
	if f.database.Stats().WaitCount != before || f.service.readDatabase.Stats().InUse != 0 {
		t.Fatal("idle cleanup waited for a writer or retained a reader")
	}
}

func TestTerminalBatchCleanupUsesTimeIndexAndBatches(t *testing.T) {
	f := newGameFixture(t, nil)
	user := f.seedUser("retention-range", fixtureFunding)
	seed := func(first, count int, settled int64, terminal bool) {
		t.Helper()
		tx, err := f.database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for i := first; i < first+count; i++ {
			id := fmt.Sprintf("fb_%021dA", i)
			_, err := tx.Exec(`INSERT INTO game_fishing_batches(id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,request_hash,state,ledger_rows_remaining,next_attempt_at,created_at)
VALUES(?,?,'worm',1,0,0,0,?,zeroblob(32),'reserved',x'00000000000000000000000000000001',?,?)`, id, user, fmt.Sprintf("op_%021dA", i), settled, settled)
			if err != nil {
				t.Fatal(err)
			}
			if terminal {
				if _, err := tx.Exec(`UPDATE game_fishing_batches SET state='released',settled_at=?,next_attempt_at=NULL,ledger_rows_remaining=zeroblob(16) WHERE id=?`, settled, id); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	seed(0, 2000, fixtureNow-1, true)
	if n, err := f.service.cleanupTerminalBatches(context.Background(), fixtureNow); err != nil || n != 0 {
		t.Fatalf("no due batches: %d, %v", n, err)
	}
	rows, err := f.database.Query(`EXPLAIN QUERY PLAN `+terminalBatchCandidatesSQL, fixtureNow-int64(rankWindow.Seconds()), workerBatchSize)
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
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(plan, "; ")
	t.Log(text)
	if !strings.Contains(text, "COVERING INDEX idx_fishing_retention (settled_at<?)") || strings.Contains(text, "TEMP B-TREE") || strings.Contains(text, "idx_fishing_due") {
		t.Fatalf("unbounded retention plan: %s", text)
	}
	seed(2000, 201, fixtureNow-int64(rankWindow.Seconds()), true)
	seed(3000, 1, fixtureNow-int64(rankWindow.Seconds())-1, false)
	for _, want := range []int{100, 100, 1, 0} {
		if n, err := f.service.cleanupTerminalBatches(context.Background(), fixtureNow); err != nil || n != want {
			t.Fatalf("batch: %d, %v; want %d", n, err, want)
		}
	}
	if n := f.scalar(`SELECT COUNT(*) FROM game_fishing_batches`); n != 2001 {
		t.Fatalf("remaining=%d, want recent history plus reserved batch", n)
	}
	if n := f.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE state='reserved' AND settled_at IS NULL`); n != 1 {
		t.Fatal("reserved batch was removed")
	}
}
