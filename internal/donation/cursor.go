package donation

import (
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const cursorLifetime = int64(24 * 60 * 60)

func (s *Service) encodeCursor(scope, owner string, now, id int64) (string, error) {
	if s == nil || nilDependency(s.cursorKeys) || id <= 0 {
		return "", ErrUnavailable
	}
	key, err := s.cursorKeys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil || len(key) != 32 {
		clear(key)
		return "", ErrUnavailable
	}
	defer clear(key)
	token, err := db.EncodePaginationCursorWithDerivedKey(key, scope, owner, uint64(now+cursorLifetime), []db.CursorAtom{{Kind: db.CursorUint, Uint: uint64(id)}})
	if err != nil {
		return "", ErrUnavailable
	}
	return token, nil
}

func (s *Service) decodeCursor(token, scope, owner string, now int64) (int64, error) {
	if token == "" {
		return 0, nil
	}
	if s == nil || nilDependency(s.cursorKeys) {
		return 0, ErrUnavailable
	}
	key, err := s.cursorKeys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil || len(key) != 32 {
		clear(key)
		return 0, ErrUnavailable
	}
	defer clear(key)
	decoded, err := db.DecodePaginationCursorWithDerivedKey(key, token, scope, owner, uint64(now))
	if err != nil || len(decoded.Atoms) != 1 || decoded.Atoms[0].Kind != db.CursorUint || decoded.Atoms[0].Uint > uint64(^uint64(0)>>1) {
		return 0, ErrInvalidRequest
	}
	return int64(decoded.Atoms[0].Uint), nil
}

func pageOwner(userID int64, suffix string) string {
	return strconv.FormatInt(userID, 10) + ":" + suffix
}

var _ = errors.Is
