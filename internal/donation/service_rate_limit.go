package donation

import (
	"context"
	"database/sql"
)

// Advancing the backoff generation fences observations from requests dispatched
// before release. Failure folding keeps its independent generation and counters.
func releaseUseRateLimitTx(ctx context.Context, tx *sql.Tx, useID, now int64) error {
	var donationID, keyID int64
	if err := tx.QueryRowContext(ctx, `SELECT dk.donation_id,dk.id FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE u.id=?`, useID).Scan(&donationID, &keyID); err != nil {
		return err
	}
	states, err := readServiceResetStatesTx(ctx, tx, donationID, keyID, useID, now)
	if err != nil {
		return err
	}
	if len(states) != 1 || states[0].receipt.Reason != "" {
		return ErrConflict
	}
	if states[0].id != 0 {
		result, err := tx.ExecContext(ctx, `UPDATE donation_service_health SET retry_after_until=NULL,
backoff_generation=backoff_generation+1,backoff_episode=0,backoff_period=NULL,backoff_source=NULL,backoff_stage=0,backoff_retry_at=NULL,trial_claim_id=NULL,updated_at=?
WHERE id=? AND backoff_generation<9223372036854775807`, now, states[0].id)
		if err != nil {
			return err
		}
		if err := requireOne(result); err != nil {
			return err
		}
	}
	// Legacy key waits apply to every service on the key. Keep a generation
	// tombstone so a late legacy observation cannot recreate a cleared wait.
	result, err := tx.ExecContext(ctx, `INSERT INTO donation_key_backoffs(donation_key_id,generation,episode,stage,updated_at)
VALUES(?,1,0,0,?) ON CONFLICT(donation_key_id) DO UPDATE SET generation=generation+1,episode=0,
period=NULL,source=NULL,stage=0,retry_at=NULL,trial_claim_id=NULL,updated_at=excluded.updated_at
WHERE generation<9223372036854775807`, keyID, now)
	if err != nil {
		return err
	}
	if err := requireOne(result); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM donation_key_cooldowns WHERE donation_key_id=?", keyID)
	return err
}
