package charityrouting

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const charityCursorLifetime = int64(24 * 60 * 60)

func (s *Service) cursorKey() ([]byte, error) {
	if s == nil || nilDependency(s.cursorKeys) {
		return nil, ErrUnavailable
	}
	key, err := s.cursorKeys.DeriveGenerationTwoSubkey([]byte("charity-routing-pagination/v1"))
	if err != nil || len(key) != sha256.Size {
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}

func (s *Service) encodeModelCursor(scope, owner string, now, modelID int64) (string, error) {
	if modelID <= 0 {
		return "", ErrInvalidRequest
	}
	key, err := s.cursorKey()
	if err != nil {
		return "", err
	}
	defer clear(key)
	token, err := db.EncodePaginationCursor(key, scope, owner, uint64(now+charityCursorLifetime),
		[]db.CursorAtom{{Kind: db.CursorUint, Uint: uint64(modelID)}})
	if err != nil {
		return "", ErrUnavailable
	}
	return token, nil
}

func (s *Service) decodeModelCursor(token, scope, owner string, now int64) (int64, error) {
	if token == "" {
		return 0, nil
	}
	key, err := s.cursorKey()
	if err != nil {
		return 0, err
	}
	defer clear(key)
	decoded, err := db.DecodePaginationCursor(key, token, scope, owner, uint64(now))
	if err != nil || len(decoded.Atoms) != 1 || decoded.Atoms[0].Kind != db.CursorUint || decoded.Atoms[0].Uint > uint64(^uint64(0)>>1) {
		return 0, ErrInvalidRequest
	}
	return int64(decoded.Atoms[0].Uint), nil
}

func (s *Service) encodeCandidateCursor(scope, owner string, now, keyID int64, modelID string) (string, error) {
	if keyID <= 0 || modelID == "" {
		return "", ErrInvalidRequest
	}
	key, err := s.cursorKey()
	if err != nil {
		return "", err
	}
	defer clear(key)
	token, err := db.EncodePaginationCursor(key, scope, owner, uint64(now+charityCursorLifetime), []db.CursorAtom{
		{Kind: db.CursorUint, Uint: uint64(keyID)}, {Kind: db.CursorText, Text: modelID},
	})
	if err != nil {
		return "", ErrUnavailable
	}
	return token, nil
}

func (s *Service) decodeCandidateCursor(token, scope, owner string, now int64) (int64, string, error) {
	if token == "" {
		return 0, "", nil
	}
	key, err := s.cursorKey()
	if err != nil {
		return 0, "", err
	}
	defer clear(key)
	decoded, err := db.DecodePaginationCursor(key, token, scope, owner, uint64(now))
	if err != nil || len(decoded.Atoms) != 2 || decoded.Atoms[0].Kind != db.CursorUint ||
		decoded.Atoms[0].Uint > uint64(^uint64(0)>>1) || decoded.Atoms[1].Kind != db.CursorText || decoded.Atoms[1].Text == "" {
		return 0, "", ErrInvalidRequest
	}
	return int64(decoded.Atoms[0].Uint), decoded.Atoms[1].Text, nil
}

func paginationOwner(role roleKind, actorID int64, parts ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(role))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.FormatInt(actorID, 10)))
	for _, part := range parts {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part))
	}
	return string(role) + ":" + hex.EncodeToString(hash.Sum(nil))
}

func boolFilter(value *bool) string {
	if value == nil {
		return ""
	}
	return strconv.FormatBool(*value)
}

func candidateFilterOwner(role roleKind, actorID, modelID, donationID, donationKeyID int64, source, query string) string {
	return paginationOwner(role, actorID, strconv.FormatInt(modelID, 10), strconv.FormatInt(donationID, 10),
		strconv.FormatInt(donationKeyID, 10), source, query)
}
