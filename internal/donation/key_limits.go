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
	return input.MaxConcurrency != nil || input.MaxRPM != nil || input.ConcurrencyOverflow != nil || input.RPMOverflow != nil || input.ConcurrencyQueueLimit != nil || input.RPMQueueLimit != nil
}

func validSharedKeyLimits(input KeyManagementInput) bool {
	if sharedKeyLimitsChanged(input) != (input.ExpectedKeyLimitsRevision > 0) || input.ExpectedKeyLimitsRevision < 0 {
		return false
	}
	for _, value := range []*int64{input.MaxConcurrency, input.MaxRPM, input.ConcurrencyQueueLimit, input.RPMQueueLimit} {
		if value != nil && (*value < 0 || *value > 2147483647) {
			return false
		}
	}
	for _, value := range []*string{input.ConcurrencyOverflow, input.RPMOverflow} {
		if value != nil && *value != "reject" && *value != "queue" {
			return false
		}
	}
	return true
}

func updateSharedKeyLimitsTx(ctx context.Context, tx *sql.Tx, donationID, keyID, actorID int64, role string, input KeyManagementInput, now int64) error {
	var physicalID, revision, concurrency, rpm, concurrencyQueueLimit, rpmQueueLimit int64
	var concurrencyOverflow, rpmOverflow string
	var suspended bool
	err := tx.QueryRowContext(ctx, `SELECT k.id,k.revision,COALESCE(l.max_concurrency,0),COALESCE(l.max_rpm,0),COALESCE(l.concurrency_overflow,'reject'),COALESCE(l.rpm_overflow,'reject'),COALESCE(l.concurrency_queue_limit,0),COALESCE(l.rpm_queue_limit,0),
EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)
FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id
JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.donation_id=d.id AND m.endpoint_key_id=dk.endpoint_key_id
JOIN endpoint_keys k ON k.id=m.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id AND e.user_id=d.user_id
LEFT JOIN endpoint_key_limits l ON l.endpoint_key_id=k.id
WHERE dk.id=? AND d.id=? AND d.status IN ('pending','approved') AND d.terminal_at IS NULL AND dk.ended_at IS NULL
AND EXISTS(SELECT 1 FROM donation_uses u WHERE u.donation_key_id=dk.id AND u.current_approved_revision_id IS NOT NULL)`, keyID, donationID).
		Scan(&physicalID, &revision, &concurrency, &rpm, &concurrencyOverflow, &rpmOverflow, &concurrencyQueueLimit, &rpmQueueLimit, &suspended)
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
	audit := map[string]any{
		"donation_key_id": strconv.FormatInt(keyID, 10), "old_key_limits_revision": strconv.FormatInt(revision, 10), "key_limits_revision": strconv.FormatInt(revision+1, 10),
		"old_max_concurrency": concurrency, "old_max_rpm": rpm,
		"old_concurrency_overflow":    concurrencyOverflow,
		"old_rpm_overflow":            rpmOverflow,
		"old_concurrency_queue_limit": concurrencyQueueLimit,
		"old_rpm_queue_limit":         rpmQueueLimit,
	}
	if input.ConcurrencyOverflow != nil {
		concurrencyOverflow = *input.ConcurrencyOverflow
	}
	if input.RPMOverflow != nil {
		rpmOverflow = *input.RPMOverflow
	}
	if input.ConcurrencyQueueLimit != nil {
		concurrencyQueueLimit = *input.ConcurrencyQueueLimit
	}
	if input.RPMQueueLimit != nil {
		rpmQueueLimit = *input.RPMQueueLimit
	}

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
	if _, err := tx.ExecContext(ctx, `INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm,concurrency_overflow,rpm_overflow,concurrency_queue_limit,rpm_queue_limit)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(endpoint_key_id) DO UPDATE SET max_concurrency=excluded.max_concurrency,max_rpm=excluded.max_rpm,
concurrency_overflow=excluded.concurrency_overflow,rpm_overflow=excluded.rpm_overflow,
concurrency_queue_limit=excluded.concurrency_queue_limit,rpm_queue_limit=excluded.rpm_queue_limit`, physicalID, concurrency, rpm, concurrencyOverflow, rpmOverflow, concurrencyQueueLimit, rpmQueueLimit); err != nil {
		return fmt.Errorf("donation: update shared key limits: %w", err)
	}
	audit["concurrency_overflow"] = concurrencyOverflow
	audit["rpm_overflow"] = rpmOverflow
	audit["concurrency_queue_limit"] = concurrencyQueueLimit
	audit["rpm_queue_limit"] = rpmQueueLimit
	audit["max_concurrency"], audit["max_rpm"] = concurrency, rpm
	note, err := json.Marshal(audit)
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
