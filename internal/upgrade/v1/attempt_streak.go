package v1

import (
	"context"
	"database/sql"
)

// A service marker retains its own sequence after reservation cleanup. The
// committed claim and attempt were persisted with the same classified outcome.
// A physical key's sequence survives only on its reservation.
func backfillAttemptStreaks(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `WITH cursors AS (
 SELECT c.id AS claim_id,l.id AS log_id,c.attempt_seq,c.streak_disposition,c.failure_origin,
 CASE WHEN m.claim_id IS NULL THEN r.claim_seq ELSE m.claim_seq END AS seq,
 CASE WHEN m.claim_id IS NOT NULL AND u.donation_key_id=k.id THEN h.next_fold_seq
      WHEN m.claim_id IS NULL AND f.use_id_snapshot IS NULL AND r.streak_generation=k.streak_generation
           AND r.claim_seq<k.next_claim_seq THEN k.next_fold_seq END AS cursor,
 (r.claim_id IS NOT NULL AND (m.claim_id IS NULL OR k.ended_at IS NULL)) AS can_fold
 FROM dispatch_claims c JOIN request_logs l ON l.logical_request_id=c.logical_request_id
 LEFT JOIN donation_usage_reservations r ON r.claim_id=c.id
 LEFT JOIN donation_keys k ON k.id=c.donation_key_id
 LEFT JOIN dispatch_service_health m ON m.claim_id=c.id
 LEFT JOIN donation_service_health h ON h.id=m.health_id AND h.id=m.health_id_snapshot
      AND h.streak_generation=m.streak_generation AND m.claim_seq<h.next_claim_seq
 LEFT JOIN donation_uses u ON u.id=h.use_id
 LEFT JOIN request_attempt_service_facts f ON f.claim_id=c.id
 WHERE c.purpose='charity' AND c.state='committed' AND c.dispatched_at IS NOT NULL AND l.route_kind GLOB 'charity_*'
 AND (r.claim_id IS NULL OR (r.state='committed' AND r.donation_key_id IS c.donation_key_id
      AND r.streak_disposition=c.streak_disposition AND r.failure_origin=c.failure_origin))
), effects AS (
 SELECT claim_id,log_id,attempt_seq,CASE
 WHEN seq>=cursor AND can_fold THEN 'pending'
 WHEN seq<cursor AND streak_disposition='success' THEN 'reset'
 WHEN seq<cursor AND streak_disposition='upstream_failure' THEN 'incremented'
 -- Early migrations labeled unclassified outcomes neutral/legacy_unknown
 -- without undoing increments already made by the old reducer.
 WHEN streak_disposition='neutral' AND failure_origin<>'legacy_unknown' THEN 'unchanged'
 END AS effect FROM cursors
)
UPDATE request_attempts AS a SET streak_effect=e.effect FROM effects e
WHERE a.claim_id=e.claim_id AND a.request_log_id=e.log_id AND a.attempt_seq=e.attempt_seq
AND a.streak_effect IS NULL AND e.effect IS NOT NULL`)
	return err
}
