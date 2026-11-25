package companion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
)

const retention int64 = 30 * 24 * 60 * 60
const historyBudget = 128 << 20

type entry struct {
	ended            int64
	id               string
	bytes            int
	personal, global bool
}

// Capture only indexed identifiers and sizes at creation. Bodies stay in the original record and are loaded individually on demand.
func (s *Service) snapshot(ctx context.Context, tx *sql.Tx, m *match, p preferences) error {
	entries := make([]entry, 0)
	seen := map[string]int{}
	size := 0
	var personalSkipped, globalSkipped int
	selectSource := func(global bool) error {
		q := "SELECT id,record_bytes,ended_at FROM companion_matches WHERE status='completed' AND game=? AND ended_at>?"
		args := []any{m.Game, m.Created - retention}
		if global {
			args = append(args, m.Mode, m.Record.Initial.Rules)
			q += " AND mode=? AND rules_hash=? AND official=1 AND contribute=1 ORDER BY ended_at DESC,id DESC LIMIT 1000"
		} else {
			q += " AND user_id=?"
			args = append(args, m.User)
			if p.Memory == "opponent" {
				q += " AND program_id=? AND difficulty=?"
				args = append(args, m.ProgramID, m.Difficulty)
			}
			q += " ORDER BY ended_at DESC,id DESC LIMIT 100"
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var n int
			var ended int64
			if err = rows.Scan(&id, &n, &ended); err != nil {
				return err
			}
			if i, ok := seen[id]; ok {
				entries[i].global = true
				continue
			}
			if size+n > historyBudget {
				if global {
					globalSkipped++
				} else {
					personalSkipped++
				}
				continue
			}
			seen[id] = len(entries)
			entries = append(entries, entry{ended: ended, id: id, bytes: n, personal: !global, global: global})
			size += n
		}
		return rows.Err()
	}
	if p.Memory != "none" {
		if err := selectSource(false); err != nil {
			return err
		}
	}
	if m.Official {
		if err := selectSource(true); err != nil {
			return err
		}
	}
	if personalSkipped+globalSkipped > 0 {
		if _, err := tx.ExecContext(ctx, "UPDATE companion_matches SET personal_skipped=?,global_skipped=? WHERE id=?", personalSkipped, globalSkipped, m.ID); err != nil {
			return err
		}
	}
	if len(entries) == 0 {
		return nil
	}
	// Allocate the shared byte quota to personal records first, then list each authorized source in chronological order.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ended != entries[j].ended {
			return entries[i].ended > entries[j].ended
		}
		return entries[i].id > entries[j].id
	})
	st, err := tx.PrepareContext(ctx, `INSERT INTO companion_history_snapshot(match_id,record_id,ordinal,personal,global_source,bytes) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for i, e := range entries {
		if _, err = st.ExecContext(ctx, m.ID, e.id, i+1, e.personal, e.global, e.bytes); err != nil {
			return err
		}
	}
	return nil
}

type source struct {
	Snapshot string `json:"snapshot"`
	Count    int    `json:"count"`
	Skipped  int    `json:"skipped_by_budget,omitempty"`
}

func (s *Service) historySources(ctx context.Context, m *match) (map[string]source, error) {
	var mode string
	var personal, global, personalSkipped, globalSkipped int
	err := s.shared.ReadDatabase.QueryRowContext(ctx, `SELECT m.memory,m.personal_skipped,m.global_skipped,COALESCE(SUM(h.personal),0),COALESCE(SUM(h.global_source),0) FROM companion_matches m LEFT JOIN companion_history_snapshot h ON h.match_id=m.id WHERE m.id=? AND m.status='active' GROUP BY m.id`, m.ID).Scan(&mode, &personalSkipped, &globalSkipped, &personal, &global)
	if err != nil {
		return nil, err
	}
	out := map[string]source{}
	add := func(kind string, n, skipped int) { out[kind] = source{digest([]byte(m.ID + "/" + kind)), n, skipped} }
	if mode != "none" {
		add("personal", personal, personalSkipped)
	}
	if m.Official {
		add("global", global, globalSkipped)
		add("aggregate", global, globalSkipped)
	}
	return out, nil
}
func replayAt(ctx context.Context, h record, step int) (frame, record, error) {
	if step < 0 || step > len(h.Actions) {
		return frame{}, record{}, errInput
	}
	r, err := rulesFor(h.Initial.Game, h.Initial.Mode)
	if err != nil || r.hash != h.Initial.Rules {
		return frame{}, record{}, errors.New("incompatible_rules")
	}
	history := h
	history.Result = result{Status: "active"}
	history.Actions = []window{}
	f, err := r.begin(h)
	if err != nil {
		return f, history, err
	}
	for _, w := range h.Actions[:step] {
		if err := ctx.Err(); err != nil {
			return f, history, err
		}
		t, err := r.observe(f, &history, 0, false)
		if err != nil {
			return f, history, err
		}
		if w.Phase != t.Phase {
			return f, history, errors.New("incompatible_rules")
		}
		f.Locked = w.Actions
		if err = r.advance(&f, &history, t); err != nil {
			return f, history, err
		}
	}
	if step == len(h.Actions) {
		history.Result = h.Result
	}
	return f, history, nil
}
func (s *Service) historyReader(m *match, sources map[string]source) func(context.Context, string, Raw) (Raw, error) {
	return func(ctx context.Context, op string, args Raw) (Raw, error) {
		var a struct {
			Snapshot string `json:"snapshot"`
			Record   string `json:"record"`
			Cursor   string `json:"cursor"`
			Limit    int    `json:"limit"`
			Step     int    `json:"step"`
			Seat     int    `json:"seat"`
		}
		if strict(args, &a) != nil {
			return nil, errInput
		}
		kind := ""
		for k, v := range sources {
			if a.Snapshot == v.Snapshot {
				kind = k
			}
		}
		if kind == "" || (kind == "aggregate") != (op == "aggregate") {
			return nil, errors.New("expired")
		}
		col := "h.personal"
		if kind != "personal" {
			col = "h.global_source"
		}
		from := " FROM companion_history_snapshot h JOIN companion_matches r ON r.id=h.record_id WHERE h.match_id=? AND " + col + "=1 AND r.ended_at>?"
		now := s.shared.Now().Unix()
		db := s.shared.ReadDatabase
		var active bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM companion_matches WHERE id=? AND status='active')", m.ID).Scan(&active); err != nil {
			return nil, err
		}
		if !active {
			return nil, errors.New("expired")
		}
		switch op {
		case "list":
			cursor := 0
			var err error
			if a.Cursor != "" {
				cursor, err = strconv.Atoi(a.Cursor)
				if err != nil || cursor < 0 {
					return nil, errInput
				}
			}
			if a.Limit == 0 {
				a.Limit = 20
			}
			if a.Limit < 1 || a.Limit > 100 {
				return nil, errInput
			}
			rows, err := db.QueryContext(ctx, "SELECT h.ordinal,r.game,r.mode,r.rules_hash,h.bytes"+from+" AND h.ordinal>? ORDER BY h.ordinal LIMIT ?", m.ID, now-retention, cursor, a.Limit+1)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			items := []Obj{}
			next := ""
			last := ""
			for rows.Next() {
				var i, n int
				var g, mode, hash string
				if err = rows.Scan(&i, &g, &mode, &hash, &n); err != nil {
					return nil, err
				}
				if len(items) == a.Limit {
					next = last
					break
				}
				last = strconv.Itoa(i)
				items = append(items, Obj{"record": last, "game": g, "mode": mode, "rules_hash": hash, "bytes": n})
			}
			if err = rows.Err(); err != nil {
				return nil, err
			}
			return encode(Obj{"items": items, "next": next}), nil
		case "aggregate":
			var n, wins, draws int
			err := db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(r.winner=r.seat),0),COALESCE(SUM(r.winner IS NULL),0)"+from, m.ID, now-retention).Scan(&n, &wins, &draws)
			if err != nil {
				return nil, err
			}
			return encode(Obj{"rules_hash": m.Record.Initial.Rules, "sample_count": n, "metrics": Obj{"human_wins": wins, "draws": draws}}), nil
		case "read", "view_at":
			i, err := strconv.Atoi(a.Record)
			if err != nil || i < 1 {
				return nil, errInput
			}
			var body string
			err = db.QueryRowContext(ctx, "SELECT r.record"+from+" AND h.ordinal=?", m.ID, now-retention, i).Scan(&body)
			if errors.Is(err, sql.ErrNoRows) {
				if sources[kind].Skipped > 0 {
					var last sql.NullInt64
					if err := db.QueryRowContext(ctx, "SELECT MAX(h.ordinal)"+from, m.ID, now-retention).Scan(&last); err != nil {
						return nil, err
					}
					if last.Valid && i > int(last.Int64) {
						return nil, errors.New("budget_exhausted")
					}
				}
				return nil, errors.New("expired")
			}
			if err != nil {
				return nil, err
			}
			if op == "read" {
				return Raw(body), nil
			}
			if a.Seat != 0 && a.Seat != 1 {
				return nil, errInput
			}
			var h record
			if err = json.Unmarshal([]byte(body), &h); err != nil {
				return nil, err
			}
			f, rewound, err := replayAt(ctx, h, a.Step)
			if err != nil {
				return nil, err
			}
			r, err := rulesFor(h.Initial.Game, h.Initial.Mode)
			if err != nil {
				return nil, err
			}
			t, err := r.observe(f, &rewound, a.Seat, rewound.Result.Status != "active")
			if err != nil {
				return nil, err
			}
			return encode(Obj{"phase": t.Phase, "view": t.View}), nil
		}
		return nil, errInput
	}
}
