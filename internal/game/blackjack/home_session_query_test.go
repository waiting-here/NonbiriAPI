package blackjack

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestHomeSessionChoosesNewestRoundOrOpenTable(t *testing.T) {
	store, _, _ := historyQueryFixture(t, 10)
	ctx := t.Context()
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	check := func(round, want int64) {
		t.Helper()
		table, err := scanSession(tx.QueryRowContext(ctx, homeSessionSQL, round))
		if want == -1 && errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil || table.StartedAt != want {
			t.Fatalf("round %d: start=%d err=%v, want %d", round, table.StartedAt, err, want)
		}
	}
	check(330, -1)
	check(300, 300)
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_blackjack_sessions(id,started_at,phase,revision,last_batch)
VALUES('bjt_000000000000000000011A',330,'seating',1,330)`); err != nil {
		t.Fatal(err)
	}
	check(360, 330)
	check(330, 330)
	check(300, 330)
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_blackjack_sessions(id,started_at,phase,revision,last_batch,view_json,terminal_at,reason)
VALUES('bjt_000000000000000000012A',360,'cancelled',1,360,'{}',361,'closed')`); err != nil {
		t.Fatal(err)
	}
	check(360, 360)

	rows, err := tx.QueryContext(ctx, "EXPLAIN QUERY PLAN "+homeSessionSQL, 390)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		if strings.HasPrefix(detail, "SCAN game_blackjack_sessions") && !strings.Contains(detail, "idx_blackjack_single_table") {
			t.Fatalf("home state scans retained tables: %s", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkHomeSessionLookup(b *testing.B) {
	for _, tables := range []int{1000, 50000} {
		b.Run(fmt.Sprintf("tables=%d", tables), func(b *testing.B) {
			store, _, _ := historyQueryFixture(b, tables)
			ctx := b.Context()
			tx, err := store.DB().BeginTx(ctx, nil)
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			for _, tc := range []struct {
				name  string
				round int
				found bool
			}{{"idle", (tables + 1) * 30, false}, {"current", tables * 30, true}} {
				b.Run(tc.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						table, err := scanSession(tx.QueryRowContext(ctx, homeSessionSQL, tc.round))
						if tc.found && (err != nil || table.StartedAt != int64(tc.round)) || !tc.found && !errors.Is(err, sql.ErrNoRows) {
							b.Fatalf("round=%d start=%d err=%v", tc.round, table.StartedAt, err)
						}
					}
				})
			}
		})
	}
}
