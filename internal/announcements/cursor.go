package announcements

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

const (
	announcementCursorVersion = 1
	maxCursorBytes            = 512
	cursorLifetimeSeconds     = int64(24 * 60 * 60)
)

type announcementCursorPayload struct {
	Version int    `json:"v"`
	Scope   string `json:"s"`
	Owner   string `json:"o"`
	Expiry  int64  `json:"e"`
	Pinned  int    `json:"p"`
	Time    int64  `json:"t"`
	ID      string `json:"i"`
}

type cursorCodec struct {
	keys CursorKeyDeriver
}

func (codec cursorCodec) encode(payload announcementCursorPayload) (string, error) {
	key, err := codec.derive()
	if err != nil {
		return "", err
	}
	defer clear(key)
	if !validCursorPayload(payload) {
		return "", ErrInvalidRequest
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", ErrUnavailable
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(body)
	raw := append(body, mac.Sum(nil)...)
	token := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	if len(token) > maxCursorBytes {
		return "", ErrInvalidRequest
	}
	return token, nil
}

func (codec cursorCodec) decode(token, scope, owner string, now int64) (announcementCursorPayload, error) {
	var payload announcementCursorPayload
	if token == "" || len(token) > maxCursorBytes || scope == "" || owner == "" {
		return payload, ErrInvalidRequest
	}
	key, err := codec.derive()
	if err != nil {
		return payload, err
	}
	defer clear(key)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) <= sha256.Size {
		clear(raw)
		return payload, ErrInvalidRequest
	}
	defer clear(raw)
	body := raw[:len(raw)-sha256.Size]
	provided := raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(body)
	if !hmac.Equal(provided, mac.Sum(nil)) || json.Unmarshal(body, &payload) != nil ||
		!validCursorPayload(payload) || payload.Scope != scope || payload.Owner != owner || payload.Expiry <= now {
		return announcementCursorPayload{}, ErrInvalidRequest
	}
	canonical, err := codec.encode(payload)
	if err != nil || canonical != token {
		return announcementCursorPayload{}, ErrInvalidRequest
	}
	return payload, nil
}

func (codec cursorCodec) derive() ([]byte, error) {
	if isNilInterface(codec.keys) {
		return nil, ErrUnavailable
	}
	key, err := codec.keys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil || len(key) != sha256.Size {
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}

func validCursorPayload(payload announcementCursorPayload) bool {
	return payload.Version == announcementCursorVersion && payload.Scope != "" && payload.Owner != "" &&
		utf8.ValidString(payload.Scope) && utf8.ValidString(payload.Owner) && payload.Expiry > 0 &&
		payload.Expiry <= maxUnixSecond && (payload.Pinned == -1 || payload.Pinned == 0 || payload.Pinned == 1) &&
		payload.Time >= 0 && payload.Time <= maxUnixSecond &&
		(payload.ID == "" || dbAnnouncementID(payload.ID))
}

func userCursorOwner(userID int64, language string) string {
	return strconv.FormatInt(userID, 10) + ":" + language
}
