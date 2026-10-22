package charity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func foldFailureOutcome(streak db.U128, disabled int, threshold db.U128, state string, success sql.NullInt64, disposition, origin sql.NullString, apply bool) (db.U128, int, bool, error) {
	if !disposition.Valid || !origin.Valid || !contract.ValidOutcome(contract.StreakDisposition(disposition.String), contract.FailureOrigin(origin.String)) || (state != "committed" && state != "released") {
		return streak, disabled, false, claim.ErrInvariant
	}
	if disposition.String != "success" && disposition.String != "upstream_failure" {
		return streak, disabled, false, nil
	}
	if state != "committed" || !success.Valid || (disposition.String == "upstream_failure" && success.Int64 != 0) {
		return streak, disabled, false, claim.ErrInvariant
	}
	if !apply {
		return streak, disabled, false, nil
	}
	if disposition.String == "success" {
		return db.U128{}, disabled, false, nil
	}
	next, err := addU128(streak, big.NewInt(1))
	if err != nil {
		return streak, disabled, false, err
	}
	if disabled == 0 && threshold != (db.U128{}) && next.Big().Cmp(threshold.Big()) >= 0 {
		return next, 1, true, nil
	}
	return next, disabled, false, nil
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
		return nil
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
		return nil
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
	disabledNow := false
	var disablingStreak, disablingFold db.U128
	for {
		var state string
		var success sql.NullInt64
		var disposition, origin sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT r.state,r.protocol_success,r.streak_disposition,r.failure_origin
FROM dispatch_service_health m JOIN donation_usage_reservations r ON r.claim_id=m.claim_id
WHERE m.health_id_snapshot=? AND m.streak_generation=? AND m.claim_seq=?`, healthID, currentGeneration, db.EncodeU128(next)).Scan(&state, &success, &disposition, &origin)
		if errors.Is(err, sql.ErrNoRows) || state == "reserved" {
			break
		}
		if err != nil {
			return err
		}
		var justDisabled bool
		streak, disabled, justDisabled, err = foldFailureOutcome(streak, disabled, threshold, state, success, disposition, origin, true)
		if err != nil {
			return err
		}
		if justDisabled {
			disabledNow, disablingStreak, disablingFold = true, streak, next
		}
		next, err = addU128(next, big.NewInt(1))
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_service_health SET next_fold_seq=?,failure_streak=?,failure_disabled=?,updated_at=? WHERE id=? AND streak_generation=? AND next_fold_seq=? AND failure_streak=?`, db.EncodeU128(next), db.EncodeU128(streak), disabled, at, healthID, currentGeneration, nextBlob, streakBlob)
	if err != nil {
		return err
	}
	if err := requireOne(result); err != nil {
		return err
	}
	if !disabledNow {
		return nil
	}
	ref := fmt.Sprintf("donation-use:%d:health:%d:generation:%s:fold:%s", useID, healthID, generation.Decimal(), disablingFold.Decimal())
	type target struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	type fact struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	body, err := json.Marshal(struct {
		Targets []target `json:"targets"`
		Facts   []fact   `json:"occurred_facts"`
	}{
		Targets: []target{{"donation", fmt.Sprint(donationID)}, {"donation_key", fmt.Sprint(keyID)}, {"endpoint_key", fmt.Sprint(endpointKeyID)}},
		Facts:   []fact{{"donation_id", fmt.Sprint(donationID)}, {"donation_key_id", fmt.Sprint(keyID)}, {"endpoint_key_id", fmt.Sprint(endpointKeyID)}, {"use_id", fmt.Sprint(useID)}, {"failure_streak", disablingStreak.Decimal()}, {"failure_threshold", thresholdText}, {"generation", generation.Decimal()}, {"fold", disablingFold.Decimal()}},
	})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,ref,created_at,resolved,context_version,context_json)
SELECT 'donation_failure_disabled','charity donation service disabled after consecutive protocol failures',?,?,0,1,?
WHERE NOT EXISTS(SELECT 1 FROM admin_alerts WHERE kind='donation_failure_disabled' AND ref=?)`, ref, at, string(body), ref)
	return err
}
