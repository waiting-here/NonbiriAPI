package smartbackoff

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

type Querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type stored struct {
	Generation, Episode int64
	Period, Source      string
	Stage               int
	RetryAt             sql.NullInt64
	Trial               sql.NullString
}

func read(ctx context.Context, q Querier, keyID int64) (stored, error) {
	s := stored{}
	var period, source sql.NullString
	err := q.QueryRowContext(ctx, `SELECT generation,episode,period,source,stage,retry_at,trial_claim_id FROM donation_key_backoffs WHERE donation_key_id=?`, keyID).Scan(&s.Generation, &s.Episode, &period, &source, &s.Stage, &s.RetryAt, &s.Trial)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.Period, s.Source = period.String, source.String
	return s, err
}
func Read(ctx context.Context, q Querier, keyID, now int64) (Projection, error) {
	s, err := read(ctx, q, keyID)
	return project(s, now), err
}
func project(s stored, now int64) Projection {
	p := Projection{}
	if s.RetryAt.Valid {
		state := "ready"
		if s.RetryAt.Int64 > now {
			state = "waiting"
		}
		if s.Trial.Valid {
			state = "probing"
		}
		p.Backoff = &Status{State: state, Reason: Classification{Period: s.Period}.Reason(), Source: s.Source, RetryAt: s.RetryAt.Int64}
	}
	return p
}
func DecodeProjection(period, source string, retryAt *int64, probing bool, now int64) Projection {
	s := stored{Period: period, Source: source}
	if retryAt != nil {
		s.RetryAt = sql.NullInt64{Int64: *retryAt, Valid: true}
	}
	s.Trial.Valid = probing
	return project(s, now)
}

type Dispatch struct {
	Trial bool
}

func Prepare(ctx context.Context, tx *sql.Tx, keyID int64, claimID string, now int64) (Dispatch, error) {
	// A key without previous configuration still needs a stable generation for
	// every dispatch fact and its eventual completion.
	if _, err := tx.ExecContext(ctx, `INSERT INTO donation_key_backoffs(donation_key_id,generation,episode,stage,updated_at)
 VALUES(?,1,0,0,?) ON CONFLICT(donation_key_id) DO NOTHING`, keyID, now); err != nil {
		return Dispatch{}, err
	}
	s, err := read(ctx, tx, keyID)
	if err != nil {
		return Dispatch{}, err
	}
	if s.RetryAt.Valid && (s.RetryAt.Int64 > now || s.Trial.Valid) {
		return Dispatch{}, ErrUnavailable
	}
	trial := s.RetryAt.Valid
	_, err = tx.ExecContext(ctx, `INSERT INTO dispatch_smart_backoff_facts(claim_id,donation_key_id,generation,episode,is_trial) VALUES(?,?,?,?,?)`, claimID, keyID, s.Generation, s.Episode, trial)
	if err != nil {
		return Dispatch{}, err
	}
	if trial {
		result, err := tx.ExecContext(ctx, `UPDATE donation_key_backoffs SET trial_claim_id=?,updated_at=? WHERE donation_key_id=? AND generation=? AND episode=? AND trial_claim_id IS NULL AND retry_at<=?`, claimID, now, keyID, s.Generation, s.Episode, now)
		if err != nil {
			return Dispatch{}, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return Dispatch{}, err
		}
		if n != 1 {
			return Dispatch{}, ErrUnavailable
		}
	}
	return Dispatch{Trial: trial}, nil
}

// Accepted records HTTP acceptance independently of accounting and completion.
func Accepted(ctx context.Context, tx *sql.Tx, claimID string, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE dispatch_smart_backoff_facts SET accepted_at=COALESCE(accepted_at,?) WHERE claim_id=? AND is_trial=1`, now, claimID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE donation_key_backoffs SET period=NULL,source=NULL,stage=0,retry_at=NULL,trial_claim_id=NULL,updated_at=?
 WHERE trial_claim_id=? AND EXISTS(SELECT 1 FROM dispatch_smart_backoff_facts f JOIN donation_keys dk ON dk.id=f.donation_key_id
 WHERE f.claim_id=? AND f.is_trial=1 AND dk.ended_at IS NULL AND f.donation_key_id=donation_key_backoffs.donation_key_id
 AND f.generation=donation_key_backoffs.generation AND f.episode=donation_key_backoffs.episode)`, now, claimID, claimID)
	return err
}
func IsTrial(ctx context.Context, tx *sql.Tx, claimID string) (bool, error) {
	var trial bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch_smart_backoff_facts WHERE claim_id=? AND is_trial=1)`, claimID).Scan(&trial)
	return trial, err
}

// RecordClassification stores only built-in classifications from a charity
// dispatch. Historical custom_rule facts remain readable during recovery.
func RecordClassification(ctx context.Context, tx *sql.Tx, claimID string, c Classification) (bool, error) {
	if !c.Valid() || c.Source != "builtin" {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE dispatch_smart_backoff_facts SET period=?,source=?,reset_at=? WHERE claim_id=? AND accepted_at IS NULL AND (period IS NULL OR (period=? AND source=? AND reset_at=?))`, c.Period, c.Source, c.ResetAt, claimID, c.Period, c.Source, c.ResetAt)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func Delay(period string, stage int) int64 {
	if period == "week" {
		return 6 * 3600
	}
	if period == "month" {
		return 12 * 3600
	}
	if stage <= 0 {
		return 15 * 60
	}
	if stage == 1 {
		return 30 * 60
	}
	return 3600
}
func Complete(ctx context.Context, tx *sql.Tx, claimID string, status int, now int64) error {
	var key sql.NullInt64
	var generation, episode int64
	var trial bool
	var accepted, reset sql.NullInt64
	var period, source sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT donation_key_id,generation,episode,is_trial,accepted_at,period,source,reset_at FROM dispatch_smart_backoff_facts WHERE claim_id=?`, claimID).Scan(&key, &generation, &episode, &trial, &accepted, &period, &source, &reset)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !key.Valid || accepted.Valid || (!trial && (!period.Valid || status == 200)) {
		return nil
	}
	var ended bool
	if err = tx.QueryRowContext(ctx, `SELECT ended_at IS NOT NULL FROM donation_keys WHERE id=?`, key.Int64).Scan(&ended); errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if ended {
		return nil
	}
	s, err := read(ctx, tx, key.Int64)
	if err != nil {
		return err
	}
	if s.Generation != generation || s.Episode != episode {
		return nil
	}
	if trial && (!s.Trial.Valid || s.Trial.String != claimID) {
		return nil
	}
	if !trial && s.RetryAt.Valid {
		return nil
	}
	if status == 200 {
		if trial {
			return Accepted(ctx, tx, claimID, now)
		}
		return nil
	}
	var header bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch_retry_after_observations WHERE claim_id=?)`, claimID).Scan(&header); err != nil {
		return err
	}
	if header {
		if trial {
			_, err = tx.ExecContext(ctx, `UPDATE donation_key_backoffs SET trial_claim_id=NULL,updated_at=? WHERE donation_key_id=? AND trial_claim_id=?`, now, key.Int64, claimID)
		}
		return err
	}
	if !period.Valid && !trial {
		return nil
	}
	nextPeriod, nextSource, stage := s.Period, s.Source, s.Stage
	nextEpisode := s.Episode
	if period.Valid {
		nextPeriod, nextSource = period.String, source.String
		if trial && s.Period != "week" && s.Period != "month" && nextPeriod != "week" && nextPeriod != "month" {
			stage = min(2, stage+1)
		} else {
			stage = 0
		}
	}
	if !trial {
		if nextEpisode == math.MaxInt64 {
			return ErrInvalid
		}
		nextEpisode++
	}
	until := min(int64(253402300799), now+Delay(nextPeriod, stage))
	if period.Valid && reset.Valid && reset.Int64 > now {
		until = reset.Int64
	}
	_, err = tx.ExecContext(ctx, `UPDATE donation_key_backoffs SET episode=?,period=?,source=?,stage=?,retry_at=?,trial_claim_id=NULL,updated_at=? WHERE donation_key_id=? AND generation=? AND episode=?`, nextEpisode, nextPeriod, nextSource, stage, until, now, key.Int64, generation, episode)
	return err
}
func EffectiveUntilSQL(key string) string {
	return `max(COALESCE((SELECT rate_limited_until FROM donation_key_cooldowns WHERE donation_key_id=` + key + `),0),
 COALESCE((SELECT CASE WHEN trial_claim_id IS NOT NULL THEN 253402300799 ELSE retry_at END FROM donation_key_backoffs WHERE donation_key_id=` + key + `),0))`
}
