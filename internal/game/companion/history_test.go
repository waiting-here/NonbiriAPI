package companion

import (
	"strconv"
	"testing"
	"time"
)

func TestHistoryCountAndSharedByteQuota(t *testing.T) {
	s := fixture(t)
	ctx := t.Context()
	db := s.shared.Database
	m := createGame(t, s, 1, "bidding", "official", token(20))
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Small synthetic bodies exercise the production indexes and counting limits without allocating a 128 MiB fixture.
	_, err = tx.Exec("ALTER TABLE companion_matches DROP COLUMN record_bytes")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec("ALTER TABLE companion_matches ADD COLUMN record_bytes INTEGER NOT NULL DEFAULT 1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec("DELETE FROM companion_matches WHERE id=?", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	st, err := tx.Prepare(`INSERT INTO companion_matches(id,user_id,game,mode,program,program_id,version,difficulty,official,seat,rules_hash,memory,contribute,start_key,start_hash,created_at,ended_at,status,ai_state,position,record,record_bytes)
 VALUES(?,?,'bidding','standard','official',0,1,1,1,0,?,'none',1,?,'h',?,?,'completed','idle','{}','{}',?)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for i := range 1110 {
		id := strconv.Itoa(i)
		owner := int64(2)
		if i < 105 {
			owner = 1
		}
		if _, err = st.Exec(id, owner, m.Record.Initial.Rules, id, now-2000, now-1200+int64(i), 1); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	if _, err = tx.Exec("INSERT INTO companion_preferences VALUES(1,'bidding','shared',1)"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	capture := func() (match, map[string]source) {
		m := createGame(t, s, 1, "bidding", "official", token(20))
		src, err := s.historySources(ctx, &m)
		if err != nil {
			t.Fatal(err)
		}
		return m, src
	}
	m, src := capture()
	if src["personal"].Count != 100 || src["global"].Count != 1000 {
		t.Fatal(src)
	}
	var n int
	if err = db.QueryRow("SELECT COUNT(*) FROM companion_history_snapshot WHERE match_id=?", m.ID).Scan(&n); err != nil || n != 1100 {
		t.Fatal(n, err)
	}
	if _, err = db.Exec("DELETE FROM companion_matches WHERE id=?", m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE companion_matches SET record_bytes=?", historyBudget/100); err != nil {
		t.Fatal(err)
	}
	_, src = capture()
	if src["personal"].Count != 100 || src["global"].Count != 0 || src["global"].Skipped != 1000 {
		t.Fatal(src)
	}
}
