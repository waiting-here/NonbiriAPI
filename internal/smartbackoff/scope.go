package smartbackoff

import (
	"context"
	"database/sql"
	"errors"
)

// The receipt fixes the state machine's scope. A service marker alone cannot
// distinguish a new service probe from a probe granted by a legacy gate.
type stateScope struct{ keyID, healthID int64 }

func readScope(ctx context.Context, q Querier, scope stateScope) (stored, error) {
	var s stored
	var period, source sql.NullString
	query := `SELECT generation,episode,period,source,stage,retry_at,trial_claim_id FROM donation_key_backoffs WHERE donation_key_id=?`
	args := []any{scope.keyID}
	if scope.healthID > 0 {
		query = `SELECT h.backoff_generation,h.backoff_episode,h.backoff_period,h.backoff_source,h.backoff_stage,h.backoff_retry_at,h.trial_claim_id
FROM donation_service_health h JOIN donation_uses u ON u.id=h.use_id WHERE h.id=? AND u.donation_key_id=?`
		args = []any{scope.healthID, scope.keyID}
	}
	err := q.QueryRowContext(ctx, query, args...).Scan(&s.Generation, &s.Episode, &period, &source, &s.Stage, &s.RetryAt, &s.Trial)
	if errors.Is(err, sql.ErrNoRows) {
		return stored{}, nil
	}
	s.Period, s.Source = period.String, source.String
	return s, err
}

func writeScope(ctx context.Context, tx *sql.Tx, scope stateScope, before, after stored, now int64) (bool, error) {
	var period, source any
	if after.Period != "" {
		period, source = after.Period, after.Source
	}
	query := `UPDATE donation_key_backoffs SET episode=?,period=?,source=?,stage=?,retry_at=?,trial_claim_id=?,updated_at=? WHERE donation_key_id=? AND generation=? AND episode=? AND trial_claim_id IS ?`
	id := scope.keyID
	if scope.healthID > 0 {
		query = `UPDATE donation_service_health SET backoff_episode=?,backoff_period=?,backoff_source=?,backoff_stage=?,backoff_retry_at=?,trial_claim_id=?,updated_at=? WHERE id=? AND backoff_generation=? AND backoff_episode=? AND trial_claim_id IS ?`
		id = scope.healthID
	}
	result, err := tx.ExecContext(ctx, query, after.Episode, period, source, after.Stage, after.RetryAt, after.Trial, now, id, before.Generation, before.Episode, before.Trial)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func prepareScope(ctx context.Context, tx *sql.Tx, keyID int64, claimID string, now int64) (stateScope, stored, error) {
	legacy := stateScope{keyID: keyID}
	previous, err := readScope(ctx, tx, legacy)
	if err != nil {
		return legacy, previous, err
	}
	if previous.RetryAt.Valid && (previous.RetryAt.Int64 > now || previous.Trial.Valid) {
		return legacy, previous, ErrUnavailable
	}
	var health sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT health_id FROM dispatch_service_health WHERE claim_id=?`, claimID).Scan(&health)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return legacy, previous, err
	}
	if err == nil {
		if !health.Valid {
			return legacy, previous, ErrUnavailable
		}
		service := stateScope{keyID: keyID, healthID: health.Int64}
		current, err := readScope(ctx, tx, service)
		if err != nil {
			return service, current, err
		}
		if current.Generation <= 0 || (current.RetryAt.Valid && (current.RetryAt.Int64 > now || current.Trial.Valid)) {
			return service, current, ErrUnavailable
		}
		// A proven legacy probe retains its original receipt scope. Its service
		// event is still folded independently through dispatch_service_health.
		if !previous.RetryAt.Valid {
			return service, current, nil
		}
	}
	if previous.Generation == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO donation_key_backoffs(donation_key_id,generation,episode,stage,updated_at) VALUES(?,1,0,0,?) ON CONFLICT(donation_key_id) DO NOTHING`, keyID, now); err != nil {
			return legacy, previous, err
		}
		previous, err = readScope(ctx, tx, legacy)
	}
	return legacy, previous, err
}

type dispatchFact struct {
	scope               stateScope
	key                 sql.NullInt64
	generation, episode int64
	trial               bool
	accepted, reset     sql.NullInt64
	period, source      sql.NullString
}

func readFact(ctx context.Context, tx *sql.Tx, claimID string) (dispatchFact, error) {
	var f dispatchFact
	var health sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT donation_key_id,health_id_snapshot,generation,episode,is_trial,accepted_at,period,source,reset_at FROM dispatch_smart_backoff_facts WHERE claim_id=?`, claimID).Scan(&f.key, &health, &f.generation, &f.episode, &f.trial, &f.accepted, &f.period, &f.source, &f.reset)
	f.scope = stateScope{keyID: f.key.Int64, healthID: health.Int64}
	return f, err
}

// ServiceUntilSQL uses the same waiting/probing semantics as the legacy gate.
// The alias is supplied only by repository-owned SQL, never request data.
func ServiceUntilSQL(alias string) string {
	return `max(COALESCE(` + alias + `.retry_after_until,0),CASE WHEN ` + alias + `.trial_claim_id IS NOT NULL THEN 253402300799 ELSE COALESCE(` + alias + `.backoff_retry_at,0) END)`
}

func ObserveRetryAfter(ctx context.Context, tx *sql.Tx, claimID string, until, now int64) error {
	f, err := readFact(ctx, tx, claimID)
	if err != nil {
		return err
	}
	if !f.key.Valid {
		return nil
	}
	if f.scope.healthID > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE donation_service_health SET retry_after_until=max(COALESCE(retry_after_until,0),?),updated_at=max(updated_at,?)
WHERE id=? AND EXISTS(SELECT 1 FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE u.id=donation_service_health.use_id AND dk.id=? AND dk.ended_at IS NULL)`, until, now, f.scope.healthID, f.key.Int64)
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donation_key_cooldowns(donation_key_id,rate_limited_until,updated_at)
SELECT id,?,? FROM donation_keys WHERE id=? AND ended_at IS NULL
ON CONFLICT(donation_key_id) DO UPDATE SET rate_limited_until=max(donation_key_cooldowns.rate_limited_until,excluded.rate_limited_until),updated_at=max(donation_key_cooldowns.updated_at,excluded.updated_at)`, until, now, f.key.Int64)
	return err
}
