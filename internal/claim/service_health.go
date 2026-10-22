package claim

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

// A new service event has its own cursor while retaining the original key
// reservation and sequence for accounting and completion ordering.
func reserveServiceHealthTx(ctx context.Context, tx *sql.Tx, claimID string, at int64) error {
	one, err := db.U128FromBig(big.NewInt(1))
	if err != nil {
		return ErrInvariant
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donation_service_health
(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,failure_disabled,next_claim_seq,next_fold_seq,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT use_id_snapshot,endpoint_key_id_snapshot,native_service_bit,credential_version_id,?,?,0,?,?,1,0,0,?
FROM dispatch_claim_targets WHERE claim_id=? AND authorization_kind='charity'
ON CONFLICT(use_id,endpoint_key_id,native_service_bit,credential_version_id) DO NOTHING`, db.EncodeU128(one), db.EncodeU128(db.U128{}), db.EncodeU128(one), db.EncodeU128(one), at, claimID)
	if err != nil {
		return err
	}
	var id int64
	var generation, sequence []byte
	var disabled bool
	var until int64
	err = tx.QueryRowContext(ctx, `SELECT h.id,h.streak_generation,h.next_claim_seq,h.failure_disabled,
`+smartbackoff.ServiceUntilSQL("h")+`
FROM dispatch_claim_targets t JOIN donation_service_health h ON h.use_id=t.use_id_snapshot
AND h.endpoint_key_id=t.endpoint_key_id_snapshot AND h.native_service_bit=t.native_service_bit AND h.credential_version_id=t.credential_version_id
WHERE t.claim_id=? AND t.authorization_kind='charity'`, claimID).Scan(&id, &generation, &sequence, &disabled, &until)
	if err != nil {
		return authorizationError(err)
	}
	if disabled {
		return ErrNotFound
	}
	if until > at {
		return ErrKeyRateLimited
	}
	if _, err := db.DecodeU128(generation); err != nil {
		return ErrInvariant
	}
	current, err := db.DecodeU128(sequence)
	if err != nil {
		return ErrInvariant
	}
	next, err := db.U128FromBig(new(big.Int).Add(current.Big(), big.NewInt(1)))
	if err != nil {
		return ErrInvariant
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_service_health SET next_claim_seq=?,updated_at=? WHERE id=? AND streak_generation=? AND next_claim_seq=?`, db.EncodeU128(next), at, id, generation, sequence)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dispatch_service_health(claim_id,health_id,health_id_snapshot,streak_generation,claim_seq,created_at) VALUES(?,?,?,?,?,?)`, claimID, id, id, generation, sequence, at)
	return err
}
