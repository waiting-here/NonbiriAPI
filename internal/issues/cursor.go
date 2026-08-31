package issues

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
)

const (
	issueCursorVersion    = 1
	maxCursorBytes        = 512
	cursorLifetimeSeconds = int64(24 * 60 * 60)
)

type issueCursorPayload struct {
	Version  int    `json:"v"`
	Scope    string `json:"s"`
	Owner    string `json:"o"`
	Expiry   int64  `json:"e"`
	LastSeen int64  `json:"l"`
	ID       string `json:"i"`
}

type cursorCodec struct {
	keys CursorKeyDeriver
}

func (codec cursorCodec) encode(payload issueCursorPayload) (string, error) {
	key, err := codec.derive("pagination-cursor/v1")
	if err != nil {
		return "", err
	}
	defer clear(key)
	if !validIssueCursor(payload) {
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

func (codec cursorCodec) decode(token, scope, owner string, now int64) (issueCursorPayload, error) {
	var payload issueCursorPayload
	if token == "" || len(token) > maxCursorBytes {
		return payload, ErrInvalidRequest
	}
	key, err := codec.derive("pagination-cursor/v1")
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
		!validIssueCursor(payload) || payload.Scope != scope || payload.Owner != owner || payload.Expiry <= now {
		return issueCursorPayload{}, ErrInvalidRequest
	}
	canonical, err := codec.encode(payload)
	if err != nil || canonical != token {
		return issueCursorPayload{}, ErrInvalidRequest
	}
	return payload, nil
}

func (codec cursorCodec) derive(info string) ([]byte, error) {
	if isNilInterface(codec.keys) {
		return nil, ErrUnavailable
	}
	key, err := codec.keys.DeriveGenerationTwoSubkey([]byte(info))
	if err != nil || len(key) != sha256.Size {
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}

func validIssueCursor(payload issueCursorPayload) bool {
	return payload.Version == issueCursorVersion && payload.Scope != "" && payload.Owner != "" &&
		payload.Expiry > 0 && payload.Expiry <= maxUnixSecond && payload.LastSeen >= 0 && payload.LastSeen <= maxUnixSecond &&
		validateIssueID(payload.ID)
}

func issueCursorOwner(userID int64, state string) string {
	return strconv.FormatInt(userID, 10) + ":" + state
}
