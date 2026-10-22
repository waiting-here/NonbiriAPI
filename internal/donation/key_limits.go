package donation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

func sharedKeyLimitsChanged(input KeyManagementInput) bool {
	return input.MaxConcurrency != nil || input.MaxRPM != nil
}

func validSharedKeyLimits(input KeyManagementInput) bool {
	if sharedKeyLimitsChanged(input) != (input.ExpectedKeyLimitsRevision > 0) || input.ExpectedKeyLimitsRevision < 0 {
		return false
	}
	for _, value := range []*int64{input.MaxConcurrency, input.MaxRPM} {
		if value != nil && (*value < 0 || *value > 2147483647) {
			return false
		}
	}
	return true
}

func updateSharedKeyLimitsTx(ctx context.Context, tx *sql.Tx, donationID, keyID, actorID int64, role string, input KeyManagementInput, now int64) error {
	var physicalID, revision, concurrency, rpm int64
	var suspended bool
	err := tx.QueryRowContext(ctx, `SELECT k.id,k.revision,COALESCE(l.max_concurrency,0),COALESCE(l.max_rpm,0),
EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)
FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id
JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.donation_id=d.id AND m.endpoint_key_id=dk.endpoint_key_id
JOIN endpoint_keys k ON k.id=m.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id AND e.user_id=d.user_id
LEFT JOIN endpoint_key_limits l ON l.endpoint_key_id=k.id
WHERE dk.id=? AND d.id=? AND d.status IN ('pending','approved') AND d.terminal_at IS NULL AND dk.ended_at IS NULL
AND EXISTS(SELECT 1 FROM donation_uses u WHERE u.donation_key_id=dk.id AND u.current_approved_revision_id IS NOT NULL)`, keyID, donationID).
		Scan(&physicalID, &revision, &concurrency, &rpm, &suspended)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("donation: read shared key limits: %w", err)
	}
	if revision != input.ExpectedKeyLimitsRevision || revision == int64(^uint64(0)>>1) {
		return ErrConflict
	}
	if suspended {
		return ErrResourceLocked
	}
	oldConcurrency, oldRPM := concurrency, rpm
	if input.MaxConcurrency != nil {
		concurrency = *input.MaxConcurrency
	}
	if input.MaxRPM != nil {
		rpm = *input.MaxRPM
	}
	result, err := tx.ExecContext(ctx, `UPDATE endpoint_keys SET revision=revision+1,updated_at=? WHERE id=? AND revision=?`, now, physicalID, revision)
	if err != nil {
		return fmt.Errorf("donation: advance shared key revision: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm)
VALUES(?,?,?) ON CONFLICT(endpoint_key_id) DO UPDATE SET max_concurrency=excluded.max_concurrency,max_rpm=excluded.max_rpm`, physicalID, concurrency, rpm); err != nil {
		return fmt.Errorf("donation: update shared key limits: %w", err)
	}
	note, err := json.Marshal(struct {
		KeyID          string `json:"donation_key_id"`
		OldRevision    string `json:"old_key_limits_revision"`
		NewRevision    string `json:"key_limits_revision"`
		OldConcurrency int64  `json:"old_max_concurrency"`
		Concurrency    int64  `json:"max_concurrency"`
		OldRPM         int64  `json:"old_max_rpm"`
		RPM            int64  `json:"max_rpm"`
	}{strconv.FormatInt(keyID, 10), strconv.FormatInt(revision, 10), strconv.FormatInt(revision+1, 10), oldConcurrency, concurrency, oldRPM, rpm})
	if err != nil {
		return fmt.Errorf("donation: encode shared key audit: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donation_reviews(donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,?,?,'limit_update',?,?)`, donationID, input.ExpectedRevision+1, actorID, role, string(note), now)
	if err != nil {
		return fmt.Errorf("donation: record shared key limits: %w", err)
	}
	return nil
}
