package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// EnsureOwnBindingInTransaction appends one owned donation key, or preserves
// an identical existing binding. A positive discovery revision requires that
// exact successful automatic catalog; zero requires a manual entry.
func (s *Service) EnsureOwnBindingInTransaction(ctx context.Context, tx *sql.Tx, userID, modelID, donationKeyID int64, upstreamModelID string, discoveryRevision int64) error {
	if s == nil || tx == nil || userID <= 0 || modelID <= 0 || donationKeyID <= 0 || discoveryRevision < 0 {
		return ErrInvalidRequest
	}
	if _, err := validateSelections([]BindingSelection{{DonationKeyID: strconv.FormatInt(donationKeyID, 10), UpstreamModelID: upstreamModelID}}); err != nil {
		return err
	}
	if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, userID); err != nil {
		return mapAuthorization(err)
	}
	now, err := s.nowUnix()
	if err != nil {
		return err
	}
	revision, count, err := readBindingHeadTx(ctx, tx, modelID)
	if err != nil {
		return err
	}
	var keyID int64
	err = tx.QueryRowContext(ctx, `SELECT dk.endpoint_key_id FROM donation_keys dk
JOIN donations d ON d.id=dk.donation_id
JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
JOIN endpoint_keys k ON k.id=m.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id
JOIN model_pair_catalog pc ON pc.endpoint_key_id=k.id AND pc.normalized_model_id=?
JOIN model_discovery_evidence de ON de.endpoint_key_id=k.id
WHERE dk.id=? AND d.user_id=? AND e.user_id=? AND d.status='approved'
AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?)
AND ((?=0 AND pc.manual_supports>0) OR (? > 0 AND de.state='succeeded' AND de.revision=? AND pc.automatic_revision=de.revision AND pc.automatic_supports>0))`,
		upstreamModelID, donationKeyID, userID, userID, now, discoveryRevision, discoveryRevision, discoveryRevision).Scan(&keyID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_model_bindings WHERE charity_model_id=? AND donation_key_id=? AND endpoint_key_id=? AND upstream_model_id=?)`, modelID, donationKeyID, keyID, upstreamModelID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if count >= maxBindingBatch {
		return ErrResourceLimit
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, modelID, donationKeyID, keyID, upstreamModelID, count, now, now); err != nil {
		return classifyWrite("append owned charity binding", err)
	}
	return advanceBindingRevisionTx(ctx, tx, modelID, revision, now)
}
