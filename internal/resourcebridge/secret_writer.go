package resourcebridge

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"io"
	"time"
	"unicode"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// MatchEndpointSecret compares credential bodies only inside the trusted vault
// boundary. The caller supplies a reference selected in its owner transaction.
func (r *Runtime) MatchEndpointSecret(ctx context.Context, tx *sql.Tx, refID int64, candidate []byte) (bool, error) {
	if err := r.begin(); err != nil {
		return false, err
	}
	defer r.end()
	if ctx == nil || tx == nil || refID <= 0 || !validCredential(candidate) {
		return false, ErrInvalidInput
	}
	var contextID []byte
	var ciphertext string
	if err := tx.QueryRowContext(ctx, `SELECT context_id,encrypted_secret FROM endpoint_key_secrets WHERE id=?`, refID).Scan(&contextID, &ciphertext); err != nil {
		return false, ErrUnavailable
	}
	defer clear(contextID)
	credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
	if err != nil {
		return false, ErrUnavailable
	}
	plaintext, err := r.vault.OpenForGenerationTwoContext(ciphertext, credentialContext)
	ciphertext = ""
	if err != nil {
		return false, ErrUnavailable
	}
	defer clear(plaintext)
	if ctx.Err() != nil {
		return false, ErrInterrupted
	}
	return bytes.Equal(plaintext, candidate), nil
}

const (
	contextIDBytes  = 16
	maxBaseURLBytes = 4096
)

// WriteEndpointSecret persists one immutable Generation 2 credential row in
// the caller's transaction. Ownership checks and commit remain with the
// resource repository.
func (r *Runtime) WriteEndpointSecret(ctx context.Context, tx *sql.Tx, input resources.SecretWriteInput) (resources.StoredSecret, error) {
	return r.writeCredentialSecret(ctx, tx, input, true)
}

// WriteCredentialSecret creates one body without inventing an authorized
// endpoint. Explicit grants and discovery sources authorize its later use.
func (r *Runtime) WriteCredentialSecret(ctx context.Context, tx *sql.Tx, plaintext []byte, createdAt int64) (resources.StoredSecret, error) {
	return r.writeCredentialSecret(ctx, tx, resources.SecretWriteInput{Plaintext: plaintext, CreatedAt: createdAt}, false)
}

func (r *Runtime) writeCredentialSecret(ctx context.Context, tx *sql.Tx, input resources.SecretWriteInput, hasOrigin bool) (resources.StoredSecret, error) {
	defer clear(input.Plaintext)
	if err := r.begin(); err != nil {
		return resources.StoredSecret{}, err
	}
	defer r.end()
	if ctx == nil || tx == nil || !r.validDecisionSecond(input.CreatedAt) || !validCredential(input.Plaintext) {
		return resources.StoredSecret{}, ErrInvalidInput
	}
	if ctx.Err() != nil {
		return resources.StoredSecret{}, ErrInterrupted
	}
	if hasOrigin && (!validConnectorType(input.ConnectorType) || !validBaseURLText(input.CanonicalBaseURL) || !r.canonicalBaseURL(input.CanonicalBaseURL)) {
		return resources.StoredSecret{}, ErrInvalidInput
	}
	if !hasOrigin && (input.ConnectorType != "" || input.CanonicalBaseURL != "") {
		return resources.StoredSecret{}, ErrInvalidInput
	}

	var sourceContextID [contextIDBytes]byte
	r.randomMu.Lock()
	_, randomErr := io.ReadFull(r.random, sourceContextID[:])
	r.randomMu.Unlock()
	if randomErr != nil {
		clear(sourceContextID[:])
		return resources.StoredSecret{}, ErrUnavailable
	}
	credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(sourceContextID[:])
	clear(sourceContextID[:])
	if err != nil {
		return resources.StoredSecret{}, ErrUnavailable
	}
	rowContextID := credentialContext.ContextID()
	defer clear(rowContextID)

	var fingerprint [sha256.Size]byte
	var originType, originURL any
	if hasOrigin {
		fingerprint = r.fingerprint(input.ConnectorType, input.CanonicalBaseURL, input.Plaintext)
		originType, originURL = input.ConnectorType, input.CanonicalBaseURL
	}
	head, tail := displayFragments(input.Plaintext)
	ciphertext, err := r.vault.SealForGenerationTwoContext(input.Plaintext, credentialContext)
	if err != nil {
		return resources.StoredSecret{}, ErrUnavailable
	}
	defer func() { ciphertext = "" }()
	result, err := tx.ExecContext(ctx, `INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at,orphaned_at)
VALUES(?,?,?,?,?,NULL)`, rowContextID, originURL, originType, ciphertext, input.CreatedAt)
	if err != nil {
		return resources.StoredSecret{}, ErrUnavailable
	}
	refID, err := result.LastInsertId()
	if err != nil || refID <= 0 {
		return resources.StoredSecret{}, ErrUnavailable
	}
	if err := r.review.RecordEndpointSecret(ctx, tx, refID, input.Plaintext, time.Unix(input.CreatedAt, 0)); err != nil {
		return resources.StoredSecret{}, ErrUnavailable
	}
	return resources.StoredSecret{
		RefID:       refID,
		Fingerprint: fingerprint,
		DisplayHead: head,
		DisplayTail: tail,
	}, nil
}

func (r *Runtime) FingerprintEndpointSecret(ctx context.Context, tx *sql.Tx, refID int64, connectorType, canonicalBaseURL string) ([32]byte, error) {
	if err := r.begin(); err != nil {
		return [32]byte{}, err
	}
	defer r.end()
	if ctx == nil || tx == nil || refID <= 0 || !validConnectorType(connectorType) ||
		!validBaseURLText(canonicalBaseURL) || !r.canonicalBaseURL(canonicalBaseURL) {
		return [32]byte{}, ErrInvalidInput
	}
	var contextID []byte
	var ciphertext string
	if err := tx.QueryRowContext(ctx, `SELECT context_id,encrypted_secret FROM endpoint_key_secrets WHERE id=? AND orphaned_at IS NULL`, refID).Scan(&contextID, &ciphertext); err != nil {
		return [32]byte{}, ErrUnavailable
	}
	defer clear(contextID)
	credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
	if err != nil {
		return [32]byte{}, ErrUnavailable
	}
	plaintext, err := r.vault.OpenForGenerationTwoContext(ciphertext, credentialContext)
	ciphertext = ""
	if err != nil {
		return [32]byte{}, ErrUnavailable
	}
	defer clear(plaintext)
	if ctx.Err() != nil {
		return [32]byte{}, ErrInterrupted
	}
	return r.fingerprint(connectorType, canonicalBaseURL, plaintext), nil
}

// MarkEndpointSecretOrphaned performs the only allowed orphan transition. A
// still-referenced or already-marked row is an idempotent no-op.
func (r *Runtime) MarkEndpointSecretOrphaned(ctx context.Context, tx *sql.Tx, refID, at int64) error {
	if err := r.begin(); err != nil {
		return err
	}
	defer r.end()
	if ctx == nil || tx == nil || refID <= 0 || !r.validDecisionSecond(at) {
		return ErrInvalidInput
	}
	if ctx.Err() != nil {
		return ErrInterrupted
	}
	if _, err := tx.ExecContext(ctx, `UPDATE endpoint_key_secrets SET orphaned_at=?
WHERE id=? AND orphaned_at IS NULL
AND NOT EXISTS(SELECT 1 FROM secret_live_references live WHERE live.secret_ref_id=endpoint_key_secrets.id)`, at, refID); err != nil {
		if ctx.Err() != nil {
			return ErrInterrupted
		}
		return ErrUnavailable
	}
	return nil
}

func (r *Runtime) canonicalBaseURL(value string) bool {
	client, err := r.backend.Open(value)
	return err == nil && !nilInterface(client) && client.BaseURL() == value
}

func (r *Runtime) fingerprint(connectorType, canonicalBaseURL string, plaintext []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, r.fingerprintKey[:])
	writeFramed(mac, []byte(connectorType))
	writeFramed(mac, []byte(canonicalBaseURL))
	writeFramed(mac, plaintext)
	sum := mac.Sum(nil)
	defer clear(sum)
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], sum)
	return fingerprint
}

func writeFramed(writer io.Writer, value []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}

func displayFragments(plaintext []byte) (string, string) {
	var first [4]rune
	var last [4]rune
	count := 0
	remaining := plaintext
	for len(remaining) > 0 {
		runeValue, size := utf8.DecodeRune(remaining)
		if count < len(first) {
			first[count] = runeValue
		}
		last[count%len(last)] = runeValue
		count++
		remaining = remaining[size:]
	}
	if count <= 4 {
		return "", ""
	}
	head := asciiFragment(first[:])
	if count <= 8 {
		return head, ""
	}
	orderedLast := [4]rune{
		last[count%len(last)],
		last[(count+1)%len(last)],
		last[(count+2)%len(last)],
		last[(count+3)%len(last)],
	}
	return head, asciiFragment(orderedLast[:])
}

func asciiFragment(values []rune) string {
	for _, value := range values {
		if value > unicode.MaxASCII {
			return ""
		}
	}
	return string(values)
}

func validCredential(value []byte) bool {
	if len(value) == 0 || len(value) > secret.MaxPlaintextBytes || !utf8.Valid(value) {
		return false
	}
	for len(value) > 0 {
		runeValue, size := utf8.DecodeRune(value)
		if unicode.IsControl(runeValue) || runeValue == 0x7f {
			return false
		}
		value = value[size:]
	}
	return true
}

func validConnectorType(value string) bool {
	return connectorcontract.Type(value).Valid()
}

func validBaseURLText(value string) bool {
	if len(value) < 1 || len(value) > maxBaseURLBytes || !utf8.ValidString(value) {
		return false
	}
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) || runeValue == 0x7f {
			return false
		}
	}
	return true
}
