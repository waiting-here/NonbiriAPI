package lakenotes

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCapacityCountsOnlyLiveLeases(t *testing.T) {
	f := newFixture(t)
	now := f.now.Load()
	f.tx(t, func(tx *sql.Tx) {
		type cast struct {
			phase    string
			paused   bool
			lease    *int64
			terminal *int64
		}
		future, expired, terminal := now+1, now-1, int64(testNow)
		casts := []cast{
			{"waiting", false, &future, nil},
			{"playing", false, &future, nil},
			{"waiting", false, &now, nil},
			{"playing", false, &expired, nil},
			{"waiting", true, nil, nil},
			{"playing", true, nil, nil},
			{"success", true, nil, &terminal},
			{"failed", true, nil, &terminal},
		}
		for i, c := range casts {
			result, err := tx.Exec(`INSERT INTO users(donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,?)`, testNow, testNow)
			if err != nil {
				t.Fatal(err)
			}
			user, err := result.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			var started any
			if !c.paused {
				started = now - 2
			}
			// Capacity depends on these constrained columns, not the snapshot payload.
			_, err = tx.Exec(`INSERT INTO lake_notes_casts(id,user_id,rules_id,phase,paused,generation,revision,last_tick,snapshot,reward_plan,held,active_elapsed_ns,active_started_at_ns,lease_until_ns,created_at,updated_at,terminal_at)
VALUES(?,?,'capacity-test',?,?,1,1,0,X'7b7d',X'7b7d',0,0,?,?,?,?,?)`,
				fmt.Sprintf("lnc_%021dA", i), user, c.phase, c.paused, started, c.lease, testNow, testNow, c.terminal)
			if err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct {
			name   string
			now    int64
			except string
			want   int
		}{
			{"live", now, "", 2},
			{"exclude waiting", now, fmt.Sprintf("lnc_%021dA", 0), 1},
			{"exclude playing", now, fmt.Sprintf("lnc_%021dA", 1), 1},
			{"exclude paused", now, fmt.Sprintf("lnc_%021dA", 4), 2},
			{"exclude terminal", now, fmt.Sprintf("lnc_%021dA", 6), 2},
			{"before expiry", now - 2, "", 4},
			{"at last expiry", future, "", 0},
		} {
			var got, previous int
			if err := tx.QueryRow(liveLeaseCountSQL, tc.now, tc.except).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRow("SELECT count(*) FROM lake_notes_casts WHERE paused=0 AND lease_until_ns>? AND id<>?", tc.now, tc.except).Scan(&previous); err != nil {
				t.Fatal(err)
			}
			if got != tc.want || previous != got {
				t.Errorf("%s: count=%d previous=%d want=%d", tc.name, got, previous, tc.want)
			}
		}
		if _, err := tx.Exec("UPDATE lake_notes_casts SET paused=0,active_started_at_ns=?,lease_until_ns=? WHERE phase='success'", now, future); err == nil {
			t.Fatal("terminal cast accepted an active lease")
		}
	})
}

func TestCapacityQueryExcludesTerminalHistory(t *testing.T) {
	f := newFixture(t)
	rows, err := f.database.Query("EXPLAIN QUERY PLAN "+liveLeaseCountSQL, f.now.Load(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var steps []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if plan := strings.Join(steps, " | "); !strings.Contains(plan, "idx_lake_notes_active_cast_user") {
		t.Fatalf("capacity query must exclude retained terminal history: %s", plan)
	}
}

func TestStartAndResumeRespectLeaseCapacity(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	profile := f.profile(t)
	started, err := f.service.Start(f.ctx(f.user), f.user, testKey(600), StartInput{profile.Revision})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := f.service.Pause(f.ctx(f.user), f.user, started.Value.Cast.ID, testKey(601), control(started.Value.Cast))
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.service.Profile(f.ctx(f.other), f.other)
	if err != nil {
		t.Fatal(err)
	}
	now := f.now.Load()
	f.tx(t, func(tx *sql.Tx) {
		_, err := tx.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
INSERT INTO users(username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
SELECT 'capacity',zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,? FROM n`, MaxLeases, testNow, testNow)
		if err != nil {
			t.Fatal(err)
		}
		// Clone a real waiting cast; each lease belongs to a distinct user.
		result, err := tx.Exec(`INSERT INTO lake_notes_casts(id,user_id,rules_id,storage_version,phase,paused,generation,revision,last_tick,snapshot,reward_plan,held,active_elapsed_ns,active_started_at_ns,lease_until_ns,created_at,updated_at)
SELECT printf('lnc_%021dA',u.id),u.id,c.rules_id,c.storage_version,c.phase,0,1,1,c.last_tick,c.snapshot,c.reward_plan,0,0,?,?,?,?
FROM users u CROSS JOIN lake_notes_casts c WHERE u.username='capacity' AND c.id=?`, now, now+int64(LeaseDuration), testNow, testNow, paused.Value.Cast.ID)
		if err != nil {
			t.Fatal(err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != MaxLeases {
			t.Fatalf("seeded %d leases: %v", count, err)
		}
	})
	startKey, resumeKey := testKey(602), testKey(603)
	if _, err := f.service.Start(f.ctx(f.other), f.other, startKey, StartInput{other.Revision}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("start at capacity: %v", err)
	}
	if _, err := f.service.Resume(f.ctx(f.user), f.user, paused.Value.Cast.ID, resumeKey, control(paused.Value.Cast)); !errors.Is(err, ErrCapacity) {
		t.Fatalf("resume at capacity: %v", err)
	}
	unchanged, err := f.service.Cast(f.ctx(f.user), f.user, paused.Value.Cast.ID)
	if err != nil || unchanged.Cast.Revision != paused.Value.Cast.Revision || !unchanged.Cast.Paused {
		t.Fatalf("rejected resume changed the cast: %+v, %v", unchanged.Cast, err)
	}
	var leaseID string
	if err := f.database.QueryRow("SELECT id FROM lake_notes_casts WHERE user_id IN (SELECT id FROM users WHERE username='capacity') ORDER BY id LIMIT 1").Scan(&leaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.Exec("UPDATE lake_notes_casts SET lease_until_ns=? WHERE id=?", now, leaseID); err != nil {
		t.Fatal(err)
	}
	accepted, err := f.service.Start(f.ctx(f.other), f.other, startKey, StartInput{other.Revision})
	if err != nil || accepted.Replayed {
		t.Fatalf("start after lease expiry: %+v, %v", accepted, err)
	}
	renewed, err := f.service.Resume(f.ctx(f.other), f.other, accepted.Value.Cast.ID, testKey(604), control(accepted.Value.Cast))
	if err != nil || renewed.Value.Cast.Generation != "2" {
		t.Fatalf("resume must exclude its own live lease: %+v, %v", renewed, err)
	}
	if _, err := f.database.Exec("UPDATE lake_notes_casts SET paused=1,active_started_at_ns=NULL,lease_until_ns=NULL WHERE id=(SELECT id FROM lake_notes_casts WHERE user_id IN (SELECT id FROM users WHERE username='capacity') AND lease_until_ns>? ORDER BY id LIMIT 1)", now); err != nil {
		t.Fatal(err)
	}
	resumed, err := f.service.Resume(f.ctx(f.user), f.user, paused.Value.Cast.ID, resumeKey, control(paused.Value.Cast))
	if err != nil || resumed.Replayed || resumed.Value.Cast.Paused {
		t.Fatalf("resume after capacity is released: %+v, %v", resumed, err)
	}
	var live int
	if err := f.database.QueryRow(liveLeaseCountSQL, now, "").Scan(&live); err != nil || live != MaxLeases {
		t.Fatalf("final live leases=%d want=%d: %v", live, MaxLeases, err)
	}
}
