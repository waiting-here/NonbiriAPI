package connectionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// EndDonationKeyUsesTx follows the domain's member termination in the same
// transaction. It preserves review history and the existing report retention.
func EndDonationKeyUsesTx(ctx context.Context, tx *sql.Tx, donationKeyID, now int64) error {
	if tx == nil || donationKeyID <= 0 || now < 0 || now > 253402300799 {
		return ErrInvariant
	}
	var ended sql.NullInt64
	var members int
	err := tx.QueryRowContext(ctx, `SELECT dk.ended_at,(SELECT count(*) FROM donation_key_memberships m WHERE m.donation_key_id=dk.id)
 FROM donation_keys dk WHERE dk.id=?`, donationKeyID).Scan(&ended, &members)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("connectionstore: read ended member: %w", err)
	}
	if !ended.Valid || ended.Int64 < 0 || ended.Int64 > 253402300799 || members != 0 {
		return ErrInvariant
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT g.credential_version_id FROM donation_uses u
 JOIN donation_use_revisions ur ON ur.use_id=u.id AND (ur.id=u.current_approved_revision_id OR ur.id=u.current_pending_revision_id)
 JOIN endpoint_key_grant_revisions g ON g.id=ur.grant_revision_id WHERE u.donation_key_id=?
 UNION SELECT DISTINCT r.credential_version_id FROM donation_uses u JOIN discovery_runs r ON r.authorization_use_id=u.id
 WHERE u.donation_key_id=? AND r.state='checking' AND r.credential_version_id IS NOT NULL ORDER BY 1`, donationKeyID, donationKeyID)
	if err != nil {
		return err
	}
	var versions []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		versions = append(versions, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	at := max(now, ended.Int64)
	statements := []string{
		`UPDATE donation_use_revisions SET review_state='cancelled',review_reason='',reviewer_user_id=NULL,reviewer_role=NULL,reviewed_at=max(?,created_at)
 WHERE review_state='pending' AND id IN(SELECT current_pending_revision_id FROM donation_uses WHERE donation_key_id=?)`,
		`UPDATE donation_uses SET current_approved_revision_id=NULL,current_pending_revision_id=NULL,enabled=0,discovery_enabled=0,revision=revision+1,updated_at=max(?,updated_at)
 WHERE donation_key_id=? AND (current_approved_revision_id IS NOT NULL OR current_pending_revision_id IS NOT NULL OR enabled<>0 OR discovery_enabled<>0)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, at, donationKeyID); err != nil {
			return fmt.Errorf("connectionstore: release ended use: %w", err)
		}
	}
	if err := interruptDiscoveryRunsTx(ctx, tx, "authorization_kind='donation_use' AND authorization_use_id IN(SELECT id FROM donation_uses WHERE donation_key_id=?)", donationKeyID, at); err != nil {
		return err
	}
	for _, id := range versions {
		if _, err := RetireUnreferencedVersionTx(ctx, tx, id, at); err != nil {
			return err
		}
	}
	return nil
}

// ClearExpiredUseFingerprintsTx follows the existing member fingerprint cleanup.
// It cannot clear a current authorization or shorten the original retention.
func ClearExpiredUseFingerprintsTx(ctx context.Context, tx *sql.Tx, donationKeyID, now int64) (int, error) {
	if tx == nil || donationKeyID <= 0 || now < 0 || now > 253402300799 {
		return 0, ErrInvariant
	}
	var fingerprint []byte
	var ended, until sql.NullInt64
	err := tx.QueryRowContext(ctx, "SELECT report_fingerprint,ended_at,report_match_until FROM donation_keys WHERE id=?", donationKeyID).Scan(&fingerprint, &ended, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if len(fingerprint) != 0 || !ended.Valid || !until.Valid || until.Int64 > now {
		return 0, ErrInvariant
	}
	var current int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM donation_uses WHERE donation_key_id=? AND (current_approved_revision_id IS NOT NULL OR current_pending_revision_id IS NOT NULL)", donationKeyID).Scan(&current); err != nil {
		return 0, err
	}
	if current != 0 {
		return 0, ErrInvariant
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_use_revisions AS ur SET report_fingerprint=NULL
 WHERE id IN(SELECT history.id FROM donation_use_revisions history JOIN donation_uses member ON member.id=history.use_id WHERE member.donation_key_id=? AND history.report_fingerprint IS NOT NULL AND member.current_approved_revision_id IS NOT history.id AND member.current_pending_revision_id IS NOT history.id ORDER BY history.id LIMIT 100)
 AND NOT EXISTS(SELECT 1 FROM donation_uses u WHERE u.id=ur.use_id AND (u.current_approved_revision_id=ur.id OR u.current_pending_revision_id=ur.id))`, donationKeyID)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if count < 0 || count > 100 {
		return 0, ErrInvariant
	}
	return int(count), nil
}
