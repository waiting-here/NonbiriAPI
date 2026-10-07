package lakenotes

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
)

type castRow struct {
	id, period                 string
	user, generation, revision int64
	state                      rules.Cast
	elapsed                    int64
	started, lease             sql.NullInt64
}

const castColumns = "id,user_id,source_period_id,rules_id,storage_version,generation,revision,snapshot,reward_plan,phase,paused,last_tick,held,active_elapsed_ns,active_started_at_ns,lease_until_ns"

func scanCast(scan func(...any) error) (castRow, error) {
	var row castRow
	var snapshot, reward []byte
	var rulesID string
	var version int
	var phase string
	var paused, held bool
	var tick uint64
	e := scan(&row.id, &row.user, &row.period, &rulesID, &version, &row.generation, &row.revision, &snapshot, &reward, &phase, &paused, &tick, &held, &row.elapsed, &row.started, &row.lease)
	if errors.Is(e, sql.ErrNoRows) {
		return row, ErrNotFound
	}
	if e != nil {
		return row, e
	}
	row.state, e = decodeStoredCast(version, rulesID, snapshot)
	if e != nil {
		return row, ErrInvariant
	}
	if string(reward) != "{}" {
		if e = json.Unmarshal(reward, &row.state.Reward); e != nil {
			return row, ErrInvariant
		}
	}
	if row.state.Phase != phase || row.state.Tick != tick {
		return row, ErrInvariant
	}
	row.state.Paused, row.state.Held = paused, held
	if e = rules.ValidateCast(row.state); e != nil {
		return row, ErrInvariant
	}
	return row, nil
}
func castTx(ctx context.Context, tx *sql.Tx, user int64, id string) (castRow, error) {
	return scanCast(tx.QueryRowContext(ctx, "SELECT "+castColumns+" FROM lake_notes_casts WHERE id=? AND user_id=?", id, user).Scan)
}
func currentCastTx(ctx context.Context, tx *sql.Tx, user int64) (castRow, error) {
	return scanCast(tx.QueryRowContext(ctx, "SELECT "+castColumns+" FROM lake_notes_casts WHERE user_id=? AND phase IN ('waiting','playing')", user).Scan)
}
func (c castRow) view(profileRevision int64) CastView {
	action := ""
	if c.state.Paused && !c.state.Terminal() {
		action = "resume"
	}
	return CastView{ID: c.id, SourcePeriodID: c.period, RulesID: c.state.RulesID, Generation: rev(c.generation), Revision: rev(c.revision), AckTick: c.state.Tick, Phase: c.state.Phase, Paused: c.state.Paused, Readonly: c.state.Paused || c.state.Terminal(), RecoveryAction: action, State: c.state, ProfileRevision: rev(profileRevision)}
}
func (c *castRow) accrue(now int64) error {
	if c.state.Paused || !c.started.Valid {
		return nil
	}
	end := min(now, c.lease.Int64)
	if end > c.started.Int64 {
		delta := end - c.started.Int64
		if c.elapsed > math.MaxInt64-delta {
			return ErrCapacity
		}
		c.elapsed += delta
	}
	c.started = sql.NullInt64{Int64: now, Valid: true}
	return nil
}
func (c *castRow) stop(now int64) error {
	if e := c.accrue(now); e != nil {
		return e
	}
	c.state.Paused, c.state.Held = true, false
	c.started, c.lease = sql.NullInt64{}, sql.NullInt64{}
	return nil
}
func saveCastTx(ctx context.Context, tx *sql.Tx, c *castRow, now time.Time, ack any) error {
	if c.revision == math.MaxInt64 {
		return ErrCapacity
	}
	state := c.state
	state.Reward = nil

	raw, e := rules.EncodeCast(state)
	if e != nil {
		return e
	}
	reward := []byte("{}")
	if c.state.Reward != nil {
		reward, e = json.Marshal(c.state.Reward)
		if e != nil {
			return e
		}
	}
	last, e := json.Marshal(ack)
	if e != nil {
		return e
	}
	var terminal any
	if c.state.Terminal() {
		terminal = now.Unix()
	}
	r, e := tx.ExecContext(ctx, "UPDATE lake_notes_casts SET rules_id=?,storage_version=?,phase=?,paused=?,generation=?,revision=revision+1,last_tick=?,snapshot=?,reward_plan=?,held=?,active_elapsed_ns=?,active_started_at_ns=?,lease_until_ns=?,last_ack_json=?,terminal_at=?,updated_at=? WHERE id=? AND user_id=? AND revision=?", rules.RulesID, castStorageVersion, c.state.Phase, c.state.Paused, c.generation, c.state.Tick, raw, reward, c.state.Held, c.elapsed, c.started, c.lease, string(last), terminal, now.Unix(), c.id, c.user, c.revision)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	c.revision++
	return nil
}
func (s *Service) castResultTx(ctx context.Context, tx *sql.Tx, c castRow, now time.Time) (CastResult, error) {
	p, e := s.profileViewTx(ctx, tx, c.user, now)
	if e != nil {
		return CastResult{}, e
	}
	r, _ := revision(p.Revision, false)
	v := c.view(r)
	if p.Readonly && !c.state.Terminal() {
		v.Readonly = true
		v.RecoveryAction = "closed"
	}
	return CastResult{Cast: v, Profile: p}, nil
}
func (s *Service) capacityTx(ctx context.Context, tx *sql.Tx, now int64, except string) error {
	var count int
	e := tx.QueryRowContext(ctx, "SELECT count(*) FROM lake_notes_casts WHERE paused=0 AND lease_until_ns>? AND id<>?", now, except).Scan(&count)
	if e != nil {
		return e
	}
	if count >= MaxLeases {
		return ErrCapacity
	}
	return nil
}
func (s *Service) Start(ctx context.Context, user int64, key string, in StartInput) (MutationResult[CastResult], error) {
	expected, e := revision(in.ExpectedProfileRevision, false)
	if e != nil {
		return MutationResult[CastResult]{}, e
	}
	result, e := mutate(ctx, s, user, false, key, "POST", baseRoute+"/casts", in, func(tx *sql.Tx, now time.Time) (CastResult, error) {
		p, e := s.qualifiedTx(ctx, tx, user, now.Unix())
		if e != nil {
			return CastResult{}, e
		}
		if e = s.expireUserTx(ctx, tx, user, now); e != nil {
			return CastResult{}, e
		}
		c, e := currentCastTx(ctx, tx, user)
		if e == nil {
			return s.castResultTx(ctx, tx, c, now)
		}
		if !errors.Is(e, ErrNotFound) {
			return CastResult{}, e
		}
		if e = s.capacityTx(ctx, tx, now.UnixNano(), ""); e != nil {
			return CastResult{}, e
		}
		row, e := profileTx(ctx, tx, user, true, now.Unix())
		if e != nil {
			return CastResult{}, e
		}
		if row.revision != expected {
			return CastResult{}, ErrConflict
		}
		seed, e := s.seed()
		if e != nil {
			return CastResult{}, e
		}
		next, state, e := rules.Start(row.profile, s.random, seed)
		if e != nil {
			return CastResult{}, e
		}
		row.profile = next
		if e = saveProfileTx(ctx, tx, user, &row, now.Unix()); e != nil {
			return CastResult{}, e
		}
		id, e := db.GenerateOpaqueID("lnc_")
		if e != nil {
			return CastResult{}, e
		}
		deadline, e := s.leaseDeadlineTx(ctx, tx, p, now)
		if e != nil {
			return CastResult{}, e
		}
		raw, e := rules.EncodeCast(state)
		if e != nil {
			return CastResult{}, e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO lake_notes_casts(id,user_id,source_period_id,rules_id,storage_version,phase,paused,generation,revision,last_tick,snapshot,reward_plan,held,active_elapsed_ns,active_started_at_ns,lease_until_ns,created_at,updated_at) VALUES(?,?,?,?,?,?,0,1,1,0,?,X'7b7d',0,0,?,?,?,?)", id, user, p.ID, rules.RulesID, castStorageVersion, state.Phase, raw, now.UnixNano(), deadline, now.Unix(), now.Unix())
		if e != nil {
			return CastResult{}, e
		}
		c, e = castTx(ctx, tx, user, id)
		if e != nil {
			return CastResult{}, e
		}
		return s.castResultTx(ctx, tx, c, now)
	})
	if e == nil && result.Replayed {
		result.Value, e = s.Cast(ctx, user, result.Value.Cast.ID)
	}
	return result, e
}

func (s *Service) Cast(ctx context.Context, user int64, id string) (CastResult, error) {
	if !db.ValidateOpaqueID(id, "lnc_") {
		return CastResult{}, ErrInvalid
	}
	now, e := s.clock()
	if e != nil {
		return CastResult{}, e
	}
	tx, e := s.database.BeginTx(ctx, nil)
	if e != nil {
		return CastResult{}, e
	}
	defer tx.Rollback()
	if e = s.authorize(ctx, tx, user, false); e != nil {
		return CastResult{}, e
	}
	if e = s.expireUserTx(ctx, tx, user, now); e != nil {
		return CastResult{}, e
	}
	c, e := castTx(ctx, tx, user, id)
	if e != nil {
		return CastResult{}, e
	}
	out, e := s.castResultTx(ctx, tx, c, now)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func decodeEdges(c castRow, in CheckpointInput) ([]bool, error) {
	if in.FromTick != c.state.Tick+1 || in.ToTick < in.FromTick || in.ToTick > rules.MaxSafeInteger || in.ToTick-in.FromTick >= MaxCheckpointTicks || len(in.Edges) > 120 || in.InitialHeld != c.state.Held {
		return nil, ErrConflict
	}
	held := in.InitialHeld
	last := uint64(0)
	for _, edge := range in.Edges {
		if edge.Tick < in.FromTick || edge.Tick > in.ToTick || edge.Tick <= last || edge.Held == held {
			return nil, ErrInvalid
		}
		last, held = edge.Tick, edge.Held
	}
	states := make([]bool, in.ToTick-in.FromTick+1)
	held = in.InitialHeld
	edge := 0
	for tick := in.FromTick; tick <= in.ToTick; tick++ {
		if edge < len(in.Edges) && in.Edges[edge].Tick == tick {
			held = in.Edges[edge].Held
			edge++
		}
		states[tick-in.FromTick] = held
	}
	return states, nil
}
func (s *Service) budget(user int64, now time.Time) error {
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	second := now.Unix()
	if s.budgetSecond != second {
		s.budgetSecond = second
		s.budgetTotal = 0
		clear(s.budgetUsers)
	}
	if s.budgetTotal >= 256 || s.budgetUsers[user] >= 4 {
		return ErrCapacity
	}
	s.budgetTotal++
	s.budgetUsers[user]++
	return nil
}
func (s *Service) Checkpoint(ctx context.Context, user int64, id, key string, in CheckpointInput) (MutationResult[CastResult], error) {
	generation, e := revision(in.Generation, false)
	if e != nil {
		return MutationResult[CastResult]{}, e
	}
	expected, e := revision(in.ExpectedRevision, false)
	if e != nil || !db.ValidateOpaqueID(id, "lnc_") {
		return MutationResult[CastResult]{}, ErrInvalid
	}
	now, e := s.clock()
	if e != nil {
		return MutationResult[CastResult]{}, e
	}
	if e = s.budget(user, now); e != nil {
		return MutationResult[CastResult]{}, e
	}
	result, e := mutate(ctx, s, user, false, key, "POST", baseRoute+"/casts/"+id+"/checkpoint", in, func(tx *sql.Tx, now time.Time) (CastResult, error) {
		c, e := castTx(ctx, tx, user, id)
		if e != nil {
			return CastResult{}, e
		}
		if c.generation != generation || c.revision != expected {
			return CastResult{}, ErrConflict
		}
		if c.state.Terminal() {
			return CastResult{}, ErrConflict
		}
		period, gateErr := s.qualifiedTx(ctx, tx, user, now.Unix())
		if gateErr != nil && !closedError(gateErr) {
			return CastResult{}, gateErr
		}
		expired := !c.state.Paused && c.lease.Valid && now.UnixNano() >= c.lease.Int64
		if gateErr != nil || expired {
			if !c.state.Paused {
				if e = c.stop(now.UnixNano()); e != nil {
					return CastResult{}, e
				}
				if e = saveCastTx(ctx, tx, &c, now, map[string]any{}); e != nil {
					return CastResult{}, e
				}
			}
			out, e := s.castResultTx(ctx, tx, c, now)
			if gateErr != nil {
				out.Cast.RecoveryAction = "closed"
			}
			return out, e
		}
		if c.state.Paused {
			return CastResult{}, ErrConflict
		}
		states, e := decodeEdges(c, in)
		if e != nil {
			return CastResult{}, e
		}
		if e = c.accrue(now.UnixNano()); e != nil {
			return CastResult{}, e
		}
		budget := uint64(c.elapsed/int64(time.Second))*60 + uint64(c.elapsed%int64(time.Second))*60/uint64(time.Second) + 120
		if in.ToTick > budget {
			return CastResult{}, ErrConflict
		}
		row, e := profileTx(ctx, tx, user, false, now.Unix())
		if e != nil {
			return CastResult{}, e
		}
		row.profile, c.state, e = rules.Advance(row.profile, c.state, states)
		if e != nil {
			return CastResult{}, e
		}
		if c.state.Phase == "success" && c.state.Treasure != nil && c.state.Treasure.Secured && c.state.Reward == nil {
			row.profile, c.state, e = rules.ResolveTreasure(row.profile, c.state, s.random)
			if e != nil {
				return CastResult{}, e
			}
		}
		if c.state.Terminal() {
			if e = c.stop(now.UnixNano()); e != nil {
				return CastResult{}, e
			}
		} else {
			c.started = sql.NullInt64{Int64: now.UnixNano(), Valid: true}
			deadline, e := s.leaseDeadlineTx(ctx, tx, period, now)
			if e != nil {
				return CastResult{}, e
			}
			c.lease = sql.NullInt64{Int64: deadline, Valid: true}
		}
		if e = saveProfileTx(ctx, tx, user, &row, now.Unix()); e != nil {
			return CastResult{}, e
		}
		raw, _ := json.Marshal(in)
		digest := sha256.Sum256(raw)
		ack := map[string]any{"from_tick": in.FromTick, "ack_tick": c.state.Tick, "input_digest": hex.EncodeToString(digest[:])}
		if e = saveCastTx(ctx, tx, &c, now, ack); e != nil {
			return CastResult{}, e
		}
		return s.castResultTx(ctx, tx, c, now)
	})
	if e == nil && result.Replayed {
		current, readErr := s.Cast(ctx, user, id)
		if readErr != nil {
			return result, readErr
		}
		if current.Cast.Generation != in.Generation {
			return result, ErrConflict
		}
		if current.Cast.Readonly && !result.Value.Cast.State.Terminal() {
			result.Value.Cast.Readonly = true
			result.Value.Cast.Paused = current.Cast.Paused
			result.Value.Cast.State.Paused = current.Cast.Paused
			result.Value.Cast.State.Held = false
			result.Value.Cast.RecoveryAction = current.Cast.RecoveryAction
			result.Value.Profile.Readonly = current.Profile.Readonly
		}
	}
	return result, e
}

func (s *Service) control(ctx context.Context, user int64, id, key string, in ControlInput, resume bool) (MutationResult[CastResult], error) {
	gen, e := revision(in.Generation, false)
	if e != nil {
		return MutationResult[CastResult]{}, e
	}
	expected, e := revision(in.ExpectedRevision, false)
	if e != nil || !db.ValidateOpaqueID(id, "lnc_") {
		return MutationResult[CastResult]{}, ErrInvalid
	}
	suffix := "/pause"
	if resume {
		suffix = "/resume"
	}
	result, e := mutate(ctx, s, user, false, key, "POST", baseRoute+"/casts/"+id+suffix, in, func(tx *sql.Tx, now time.Time) (CastResult, error) {
		c, e := castTx(ctx, tx, user, id)
		if e != nil {
			return CastResult{}, e
		}
		if c.state.Terminal() {
			if _, e = tx.ExecContext(ctx, "UPDATE lake_notes_casts SET updated_at=? WHERE id=? AND user_id=?", now.Unix(), id, user); e != nil {
				return CastResult{}, e
			}
			return s.castResultTx(ctx, tx, c, now)
		}
		if c.revision != expected || c.generation != gen {
			return CastResult{}, ErrConflict
		}
		var period Period
		if resume {
			if period, e = s.qualifiedTx(ctx, tx, user, now.Unix()); e != nil {
				return CastResult{}, e
			}
			if c.generation == math.MaxInt64 {
				return CastResult{}, ErrCapacity
			}
			if e = s.capacityTx(ctx, tx, now.UnixNano(), id); e != nil {
				return CastResult{}, e
			}
		}
		if e = c.stop(now.UnixNano()); e != nil {
			return CastResult{}, e
		}
		if resume {
			c.generation++
			c.state.Paused = false
			c.started = sql.NullInt64{Int64: now.UnixNano(), Valid: true}
			deadline, e := s.leaseDeadlineTx(ctx, tx, period, now)
			if e != nil {
				return CastResult{}, e
			}
			c.lease = sql.NullInt64{Int64: deadline, Valid: true}
		}
		if e = saveCastTx(ctx, tx, &c, now, map[string]any{}); e != nil {
			return CastResult{}, e
		}
		return s.castResultTx(ctx, tx, c, now)
	})
	if e == nil && result.Replayed {
		result.Value, e = s.Cast(ctx, user, id)
	}
	return result, e
}
func (s *Service) Pause(ctx context.Context, user int64, id, key string, in ControlInput) (MutationResult[CastResult], error) {
	return s.control(ctx, user, id, key, in, false)
}
func (s *Service) Resume(ctx context.Context, user int64, id, key string, in ControlInput) (MutationResult[CastResult], error) {
	return s.control(ctx, user, id, key, in, true)
}

// A control lease never extends beyond the authoritative open interval.
func (s *Service) leaseDeadlineTx(ctx context.Context, tx *sql.Tx, p Period, now time.Time) (int64, error) {
	deadline := now.Add(LeaseDuration).UnixNano()
	periodEnd := time.Unix(p.EndsAt, 0)
	if periodEnd.Before(now.Add(LeaseDuration)) {
		deadline = periodEnd.UnixNano()
	}
	var end sql.NullInt64
	if e := tx.QueryRowContext(ctx, "SELECT ends_at FROM limited_activity_configs WHERE activity_key=?", Key).Scan(&end); e != nil {
		return 0, e
	}
	if end.Valid && time.Unix(end.Int64, 0).Before(time.Unix(0, deadline)) {
		deadline = time.Unix(end.Int64, 0).UnixNano()
	}
	return deadline, nil
}
