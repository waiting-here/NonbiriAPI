package secret

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"time"
)

var ErrReviewMaterial = errors.New("secret: donation review material unavailable")
var ErrReviewConflict = errors.New("secret: donation review revision conflict")

const reviewPurpose = "donation-key-body-review/v1"
const reviewAAD = "NonbiriAPI/generation/2/donation-review-material/v1"

// DonationReview keeps credential-body identity inside the trusted secret boundary.
type DonationReview struct{ vault *Vault }

func NewDonationReview(vault *Vault) (*DonationReview, error) {
	if vault == nil {
		return nil, ErrReviewMaterial
	}
	return &DonationReview{vault: vault}, nil
}

// Initialize creates material only when no durable review identities exist.
func (r *DonationReview) Initialize(ctx context.Context, store *sql.DB) error {
	if r == nil || store == nil {
		return ErrReviewMaterial
	}
	tx, err := store.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var envelope []byte
	err = tx.QueryRowContext(ctx, `SELECT envelope FROM donation_review_material WHERE id = 1`).Scan(&envelope)
	if errors.Is(err, sql.ErrNoRows) {
		var existing int
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_key_review_requirements)
   OR EXISTS(SELECT 1 FROM donation_keys WHERE key_body_review_hmac IS NOT NULL)
   OR EXISTS(SELECT 1 FROM endpoint_key_secrets WHERE key_body_review_hmac IS NOT NULL)`).Scan(&existing)
		if err != nil {
			return err
		}
		if existing != 0 {
			return ErrReviewMaterial
		}
		key, err := r.vault.DeriveGenerationTwoSubkey([]byte(reviewPurpose))
		if err != nil {
			return ErrReviewMaterial
		}
		defer clear(key)
		encoded, err := r.vault.seal(key, []byte(reviewAAD))
		if err != nil {
			return ErrReviewMaterial
		}
		now := time.Now().Unix()
		if _, err = tx.ExecContext(ctx, `INSERT INTO donation_review_material(id,version,envelope,created_at,updated_at) VALUES(1,1,?,?,?)`, []byte(encoded), now, now); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	key, err := r.load(ctx, tx)
	if err != nil {
		return err
	}
	clear(key)
	return tx.Commit()
}

func (r *DonationReview) load(ctx context.Context, tx *sql.Tx) ([]byte, error) {
	if r == nil || r.vault == nil || tx == nil {
		return nil, ErrReviewMaterial
	}
	var envelope []byte
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version,envelope FROM donation_review_material WHERE id=1`).Scan(&version, &envelope); err != nil || version != 1 {
		return nil, ErrReviewMaterial
	}
	key, err := r.vault.open(string(envelope), []byte(reviewAAD))
	clear(envelope)
	if err != nil || len(key) != sha256.Size {
		clear(key)
		return nil, ErrReviewMaterial
	}
	return key, nil
}

func (r *DonationReview) RecordEndpointSecret(ctx context.Context, tx *sql.Tx, id int64, plaintext []byte, now time.Time) error {
	key, err := r.load(ctx, tx)
	if err != nil {
		return err
	}
	defer clear(key)
	if id <= 0 || len(plaintext) == 0 || len(plaintext) > MaxPlaintextBytes {
		return ErrInvalidPlaintext
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(plaintext)
	digest := mac.Sum(nil)
	defer clear(digest)
	result, err := tx.ExecContext(ctx, `UPDATE endpoint_key_secrets SET key_body_review_hmac=? WHERE id=? AND key_body_review_hmac IS NULL`, digest, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrReviewConflict
	}
	return nil
}

// BackfillEndpointSecrets accepts the migration's bounded, verified source set.
func (r *DonationReview) BackfillEndpointSecrets(ctx context.Context, tx *sql.Tx, ids []int64, now time.Time) error {
	if len(ids) > 1000 {
		return ErrReviewMaterial
	}
	for _, id := range ids {
		var contextID []byte
		var encoded string
		var present int
		if err := tx.QueryRowContext(ctx, `SELECT context_id,encrypted_secret,key_body_review_hmac IS NOT NULL FROM endpoint_key_secrets WHERE id=?`, id).Scan(&contextID, &encoded, &present); err != nil {
			return err
		}
		if present != 0 {
			continue
		}
		credentialContext, err := NewGenerationTwoEndpointKeyContext(contextID)
		clear(contextID)
		if err != nil {
			return ErrReviewMaterial
		}
		plaintext, err := r.vault.OpenForGenerationTwoContext(encoded, credentialContext)
		if err != nil {
			return ErrReviewMaterial
		}
		err = r.RecordEndpointSecret(ctx, tx, id, plaintext, now)
		clear(plaintext)
		if err != nil {
			return err
		}
	}
	return nil
}

// BackfillLegacySecrets proves historical members from retained immutable bodies.
// The caller commits each batch and resumes from secrets without body identity.
func (r *DonationReview) BackfillLegacySecrets(ctx context.Context, tx *sql.Tx, ids []int64, now time.Time) error {
	if len(ids) > 1000 {
		return ErrReviewMaterial
	}
	key, err := r.load(ctx, tx)
	if err != nil {
		return err
	}
	defer clear(key)
	legacyKey, err := r.vault.DeriveGenerationTwoSubkey([]byte("credential-report-fingerprint/v1"))
	if err != nil {
		return ErrReviewMaterial
	}
	defer clear(legacyKey)
	for _, id := range ids {
		if id <= 0 {
			return ErrReviewMaterial
		}
		var contextID []byte
		var encoded, connector, canonicalURL string
		var createdAt int64
		var present int
		if err = tx.QueryRowContext(ctx, `SELECT context_id,encrypted_secret,connector_type,canonical_base_url,created_at,key_body_review_hmac IS NOT NULL
FROM endpoint_key_secrets WHERE id=?`, id).Scan(&contextID, &encoded, &connector, &canonicalURL, &createdAt, &present); err != nil {
			return err
		}
		if present != 0 {
			clear(contextID)
			continue
		}
		credentialContext, err := NewGenerationTwoEndpointKeyContext(contextID)
		clear(contextID)
		if err != nil {
			return ErrReviewMaterial
		}
		plaintext, err := r.vault.OpenForGenerationTwoContext(encoded, credentialContext)
		if err != nil {
			return ErrReviewMaterial
		}
		bodyMAC := hmac.New(sha256.New, key)
		_, _ = bodyMAC.Write(plaintext)
		bodyDigest := bodyMAC.Sum(nil)
		legacyMAC := hmac.New(sha256.New, legacyKey)
		for _, field := range [][]byte{[]byte(connector), []byte(canonicalURL), plaintext} {
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(field)))
			_, _ = legacyMAC.Write(length[:])
			_, _ = legacyMAC.Write(field)
		}
		legacyDigest := legacyMAC.Sum(nil)
		clear(plaintext)
		_, err = tx.ExecContext(ctx, `UPDATE donation_keys SET key_body_review_hmac=?,
review_revision=(SELECT revision FROM donation_key_review_requirements WHERE hmac=?)
WHERE report_fingerprint=? AND created_at>=? AND key_body_review_hmac IS NULL`, bodyDigest, bodyDigest, legacyDigest, createdAt)
		clear(legacyDigest)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE endpoint_key_secrets SET key_body_review_hmac=? WHERE id=? AND key_body_review_hmac IS NULL`, bodyDigest, id)
		}
		clear(bodyDigest)
		if err != nil {
			return err
		}
	}
	return nil
}

// CaptureDonationKey snapshots the exact immutable credential version at submission.
func (r *DonationReview) CaptureDonationKey(ctx context.Context, tx *sql.Tx, memberID, endpointKeyID int64, now time.Time) (bool, error) {
	key, err := r.load(ctx, tx)
	if err != nil {
		return false, err
	}
	clear(key)
	var secretID int64
	if err = tx.QueryRowContext(ctx, `SELECT secret_ref_id FROM endpoint_keys WHERE id=?`, endpointKeyID).Scan(&secretID); err != nil {
		return false, err
	}
	if err = r.BackfillEndpointSecrets(ctx, tx, []int64{secretID}, now); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE donation_keys SET key_body_review_hmac=(SELECT key_body_review_hmac FROM endpoint_key_secrets WHERE id=?),
 review_revision=(SELECT revision FROM donation_key_review_requirements WHERE hmac=(SELECT key_body_review_hmac FROM endpoint_key_secrets WHERE id=?)) WHERE id=? AND key_body_review_hmac IS NULL`, secretID, secretID, memberID)
	if err != nil {
		return false, err
	}
	var required int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(r.required,0) FROM donation_keys dk LEFT JOIN donation_key_review_requirements r ON r.hmac=dk.key_body_review_hmac WHERE dk.id=?`, memberID).Scan(&required)
	return required != 0, err
}

func (r *DonationReview) RequireDonation(ctx context.Context, tx *sql.Tx, donationID int64, now time.Time) error {
	key, err := r.load(ctx, tx)
	if err != nil {
		return err
	}
	clear(key)
	var unknown int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM donation_keys WHERE donation_id=? AND key_body_review_hmac IS NULL`, donationID).Scan(&unknown); err != nil {
		return err
	}
	if unknown != 0 {
		return ErrReviewMaterial
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donation_key_review_requirements(hmac,required,revision,created_at,updated_at)
 SELECT DISTINCT key_body_review_hmac,1,1,?,? FROM donation_keys WHERE donation_id=?
 ON CONFLICT(hmac) DO UPDATE SET required=1,revision=revision+1,updated_at=excluded.updated_at`, now.Unix(), now.Unix(), donationID)
	return err
}

type ReviewApproval struct {
	DonationKeyID    int64
	Enabled          bool
	ExpectedRevision *int64
}

// ApproveDonationKeys clears only explicitly enabled, currently required bodies.
func (r *DonationReview) ApproveDonationKeys(ctx context.Context, tx *sql.Tx, donationID int64, approvals []ReviewApproval, now time.Time) error {
	key, err := r.load(ctx, tx)
	if err != nil {
		return err
	}
	clear(key)
	type selected struct {
		hmac     []byte
		revision int64
	}
	selections := make(map[string]selected)
	defer func() {
		for _, item := range selections {
			clear(item.hmac)
		}
	}()
	for _, approval := range approvals {
		if !approval.Enabled {
			continue
		}
		var digest []byte
		var required int
		var revision sql.NullInt64
		err = tx.QueryRowContext(ctx, `SELECT dk.key_body_review_hmac,COALESCE(r.required,0),r.revision FROM donation_keys dk LEFT JOIN donation_key_review_requirements r ON r.hmac=dk.key_body_review_hmac WHERE dk.id=? AND dk.donation_id=?`, approval.DonationKeyID, donationID).Scan(&digest, &required, &revision)
		if err != nil {
			return err
		}
		if len(digest) != sha256.Size {
			clear(digest)
			return ErrReviewMaterial
		}
		if required == 0 {
			clear(digest)
			continue
		}
		if approval.ExpectedRevision == nil || *approval.ExpectedRevision != revision.Int64 {
			clear(digest)
			return ErrReviewConflict
		}
		if _, ok := selections[string(digest)]; ok {
			clear(digest)
			continue
		}
		selections[string(digest)] = selected{digest, revision.Int64}
	}
	for _, item := range selections {
		result, err := tx.ExecContext(ctx, `UPDATE donation_key_review_requirements SET required=0,revision=revision+1,updated_at=? WHERE hmac=? AND required=1 AND revision=?`, now.Unix(), item.hmac, item.revision)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrReviewConflict
		}
	}
	return nil
}

// RewrapMaterial preserves stable identity across a change of encryption master.
func (r *DonationReview) RewrapMaterial(ctx context.Context, tx *sql.Tx, target *Vault, now time.Time) error {
	key, err := r.load(ctx, tx)
	if err != nil {
		return err
	}
	defer clear(key)
	encoded, err := target.seal(key, []byte(reviewAAD))
	if err != nil {
		return ErrReviewMaterial
	}
	_, err = tx.ExecContext(ctx, `UPDATE donation_review_material SET envelope=?,updated_at=? WHERE id=1`, []byte(encoded), now.Unix())
	return err
}

// DonationReviewSnapshot names an original immutable source proven by migration.
type DonationReviewSnapshot struct {
	DonationKeyID int64
	SecretRefID   int64
}

func (r *DonationReview) BackfillDonationKeys(ctx context.Context, tx *sql.Tx, snapshots []DonationReviewSnapshot, now time.Time) error {
	if len(snapshots) > 1000 {
		return ErrReviewMaterial
	}
	key, err := r.load(ctx, tx)
	if err != nil {
		return err
	}
	clear(key)
	for _, snapshot := range snapshots {
		if snapshot.DonationKeyID <= 0 || snapshot.SecretRefID <= 0 {
			return ErrReviewMaterial
		}
		if err = r.BackfillEndpointSecrets(ctx, tx, []int64{snapshot.SecretRefID}, now); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE donation_keys SET key_body_review_hmac=(SELECT key_body_review_hmac FROM endpoint_key_secrets WHERE id=?),review_revision=(SELECT revision FROM donation_key_review_requirements WHERE hmac=(SELECT key_body_review_hmac FROM endpoint_key_secrets WHERE id=?)) WHERE id=? AND key_body_review_hmac IS NULL`, snapshot.SecretRefID, snapshot.SecretRefID, snapshot.DonationKeyID); err != nil {
			return err
		}
	}
	return nil
}
