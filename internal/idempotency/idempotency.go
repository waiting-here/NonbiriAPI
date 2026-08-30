// Package idempotency provides the transaction-local replay primitive shared
// by control-plane mutations. Authentication and authorization deliberately
// remain outside this package and must run before every call to Begin,
// including a replay.
package idempotency

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strings"
)

const (
	ReplayWindowSeconds = int64(24 * 60 * 60)
	MaxResponseBytes    = 64 * 1024
	MaxControlBodyBytes = 256 * 1024
	maxUnixSecond       = int64(253402300799)
)

type Scope string

const (
	ScopeCredentialReport       Scope = "credential_report"
	ScopeControlMutation        Scope = "control_mutation"
	ScopeOpenAIChatCompletions  Scope = "openai_chat_completions"
	ScopeCharityChatCompletions Scope = "charity_chat_completions"
	ScopeModelDiscovery         Scope = "model_discovery"
	ScopeMaintenance            Scope = "maintenance"
	ScopeAnnouncement           Scope = "announcement"
	ScopeActivity               Scope = "activity"
	ScopeGameFishing            Scope = "game_fishing"
	ScopeGameLinkLink           Scope = "game_linklink"
	ScopeGameRPS                Scope = "game_rps"
	ScopeDonation               Scope = "donation"
)

var (
	ErrConflict   = errors.New("idempotency key was already used for a different request")
	ErrInProgress = errors.New("idempotent request is still in progress")
	ErrState      = errors.New("idempotency record has an invalid state")
)

var validScopes = map[Scope]struct{}{
	ScopeCredentialReport:       {},
	ScopeControlMutation:        {},
	ScopeOpenAIChatCompletions:  {},
	ScopeCharityChatCompletions: {},
	ScopeModelDiscovery:         {},
	ScopeMaintenance:            {},
	ScopeAnnouncement:           {},
	ScopeActivity:               {},
	ScopeGameFishing:            {},
	ScopeGameLinkLink:           {},
	ScopeGameRPS:                {},
	ScopeDonation:               {},
}

// DigestInput contains canonical, already-authorized request facts. Body must
// be the canonical encoding of the strict-decoded DTO, not the raw HTTP body.
// Query must be the route owner's canonical query encoding without a leading
// question mark.
type DigestInput struct {
	ActorScopeHash  [32]byte
	Method          string
	Route           string
	PathResourceIDs []string
	Query           string
	Body            []byte
}

// CanonicalJSON encodes a strict-decoded DTO for DigestInput.Body. Strict
// decoding itself remains the route owner's responsibility.
func CanonicalJSON(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical idempotency body: %w", err)
	}
	if len(body) > MaxControlBodyBytes {
		return nil, fmt.Errorf("canonical idempotency body exceeds %d bytes", MaxControlBodyBytes)
	}
	return body, nil
}

// ActorScopeHash returns the irreversible lookup identity for one canonical
// actor kind and ID. The kind keeps otherwise identical user and
// administrator identifiers in separate namespaces.
func ActorScopeHash(kind, canonicalID string) ([32]byte, error) {
	if kind == "" || canonicalID == "" {
		return [32]byte{}, errors.New("actor kind and canonical ID are required")
	}
	return framedDigest("NonbiriAPI/idempotency-actor/v1", []byte(kind), []byte(canonicalID)), nil
}

// KeyHash validates the public Idempotency-Key grammar and returns the only
// key material persisted by the replay store.
func KeyHash(key string) ([32]byte, error) {
	if len(key) < 22 || len(key) > 128 {
		return [32]byte{}, errors.New("idempotency key must contain 22 to 128 URL-safe ASCII characters")
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			continue
		}
		return [32]byte{}, errors.New("idempotency key must contain 22 to 128 URL-safe ASCII characters")
	}
	return sha256.Sum256([]byte(key)), nil
}

// RequestDigest binds every control-plane routing dimension to the canonical
// strict-decoded payload. Route is the registered route template; concrete
// resource IDs are carried separately in path order.
func RequestDigest(input DigestInput) ([32]byte, error) {
	if input.Method == "" || input.Method != strings.ToUpper(input.Method) {
		return [32]byte{}, errors.New("canonical HTTP method is required")
	}
	if input.Route == "" || input.Route[0] != '/' || strings.ContainsAny(input.Route, "?#") {
		return [32]byte{}, errors.New("canonical route must be an absolute path template without query or fragment")
	}
	if len(input.Body) > MaxControlBodyBytes {
		return [32]byte{}, fmt.Errorf("canonical idempotency body exceeds %d bytes", MaxControlBodyBytes)
	}
	h := sha256.New()
	writeField(h, []byte("NonbiriAPI/control-mutation-request/v1"))
	writeField(h, input.ActorScopeHash[:])
	writeField(h, []byte(input.Method))
	writeField(h, []byte(input.Route))
	writeUint64(h, uint64(len(input.PathResourceIDs)))
	for _, id := range input.PathResourceIDs {
		writeField(h, []byte(id))
	}
	writeField(h, []byte(input.Query))
	writeField(h, input.Body)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

type DecisionKind uint8

const (
	Proceed DecisionKind = iota + 1
	Replay
)

type token struct {
	scope       Scope
	actorHash   [32]byte
	keyHash     [32]byte
	requestHash [32]byte
}

type Decision struct {
	Kind         DecisionKind
	HTTPStatus   int
	ResponseBody []byte
	token        token
}

type BeginInput struct {
	Scope       Scope
	ActorHash   [32]byte
	Key         string
	RequestHash [32]byte
	DecisionNow int64
}

// Begin reserves a replay identity inside the caller's business transaction,
// or returns the completed safe response. The caller must authenticate and
// authorize before opening this transaction and calling Begin.
func Begin(ctx context.Context, tx *sql.Tx, input BeginInput) (Decision, error) {
	if tx == nil {
		return Decision{}, errors.New("idempotency transaction is required")
	}
	if _, ok := validScopes[input.Scope]; !ok {
		return Decision{}, fmt.Errorf("unknown idempotency scope %q", input.Scope)
	}
	if input.Scope == ScopeCredentialReport {
		return Decision{}, errors.New("credential report uses its dedicated acceptance rail")
	}
	if input.DecisionNow < 0 || input.DecisionNow > maxUnixSecond-ReplayWindowSeconds {
		return Decision{}, errors.New("idempotency decision time is outside the supported UTC range")
	}
	keyHash, err := KeyHash(input.Key)
	if err != nil {
		return Decision{}, err
	}
	recordToken := token{scope: input.Scope, actorHash: input.ActorHash, keyHash: keyHash, requestHash: input.RequestHash}

	if _, err := tx.ExecContext(ctx, `
DELETE FROM idempotency_records
WHERE scope=? AND actor_scope_hash=? AND key_hash=? AND expires_at<=?`,
		string(input.Scope), input.ActorHash[:], keyHash[:], input.DecisionNow); err != nil {
		return Decision{}, fmt.Errorf("expire idempotency record: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at
) VALUES(?,?,?,?,'accepted',0,?, ?,?)
ON CONFLICT(scope,actor_scope_hash,key_hash) DO NOTHING`,
		string(input.Scope), input.ActorHash[:], keyHash[:], input.RequestHash[:], []byte{}, input.DecisionNow, input.DecisionNow+ReplayWindowSeconds)
	if err != nil {
		return Decision{}, fmt.Errorf("accept idempotency record: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return Decision{}, fmt.Errorf("observe idempotency acceptance: %w", err)
	}
	if inserted == 1 {
		return Decision{Kind: Proceed, token: recordToken}, nil
	}

	var storedHash []byte
	var state string
	var status int
	var body []byte
	if err := tx.QueryRowContext(ctx, `
SELECT request_hash,state,http_status,response_body
FROM idempotency_records
WHERE scope=? AND actor_scope_hash=? AND key_hash=?`,
		string(input.Scope), input.ActorHash[:], keyHash[:]).Scan(&storedHash, &state, &status, &body); err != nil {
		return Decision{}, fmt.Errorf("read idempotency record: %w", err)
	}
	if len(storedHash) != sha256.Size || !equalHash(storedHash, input.RequestHash) {
		return Decision{}, ErrConflict
	}
	switch state {
	case "accepted":
		return Decision{}, ErrInProgress
	case "completed":
		return Decision{Kind: Replay, HTTPStatus: status, ResponseBody: append([]byte(nil), body...), token: recordToken}, nil
	default:
		return Decision{}, ErrState
	}
}

// Complete stores the safe response in the same transaction as the domain
// mutation. A token is issued only by a successful Proceed decision.
func Complete(ctx context.Context, tx *sql.Tx, decision Decision, status int, body []byte) error {
	if tx == nil {
		return errors.New("idempotency transaction is required")
	}
	if decision.Kind != Proceed || decision.token.scope == "" {
		return errors.New("a proceed decision is required")
	}
	if status < 100 || status > 599 {
		return errors.New("idempotent response status must be between 100 and 599")
	}
	if len(body) > MaxResponseBytes {
		return fmt.Errorf("idempotent response exceeds %d bytes", MaxResponseBytes)
	}
	t := decision.token
	storedBody := append([]byte{}, body...)
	result, err := tx.ExecContext(ctx, `
UPDATE idempotency_records
SET state='completed',http_status=?,response_body=?
WHERE scope=? AND actor_scope_hash=? AND key_hash=? AND request_hash=? AND state='accepted'`,
		status, storedBody, string(t.scope), t.actorHash[:], t.keyHash[:], t.requestHash[:])
	if err != nil {
		return fmt.Errorf("complete idempotency record: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("observe idempotency completion: %w", err)
	}
	if updated != 1 {
		return ErrState
	}
	return nil
}

func framedDigest(domain string, fields ...[]byte) [32]byte {
	h := sha256.New()
	writeField(h, []byte(domain))
	for _, field := range fields {
		writeField(h, field)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func writeField(h hash.Hash, field []byte) {
	writeUint64(h, uint64(len(field)))
	_, _ = h.Write(field)
}

func writeUint64(h hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = h.Write(encoded[:])
}

func equalHash(stored []byte, expected [32]byte) bool {
	var diff byte
	for i := range expected {
		diff |= stored[i] ^ expected[i]
	}
	return diff == 0
}
