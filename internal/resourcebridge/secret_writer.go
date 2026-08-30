package resourcebridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"io"
	"unicode"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	contextIDBytes  = 16
	maxBaseURLBytes = 4096
)

// WriteEndpointSecret persists one immutable Generation 2 credential row in
// the caller's transaction. Ownership checks and commit remain with the
// resource repository.
func (r *Runtime) WriteEndpointSecret(ctx context.Context, tx *sql.Tx, input resources.SecretWriteInput) (resources.StoredSecret, error) {
	defer clear(input.Plaintext)
	if err := r.begin(); err != nil {
		return resources.StoredSecret{}, err
	}
	defer r.end()
	if ctx == nil || tx == nil || !r.validDecisionSecond(input.CreatedAt) ||
		!validConnectorType(input.ConnectorType) || !validBaseURLText(input.CanonicalBaseURL) ||
		!validCredential(input.Plaintext) {
		return resources.StoredSecret{}, ErrInvalidInput
	}
	if ctx.Err() != nil {
		return resources.StoredSecret{}, ErrInterrupted
	}
	if !r.canonicalBaseURL(input.CanonicalBaseURL) {
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

	fingerprint := r.fingerprint(input.ConnectorType, input.CanonicalBaseURL, input.Plaintext)
	head, tail := displayFragments(input.Plaintext)
	ciphertext, err := r.vault.SealForGenerationTwoContext(input.Plaintext, credentialContext)
	if err != nil {
		return resources.StoredSecret{}, ErrUnavailable
	}
	defer func() { ciphertext = "" }()
	result, err := tx.ExecContext(ctx, `INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at,orphaned_at)
VALUES(?,?,?,?,?,NULL)`, rowContextID, input.CanonicalBaseURL, input.ConnectorType, ciphertext, input.CreatedAt)
	if err != nil {
		return resources.StoredSecret{}, ErrUnavailable
	}
	refID, err := result.LastInsertId()
	if err != nil || refID <= 0 {
		return resources.StoredSecret{}, ErrUnavailable
	}
	return resources.StoredSecret{
		RefID:       refID,
		Fingerprint: fingerprint,
		DisplayHead: head,
		DisplayTail: tail,
	}, nil
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
AND NOT EXISTS(SELECT 1 FROM endpoint_keys k WHERE k.secret_ref_id=endpoint_key_secrets.id)
AND NOT EXISTS(SELECT 1 FROM dispatch_claims c WHERE c.secret_ref_id=endpoint_key_secrets.id
               AND c.state IN ('claimed','dispatched'))`, at, refID); err != nil {
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
	switch connectorcontract.Type(value) {
	case connectorcontract.TypeOpenAICompatible, connectorcontract.TypeAnthropicCompatible:
		return true
	default:
		return false
	}
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
