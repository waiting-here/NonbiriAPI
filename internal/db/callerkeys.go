package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CallerKeyPrefix identifies the one platform caller key issued to a normal
// user. The key is not an upstream credential.
const CallerKeyPrefix = "nbk_"

const (
	callerKeyRandomBytes  = 32
	callerKeyEncodedBytes = 43 // RawURLEncoding.EncodedLen(callerKeyRandomBytes)
	callerKeyDisplayPart  = 4
	maxCallerKeyBytes     = len(CallerKeyPrefix) + callerKeyEncodedBytes
)

// CallerKey is metadata safe to return from list/detail endpoints. Secret is
// intentionally absent; it cannot be recovered after generation.
type CallerKey struct {
	UserID      int64
	DisplayHead string
	DisplayTail string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CallerKeyGeneration is returned only by a regeneration operation. Secret is
// the sole plaintext exposure and callers must write it once in a no-store
// response without logging, caching, or persisting it.
type CallerKeyGeneration struct {
	Secret    string
	Display   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func hashCallerKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func generateCallerKeyMaterial() (string, error) {
	raw := make([]byte, callerKeyRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		clear(raw)
		return "", fmt.Errorf("generate caller key")
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	return CallerKeyPrefix + encoded, nil
}

func validateCallerKey(key string) bool {
	if !strings.HasPrefix(key, CallerKeyPrefix) || len(key) != maxCallerKeyBytes {
		return false
	}
	encoded := strings.TrimPrefix(key, CallerKeyPrefix)
	for i := 0; i < len(encoded); i++ {
		c := encoded[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != callerKeyRandomBytes {
		clear(decoded)
		return false
	}
	clear(decoded)
	return true
}

func callerKeyDisplay(secret string) (head, tail, display string) {
	secretPart := strings.TrimPrefix(secret, CallerKeyPrefix)
	head = secretPart[:callerKeyDisplayPart]
	tail = secretPart[len(secretPart)-callerKeyDisplayPart:]
	display = CallerKeyPrefix + head + "…" + tail
	return head, tail, display
}

// RegenerateCallerKey atomically replaces the one row for a normal user. The
// previous hash stops matching as soon as this transaction commits; no
// encrypted or reversible plaintext copy is written.
func (s *Store) RegenerateCallerKey(userID int64) (CallerKeyGeneration, error) {
	if userID <= 0 {
		return CallerKeyGeneration{}, ErrNotFound
	}
	secret, err := generateCallerKeyMaterial()
	if err != nil {
		return CallerKeyGeneration{}, err
	}
	head, tail, display := callerKeyDisplay(secret)
	now := time.Now().UTC()

	tx, err := s.db.Begin()
	if err != nil {
		return CallerKeyGeneration{}, fmt.Errorf("regenerate caller key")
	}
	defer tx.Rollback()
	var isAdmin, isBanned int
	if err := tx.QueryRow(`SELECT is_admin, is_banned FROM users WHERE id=?`, userID).Scan(&isAdmin, &isBanned); errors.Is(err, sql.ErrNoRows) {
		return CallerKeyGeneration{}, ErrNotFound
	} else if err != nil {
		return CallerKeyGeneration{}, fmt.Errorf("regenerate caller key")
	}
	if isAdmin != 0 {
		return CallerKeyGeneration{}, ErrAdminProtected
	}
	if isBanned != 0 {
		return CallerKeyGeneration{}, ErrBanned
	}
	_, err = tx.Exec(`INSERT INTO caller_keys
		(user_id, key_hash, display_head, display_tail, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			key_hash=excluded.key_hash,
			display_head=excluded.display_head,
			display_tail=excluded.display_tail,
			updated_at=excluded.updated_at`,
		userID, hashCallerKey(secret), head, tail, now.Unix(), now.Unix())
	if err != nil {
		return CallerKeyGeneration{}, fmt.Errorf("regenerate caller key")
	}
	if err := tx.Commit(); err != nil {
		return CallerKeyGeneration{}, fmt.Errorf("regenerate caller key")
	}
	return CallerKeyGeneration{Secret: secret, Display: display, CreatedAt: now, UpdatedAt: now}, nil
}

// SetCallerKey is the concise generation entry point used by handlers.
func (s *Store) SetCallerKey(userID int64) (string, error) {
	generation, err := s.RegenerateCallerKey(userID)
	if err != nil {
		return "", err
	}
	return generation.Secret, nil
}

// GetCallerKey returns metadata only. It never returns the secret.
func (s *Store) GetCallerKey(userID int64) (*CallerKey, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	var key CallerKey
	var createdAt, updatedAt int64
	err := s.db.QueryRow(`SELECT user_id, display_head, display_tail, created_at, updated_at
		FROM caller_keys WHERE user_id=?`, userID).Scan(&key.UserID, &key.DisplayHead, &key.DisplayTail, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read caller key metadata")
	}
	key.CreatedAt = time.Unix(createdAt, 0).UTC()
	key.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &key, nil
}

// GetUserByCallerKey resolves a bearer key by SHA-256 lookup and checks ban
// state in the same query. The plaintext is never a SQL value.
func (s *Store) GetUserByCallerKey(key string) (*User, error) {
	if !validateCallerKey(key) {
		return nil, nil
	}
	user, err := scanUser(s.db.QueryRow(`SELECT `+userSelectColumns+`
		FROM caller_keys c JOIN users u ON u.id=c.user_id
		WHERE c.key_hash=? AND u.is_admin=0 AND u.is_banned=0`, hashCallerKey(key)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return user, err
}

// CallerKeyExists reports whether a user has a currently stored key row.
func (s *Store) CallerKeyExists(userID int64) (bool, error) {
	if userID <= 0 {
		return false, ErrNotFound
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM caller_keys WHERE user_id=?`, userID).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}

// DeleteCallerKey invalidates the current key. It is idempotent.
func (s *Store) DeleteCallerKey(userID int64) error {
	if userID <= 0 {
		return ErrNotFound
	}
	if _, err := s.db.Exec(`DELETE FROM caller_keys WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("delete caller key")
	}
	return nil
}
