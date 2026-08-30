package resources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const callerKeyPrefix = "nbk_"

func (r *Repository) GetCallerKey(ctx context.Context, userID int64) (CallerKeyState, error) {
	if r == nil || userID <= 0 {
		return CallerKeyState{}, ErrNotFound
	}
	var generation int64
	var hash []byte
	var head, tail string
	var created sql.NullInt64
	var updated int64
	err := r.db.QueryRowContext(ctx, `
SELECT generation,key_hash,display_head,display_tail,key_created_at,updated_at
FROM caller_keys WHERE user_id=?`, userID).Scan(&generation, &hash, &head, &tail, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return CallerKeyState{}, ErrNotFound
	}
	if err != nil {
		return CallerKeyState{}, fmt.Errorf("resources: read caller key: %w", err)
	}
	if generation < 0 {
		return CallerKeyState{}, ErrUnavailable
	}
	state := CallerKeyState{Generation: strconv.FormatInt(generation, 10)}
	if hash == nil {
		if head != "" || tail != "" || created.Valid {
			return CallerKeyState{}, ErrUnavailable
		}
		return state, nil
	}
	if len(hash) != sha256.Size || !created.Valid || len(head) != 4 || len(tail) != 4 {
		return CallerKeyState{}, ErrUnavailable
	}
	state.Metadata = &CallerKeyMetadata{
		Display:   callerKeyPrefix + head + "…" + tail,
		CreatedAt: created.Int64, UpdatedAt: updated, Generation: state.Generation,
	}
	return state, nil
}

func (r *Repository) RegenerateCallerKey(ctx context.Context, userID, expectedGeneration int64) (CallerKeySecret, error) {
	if r == nil || userID <= 0 || expectedGeneration < 0 || expectedGeneration == int64(^uint64(0)>>1) {
		return CallerKeySecret{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return CallerKeySecret{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return CallerKeySecret{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	var currentGeneration int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM caller_keys WHERE user_id=?`, userID).Scan(&currentGeneration); errors.Is(err, sql.ErrNoRows) {
		return CallerKeySecret{}, ErrNotFound
	} else if err != nil {
		return CallerKeySecret{}, fmt.Errorf("resources: read caller key generation: %w", err)
	}
	if currentGeneration != expectedGeneration {
		return CallerKeySecret{}, ErrConflict
	}
	var random [32]byte
	if _, err := io.ReadFull(r.random, random[:]); err != nil {
		clear(random[:])
		return CallerKeySecret{}, ErrUnavailable
	}
	body := base64.RawURLEncoding.EncodeToString(random[:])
	clear(random[:])
	if len(body) != 43 {
		return CallerKeySecret{}, ErrUnavailable
	}
	secretText := callerKeyPrefix + body
	digest := sha256.Sum256([]byte(secretText))
	head, tail := body[:4], body[len(body)-4:]

	result, err := tx.ExecContext(ctx, `
UPDATE caller_keys
SET generation=generation+1,key_hash=?,display_head=?,display_tail=?,key_created_at=?,updated_at=?
WHERE user_id=? AND generation=?`, digest[:], head, tail, now, now, userID, expectedGeneration)
	if err != nil {
		return CallerKeySecret{}, fmt.Errorf("resources: replace caller key: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return CallerKeySecret{}, fmt.Errorf("resources: observe caller key replacement: %w", err)
	}
	if updated != 1 {
		return CallerKeySecret{}, ErrConflict
	}
	if err := commitTx(tx, &committed); err != nil {
		return CallerKeySecret{}, err
	}
	generation := strconv.FormatInt(expectedGeneration+1, 10)
	return CallerKeySecret{
		Secret: secretText,
		Metadata: CallerKeyMetadata{
			Display:   callerKeyPrefix + head + "…" + tail,
			CreatedAt: now, UpdatedAt: now, Generation: generation,
		},
	}, nil
}

func (r *Repository) RevokeCallerKey(ctx context.Context, userID, expectedGeneration int64) (string, error) {
	if r == nil || userID <= 0 || expectedGeneration < 0 || expectedGeneration == int64(^uint64(0)>>1) {
		return "", ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return "", err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return "", err
	}
	committed := false
	defer finishTx(tx, &committed)
	var currentGeneration int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM caller_keys WHERE user_id=?`, userID).Scan(&currentGeneration); errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", fmt.Errorf("resources: read caller key generation: %w", err)
	}
	if currentGeneration != expectedGeneration {
		return "", ErrConflict
	}
	result, err := tx.ExecContext(ctx, `
UPDATE caller_keys
SET generation=generation+1,key_hash=NULL,display_head='',display_tail='',key_created_at=NULL,updated_at=?
WHERE user_id=? AND generation=?`, now, userID, expectedGeneration)
	if err != nil {
		return "", fmt.Errorf("resources: revoke caller key: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("resources: observe caller key revocation: %w", err)
	}
	if updated != 1 {
		return "", ErrConflict
	}
	if err := commitTx(tx, &committed); err != nil {
		return "", err
	}
	return strconv.FormatInt(expectedGeneration+1, 10), nil
}

type CallerIdentity struct {
	UserID     int64
	Generation int64
}

func (r *Repository) ResolveCallerKey(ctx context.Context, presented string) (CallerIdentity, error) {
	if r == nil || !validCallerKey(presented) {
		return CallerIdentity{}, ErrNotFound
	}
	now, err := r.nowUnix()
	if err != nil {
		return CallerIdentity{}, err
	}
	digest := sha256.Sum256([]byte(presented))
	var identity CallerIdentity
	err = r.db.QueryRowContext(ctx, `
SELECT c.user_id,c.generation
FROM caller_keys c JOIN users u ON u.id=c.user_id
WHERE c.key_hash=? AND u.is_admin=0
  AND (u.is_banned=0 OR (u.banned_until IS NOT NULL AND u.banned_until<=?))`, digest[:], now).Scan(&identity.UserID, &identity.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return CallerIdentity{}, ErrNotFound
	}
	if err != nil {
		return CallerIdentity{}, fmt.Errorf("resources: resolve caller key: %w", err)
	}
	return identity, nil
}

func validCallerKey(value string) bool {
	if len(value) != len(callerKeyPrefix)+43 || value[:len(callerKeyPrefix)] != callerKeyPrefix {
		return false
	}
	body := value[len(callerKeyPrefix):]
	decoded, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || len(decoded) != 32 {
		clear(decoded)
		return false
	}
	canonical := base64.RawURLEncoding.EncodeToString(decoded) == body
	clear(decoded)
	return canonical
}
