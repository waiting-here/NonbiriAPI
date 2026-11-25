// Package companion hosts free, resumable two-seat games against a pinned program.
package companion

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/companion/program"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

var (
	errInput    = errors.New("invalid game input")
	errConflict = errors.New("game decision has changed")
	errDisabled = errors.New("game unavailable")
)

type preferences struct {
	Memory     string `json:"memory"`
	Contribute bool   `json:"contribute"`
}
type summary struct {
	ID         string `json:"id"`
	Game       string `json:"game"`
	Mode       string `json:"mode"`
	Program    string `json:"program"`
	Version    int64  `json:"version"`
	Difficulty int    `json:"difficulty"`
	Seat       int    `json:"seat"`
	Created    int64  `json:"created_at"`
	Ended      int64  `json:"ended_at"`
	result
}
type match struct {
	startReservation *game.StartReservation
	summary
	User      int64
	ProgramID int64
	Official  bool
	AIState   string
	Frame     frame
	Record    record
}
type view struct {
	summary
	AIState  string `json:"ai_state"`
	Phase    string `json:"phase"`
	Round    int    `json:"round"`
	Decision string `json:"decision_id"`
	View     any    `json:"view"`
	Space    space  `json:"space"`
}

func (m *match) decision(seat int) string {
	return fmt.Sprintf("%s/%d/%d", m.ID, seat, len(m.Record.Actions))
}
func (m *match) view(seat int) (view, error) {
	r, err := rulesFor(m.Game, m.Mode)
	if err != nil {
		return view{}, err
	}
	t, err := r.observe(m.Frame, &m.Record, seat, m.Status != "active")
	if err != nil {
		return view{}, err
	}
	v := view{m.summary, m.AIState, t.Phase, t.Round, "", t.View, t.Space}
	if m.Status == "active" && t.Need[seat] && m.Frame.Locked[seat] == nil {
		v.Decision = m.decision(seat)
	} else {
		v.Space.Examples = []Raw{}
	}
	return v, nil
}
func (m *match) apply(r rules, seat int, a Raw) error {
	t, err := r.observe(m.Frame, &m.Record, seat, false)
	if err != nil {
		return err
	}
	if err := r.validate(m.Frame, &m.Record, seat, t, a); err != nil {
		return err
	}
	m.Frame.Locked[seat] = a
	for {
		for i, need := range t.Need {
			if need && m.Frame.Locked[i] == nil {
				return nil
			}
		}
		if len(m.Record.Actions) >= 4096 {
			return errors.New("game exceeded its window limit")
		}
		if err := r.advance(&m.Frame, &m.Record, t); err != nil {
			return err
		}
		if m.Record.Result.Status == "completed" {
			m.result = m.Record.Result
			return nil
		}
		t, err = r.observe(m.Frame, &m.Record, seat, false)
		if err != nil {
			return err
		}
	}
}
func (m *match) update(ctx context.Context, tx *sql.Tx, now time.Time) error {
	ai := "idle"
	if m.Status == "active" {
		r, err := rulesFor(m.Game, m.Mode)
		if err != nil {
			return err
		}
		seat := 1 - m.Seat
		t, err := r.observe(m.Frame, &m.Record, seat, false)
		if err != nil {
			return err
		}
		if t.Need[seat] {
			switch {
			case m.Frame.Locked[seat] != nil:
				ai = "locked"
			case m.AIState == "thinking":
				ai = "thinking"
			default:
				ai = "queued"
			}
		}
	} else {
		m.Ended = now.Unix()
		m.Record.Result = m.result
	}
	_, err := tx.ExecContext(ctx, `UPDATE companion_matches SET position=?,record=?,status=?,ended_at=?,reason=?,winner=?,score0=?,score1=?,ai_state=?,queued_at=CASE WHEN ?='queued' AND ai_state!='queued' THEN ? ELSE queued_at END WHERE id=? AND status='active'`, string(encode(m.Frame)), string(encode(m.Record)), m.Status, m.Ended, m.Reason, m.Winner, m.Scores[0], m.Scores[1], ai, ai, now.UnixMilli(), m.ID)
	m.AIState = ai
	if err == nil && m.Status != "active" {
		_, err = tx.ExecContext(ctx, `DELETE FROM companion_history_snapshot WHERE match_id=?`, m.ID)
	}
	return err
}

const summaryCols = "id,game,mode,program,version,difficulty,seat,created_at,ended_at,status,reason,winner,score0,score1"

func (s *summary) destinations() []any {
	return []any{&s.ID, &s.Game, &s.Mode, &s.Program, &s.Version, &s.Difficulty, &s.Seat, &s.Created, &s.Ended, &s.Status, &s.Reason, &s.Winner, &s.Scores[0], &s.Scores[1]}
}
func readMatch(ctx context.Context, tx *sql.Tx, id, game string, user int64) (match, error) {
	var m match
	var pos, rec string
	dest := append(m.destinations(), &m.User, &m.Official, &m.ProgramID, &m.AIState, &pos, &rec)
	err := tx.QueryRowContext(ctx, "SELECT "+summaryCols+",user_id,official,program_id,ai_state,position,record FROM companion_matches WHERE id=? AND game=? AND user_id=?", id, game, user).Scan(dest...)
	if err == nil {
		err = json.Unmarshal([]byte(pos), &m.Frame)
	}
	if err == nil {
		err = json.Unmarshal([]byte(rec), &m.Record)
	}
	return m, err
}
func getPrefs(ctx context.Context, tx *sql.Tx, user int64, game string) (preferences, error) {
	p := preferences{Memory: "none"}
	err := tx.QueryRowContext(ctx, `SELECT memory,contribute FROM companion_preferences WHERE user_id=? AND game=?`, user, game).Scan(&p.Memory, &p.Contribute)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return p, err
}

type start struct {
	Mode        string `json:"mode"`
	Program     string `json:"program"`
	Difficulty  int    `json:"difficulty"`
	Constraints Raw    `json:"constraints"`
	Setup       Raw    `json:"setup"`
}

func token(n int) string { b := make([]byte, n); rand.Read(b); return hex.EncodeToString(b) }
func (s *Service) create(ctx context.Context, tx *sql.Tx, user int64, game, key string, in start) (match, error) {
	var m match
	if _, err := idempotency.KeyHash(key); err != nil {
		return m, errInput
	}
	bodyHash := digest(encode(in))
	var id, oldHash string
	err := tx.QueryRowContext(ctx, `SELECT id,start_hash FROM companion_matches WHERE user_id=? AND game=? AND start_key=?`, user, game, key).Scan(&id, &oldHash)
	if err == nil {
		if bodyHash != oldHash {
			return m, errConflict
		}
		return readMatch(ctx, tx, id, game, user)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return m, err
	}
	r, err := rulesFor(game, in.Mode)
	if err != nil {
		return m, err
	}
	var enabled, occupied, eligible, maintenance bool
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM site_config WHERE key IN ('games_enabled',?) AND value='1')=2,(EXISTS(SELECT 1 FROM companion_matches WHERE user_id=? AND game=? AND status='active') OR EXISTS(SELECT 1 FROM game_duel_user_slots WHERE user_id=? AND game_key=?)), EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0 AND discord_id IS NOT NULL AND discord_id<>'' AND (is_banned=0 OR banned_until<=?)), (SELECT enabled FROM maintenance_state WHERE id=1)`, "game_"+game+"_enabled", user, game, user, game, user, s.shared.Now().Unix()).Scan(&enabled, &occupied, &eligible, &maintenance); err != nil {
		return m, err
	}
	if !eligible {
		return m, resources.ErrForbidden
	}
	if maintenance {
		return m, resources.ErrMaintenance
	}
	if !enabled {
		return m, errDisabled
	}
	if occupied {
		return m, errConflict
	}
	b, err := s.programs.ResolveTx(ctx, tx, user, game, in.Program, in.Difficulty)
	if err != nil {
		return m, err
	}
	if len(in.Constraints) == 0 {
		in.Constraints = Raw("{}")
	}
	if in.Constraints[0] != '{' {
		return m, errInput
	}
	if game == "bidding" && (strict(in.Constraints, &struct{}{}) != nil || len(in.Setup) > 0) {
		return m, errInput
	}
	p, err := getPrefs(ctx, tx, user, game)
	if err != nil {
		return m, err
	}
	now := s.shared.Now()
	var uuid [16]byte
	rand.Read(uuid[:])
	prefix := map[string]string{"bidding": "bidc_", "likes": "likc_", "gwent": "gwtc_"}[game]
	m.ID = prefix + base64.RawURLEncoding.EncodeToString(uuid[:])
	m.Game = game
	m.Mode = in.Mode
	m.Program = b.Name
	m.Difficulty = b.Difficulty
	m.Version = b.Version
	m.User = user
	m.Official = b.Official
	m.ProgramID = b.ProgramID
	var side [1]byte
	rand.Read(side[:])
	m.Seat = int(side[0] & 1)
	m.Created = now.Unix()
	m.Status = "active"
	m.AIState = "idle"
	m.Record = record{Initial: initial{Game: game, Mode: in.Mode, Rules: r.hash, Human: m.Seat, Constraints: in.Constraints, Program: b.Name, Version: b.Version, Difficulty: b.Difficulty}, Chance: token(32), Actions: []window{}, Result: m.result}
	m.Frame, err = r.begin(m.Record)
	if err != nil {
		return m, err
	}
	if _, err = r.observe(m.Frame, &m.Record, 1-m.Seat, false); err != nil {
		return m, err
	}
	if len(in.Setup) > 0 {
		if err = m.apply(r, m.Seat, in.Setup); err != nil {
			return m, err
		}
	}
	m.startReservation, _, err = s.shared.Limiter.ReserveTx(ctx, tx, user)
	if err != nil {
		return m, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO companion_matches(id,user_id,game,mode,program,program_id,version,difficulty,official,seat,rules_hash,memory,contribute,start_key,start_hash,created_at,status,ai_state,position,record) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, m.ID, user, game, in.Mode, b.Name, b.ProgramID, b.Version, b.Difficulty, b.Official, m.Seat, r.hash, p.Memory, p.Contribute, key, bodyHash, m.Created, "active", "idle", "{}", "{}")
	if err != nil {
		return m, err
	}
	if err = s.snapshot(ctx, tx, &m, p); err != nil {
		return m, err
	}
	err = m.update(ctx, tx, now)
	return m, err
}
func (m *match) binding() program.Binding {
	b := program.Binding{Game: m.Game, Name: m.Program, Version: m.Version, Difficulty: m.Difficulty, Official: m.Official}
	if !m.Official {
		b.OwnerID = m.User
		b.ProgramID = m.ProgramID
	}
	return b
}
