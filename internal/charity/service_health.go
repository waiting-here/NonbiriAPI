package charity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func foldFailureOutcome(streak db.U128, disabled int, threshold db.U128, state string, success sql.NullInt64, disposition, origin sql.NullString, apply bool) (db.U128, int, string, error) {
	if !disposition.Valid || !origin.Valid || !contract.ValidOutcome(contract.StreakDisposition(disposition.String), contract.FailureOrigin(origin.String)) || (state != "committed" && state != "released") {
		return streak, disabled, "", claim.ErrInvariant
	}
	if disposition.String != "success" && disposition.String != "upstream_failure" {
		return streak, disabled, "unchanged", nil
	}
	if state != "committed" || !success.Valid || (disposition.String == "upstream_failure" && success.Int64 != 0) {
		return streak, disabled, "", claim.ErrInvariant
	}
	if !apply {
		return streak, disabled, "", nil
	}
	if disposition.String == "success" {
		return db.U128{}, disabled, "reset", nil
	}
	next, err := addU128(streak, big.NewInt(1))
	if err != nil {
		return streak, disabled, "", err
	}
	if disabled == 0 && threshold != (db.U128{}) && next.Big().Cmp(threshold.Big()) >= 0 {
		disabled = 1
	}
	return next, disabled, "incremented", nil
}

func foldServiceStreak(ctx context.Context, tx *sql.Tx, claimID string, at int64) error {
	var healthID, useID, donationID, keyID, endpointKeyID int64
	var currentGeneration, eventGeneration, nextBlob, streakBlob []byte
	var disabled int
	var thresholdText string
	err := tx.QueryRowContext(ctx, `SELECT h.id,h.use_id,dk.donation_id,dk.id,h.endpoint_key_id,h.streak_generation,m.streak_generation,h.next_fold_seq,h.failure_streak,h.failure_disabled,dk.failure_disable_threshold
FROM dispatch_service_health m JOIN donation_service_health h ON h.id=m.health_id
JOIN donation_uses u ON u.id=h.use_id JOIN donation_keys dk ON dk.id=u.donation_key_id
WHERE m.claim_id=? AND dk.ended_at IS NULL`, claimID).Scan(&healthID, &useID, &donationID, &keyID, &endpointKeyID, &currentGeneration, &eventGeneration, &nextBlob, &streakBlob, &disabled, &thresholdText)
	if errors.Is(err, sql.ErrNoRows) {
		// A detached service marker or ended member can no longer fold.
		// Unmarked reservations still belong to their physical key's cursor.
		_, err = tx.ExecContext(ctx, `UPDATE request_attempts SET streak_effect='unchanged'
WHERE claim_id=? AND streak_effect='pending' AND (
 EXISTS(SELECT 1 FROM dispatch_service_health m WHERE m.claim_id=request_attempts.claim_id)
 OR NOT EXISTS(SELECT 1 FROM donation_usage_reservations r JOIN donation_keys k ON k.id=r.donation_key_id
 WHERE r.claim_id=request_attempts.claim_id AND r.streak_generation=k.streak_generation))`, claimID)
		return err
	}
	if err != nil {
		return err
	}
	generation, err := decodeU128(currentGeneration)
	if err != nil {
		return err
	}
	event, err := decodeU128(eventGeneration)
	if err != nil {
		return err
	}
	if generation != event {
		_, err := tx.ExecContext(ctx, "UPDATE request_attempts SET streak_effect='unchanged' WHERE claim_id=? AND streak_effect='pending'", claimID)
		return err
	}
	next, err := decodeU128(nextBlob)
	if err != nil {
		return err
	}
	streak, err := decodeU128(streakBlob)
	if err != nil {
		return err
	}
	threshold, err := db.ParseU128Decimal(thresholdText)
	if err != nil {
		return claim.ErrInvariant
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.claim_id,m.claim_seq,r.state,r.protocol_success,r.streak_disposition,r.failure_origin,1
FROM dispatch_service_health m JOIN donation_usage_reservations r ON r.claim_id=m.claim_id
WHERE m.health_id_snapshot=? AND m.streak_generation=? AND m.claim_seq>=? ORDER BY m.claim_seq`,
		healthID, currentGeneration, db.EncodeU128(next))
	if err != nil {
		return err
	}
	folded, err := foldFailureRows(ctx, tx, rows, next, streak, threshold, disabled)
	if err != nil {
		return err
	}
	next, streak, disabled = folded.next, folded.streak, folded.disabled

	result, err := tx.ExecContext(ctx, `UPDATE donation_service_health SET next_fold_seq=?,failure_streak=?,failure_disabled=?,updated_at=? WHERE id=? AND streak_generation=? AND next_fold_seq=? AND failure_streak=?`, db.EncodeU128(next), db.EncodeU128(streak), disabled, at, healthID, currentGeneration, nextBlob, streakBlob)
	if err != nil {
		return err
	}
	if err := requireOne(result); err != nil {
		return err
	}
	if folded.crossing == nil {
		return nil
	}
	return adminalerts.RecordDonationFailureTx(ctx, tx, adminalerts.DonationFailure{
		DonationID: donationID, KeyID: keyID, EndpointKeyID: endpointKeyID,
		UseID: useID, Generation: generation, OccurredAt: at, Crossing: *folded.crossing,
		Ref: fmt.Sprintf("donation-use:%d:health:%d:generation:%s:fold:%s", useID, healthID, generation.Decimal(), folded.crossing.Fold.Decimal()),
	})
}
