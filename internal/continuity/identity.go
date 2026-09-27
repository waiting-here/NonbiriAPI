// Package continuity owns the minimum identity-bound eligibility and live
// windows that survive account deletion. Its keys are private, linkable data
// and must never be included in public account or game projections.
package continuity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"sync"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid        = errors.New("continuity: invalid input")
	ErrNotFound       = errors.New("continuity: account not found")
	ErrInvariant      = errors.New("continuity: identity invariant failed")
	ErrClosed         = errors.New("continuity: service closed")
	ErrAlreadyClaimed = errors.New("continuity: eligibility already consumed")
)

const maxUnixSecond int64 = 253402300799

type Key [sha256.Size]byte

type SubkeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

type Service struct {
	database *sql.DB
	mu       sync.RWMutex
	secret   [sha256.Size]byte
	closed   bool
}

func New(database *sql.DB, deriver SubkeyDeriver) (*Service, error) {
	if database == nil || deriver == nil {
		return nil, ErrInvalid
	}
	material, err := deriver.DeriveGenerationTwoSubkey([]byte("discord-continuity-v1"))
	defer clear(material)
	if err != nil || len(material) != sha256.Size {
		return nil, ErrInvariant
	}
	service := &Service{database: database}
	copy(service.secret[:], material)
	return service, nil
}

// KeyForDiscord uses the exact provider identity accepted by authentication.
// It never folds case, trims an ID, or substitutes a nickname.
func (service *Service) KeyForDiscord(discordID string) (Key, error) {
	if service == nil || len(discordID) == 0 || len(discordID) > 128 || !utf8.ValidString(discordID) {
		return Key{}, ErrInvalid
	}
	for _, char := range discordID {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return Key{}, ErrInvalid
		}
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.closed {
		return Key{}, ErrClosed
	}
	mac := hmac.New(sha256.New, service.secret[:])
	_, _ = mac.Write([]byte("discord-continuity-v1\x00"))
	_, _ = mac.Write([]byte(discordID))
	var key Key
	copy(key[:], mac.Sum(nil))
	return key, nil
}

// UserKeyTx checks the current account in the caller's transaction. Missing
// bindings can be derived during bounded startup backfill. Conflicting stored
// bindings fail closed; a deleted account cannot attach late work to a new one.
func (service *Service) UserKeyTx(ctx context.Context, tx *sql.Tx, userID int64) (Key, error) {
	if service == nil || ctx == nil || tx == nil || userID <= 0 {
		return Key{}, ErrInvalid
	}
	var discordID string
	var stored []byte
	err := tx.QueryRowContext(ctx, `SELECT u.discord_id,c.identity_key FROM users u
LEFT JOIN user_continuity_identities c ON c.user_id=u.id WHERE u.id=? AND u.is_admin=0`, userID).Scan(&discordID, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	if err != nil {
		return Key{}, err
	}
	key, err := service.KeyForDiscord(discordID)
	if err != nil {
		return Key{}, err
	}
	if stored != nil && (len(stored) != len(key) || subtle.ConstantTimeCompare(stored, key[:]) != 1) {
		return Key{}, ErrInvariant
	}
	return key, nil
}

func (service *Service) BindUserTx(ctx context.Context, tx *sql.Tx, userID int64) (Key, error) {
	key, err := service.UserKeyTx(ctx, tx, userID)
	if err != nil {
		return Key{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_continuity_identities(user_id,identity_key)
VALUES(?,?) ON CONFLICT(user_id) DO NOTHING`, userID, key[:])
	return key, err
}

func (service *Service) UserKey(ctx context.Context, userID int64) (Key, error) {
	if service == nil || ctx == nil {
		return Key{}, ErrInvalid
	}
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Key{}, err
	}
	defer tx.Rollback()
	key, err := service.UserKeyTx(ctx, tx, userID)
	if err != nil {
		return Key{}, err
	}
	return key, tx.Commit()
}

// BackfillBatch binds only extant accounts, never inventing deleted history.
// The caller owns the overall startup budget. A failed batch returns its input
// cursor, so a retry cannot skip rows whose transaction was rolled back.
func (service *Service) BackfillBatch(ctx context.Context, lastID int64, limit int) (int64, int, error) {
	if service == nil || ctx == nil || lastID < 0 || limit < 1 || limit > 1000 {
		return lastID, 0, ErrInvalid
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return lastID, 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM users WHERE is_admin=0 AND id>? ORDER BY id LIMIT ?`, lastID, limit)
	if err != nil {
		return lastID, 0, err
	}
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return lastID, 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return lastID, 0, err
	}
	next := lastID
	for _, id := range ids {
		if _, err := service.BindUserTx(ctx, tx, id); err != nil {
			return lastID, 0, err
		}
		next = id
	}
	if err := tx.Commit(); err != nil {
		return lastID, 0, err
	}
	return next, len(ids), nil
}

func (service *Service) Close() error {
	if service == nil {
		return nil
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.closed = true
	clear(service.secret[:])
	return nil
}
