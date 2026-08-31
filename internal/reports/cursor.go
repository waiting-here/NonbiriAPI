package reports

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math"
	"sync"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const cursorVersion = byte(1)

type cursorCodec struct {
	mu     sync.RWMutex
	key    [32]byte
	closed bool
}

func newCursorCodec(deriver GenerationTwoSubkeyDeriver) (*cursorCodec, error) {
	if deriver == nil {
		return nil, ErrUnavailable
	}
	derived, err := deriver.DeriveGenerationTwoSubkey([]byte(cursorInfo))
	if err != nil || len(derived) != sha256.Size {
		clear(derived)
		return nil, ErrUnavailable
	}
	codec := &cursorCodec{}
	copy(codec.key[:], derived)
	clear(derived)
	return codec, nil
}

func (codec *cursorCodec) Close() error {
	if codec == nil {
		return nil
	}
	codec.mu.Lock()
	defer codec.mu.Unlock()
	if !codec.closed {
		clear(codec.key[:])
		codec.closed = true
	}
	return nil
}

func (codec *cursorCodec) encode(scope, owner string, first int64, second string, now int64) (string, error) {
	if now < 0 || now > maxUnixSecond-replayWindowSeconds {
		return "", ErrInvalidRequest
	}
	return codec.encodeExpiry(scope, owner, first, second, now+replayWindowSeconds)
}

func (codec *cursorCodec) encodeExpiry(scope, owner string, first int64, second string, expiry int64) (string, error) {
	if codec == nil || scope == "" || len(scope) > 128 || len(owner) > 128 || first < 0 || second == "" || len(second) > 128 || expiry < 0 || expiry > maxUnixSecond ||
		!utf8.ValidString(scope) || !utf8.ValidString(owner) || !utf8.ValidString(second) {
		return "", ErrInvalidRequest
	}
	codec.mu.RLock()
	defer codec.mu.RUnlock()
	if codec.closed {
		return "", ErrClosed
	}
	payload := []byte{cursorVersion}
	payload = append(payload, framed([]byte(scope))...)
	payload = append(payload, framed([]byte(owner))...)
	var number [8]byte
	binary.BigEndian.PutUint64(number[:], uint64(expiry))
	payload = append(payload, number[:]...)
	var count [2]byte
	binary.BigEndian.PutUint16(count[:], 2)
	payload = append(payload, count[:]...)
	payload = append(payload, db.CursorUint)
	binary.BigEndian.PutUint64(number[:], uint64(first))
	payload = append(payload, number[:]...)
	payload = append(payload, db.CursorText)
	payload = append(payload, framed([]byte(second))...)
	mac := hmac.New(sha256.New, codec.key[:])
	_, _ = mac.Write(payload)
	raw := append(payload, mac.Sum(nil)...)
	token := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	if len(token) > 512 {
		return "", ErrInvalidRequest
	}
	return token, nil
}

func readCursorField(payload []byte, offset *int, max int) ([]byte, bool) {
	if offset == nil || *offset < 0 || len(payload)-*offset < 4 {
		return nil, false
	}
	length := int(binary.BigEndian.Uint32(payload[*offset : *offset+4]))
	*offset += 4
	if length < 0 || length > max || length > len(payload)-*offset {
		return nil, false
	}
	value := payload[*offset : *offset+length]
	*offset += length
	return value, true
}

func (codec *cursorCodec) decode(token, scope, owner string, now int64) (int64, string, error) {
	if codec == nil || token == "" || len(token) > 512 || scope == "" || len(scope) > 128 || len(owner) > 128 || now < 0 || now > maxUnixSecond {
		return 0, "", ErrInvalidRequest
	}
	codec.mu.RLock()
	defer codec.mu.RUnlock()
	if codec.closed {
		return 0, "", ErrClosed
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < 1+4+4+8+2+1+8+1+4+sha256.Size {
		clear(raw)
		return 0, "", ErrInvalidRequest
	}
	defer clear(raw)
	if base64.RawURLEncoding.EncodeToString(raw) != token {
		return 0, "", ErrInvalidRequest
	}
	payload, supplied := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, codec.key[:])
	_, _ = mac.Write(payload)
	if !hmac.Equal(supplied, mac.Sum(nil)) || payload[0] != cursorVersion {
		return 0, "", ErrInvalidRequest
	}
	offset := 1
	decodedScope, ok := readCursorField(payload, &offset, 128)
	if !ok {
		return 0, "", ErrInvalidRequest
	}
	decodedOwner, ok := readCursorField(payload, &offset, 128)
	if !ok || len(payload)-offset < 10 {
		return 0, "", ErrInvalidRequest
	}
	expiryValue := binary.BigEndian.Uint64(payload[offset : offset+8])
	offset += 8
	atomCount := binary.BigEndian.Uint16(payload[offset : offset+2])
	offset += 2
	if atomCount != 2 || offset >= len(payload) || payload[offset] != db.CursorUint {
		return 0, "", ErrInvalidRequest
	}
	offset++
	if len(payload)-offset < 8 {
		return 0, "", ErrInvalidRequest
	}
	firstValue := binary.BigEndian.Uint64(payload[offset : offset+8])
	offset += 8
	if offset >= len(payload) || payload[offset] != db.CursorText {
		return 0, "", ErrInvalidRequest
	}
	offset++
	secondBytes, ok := readCursorField(payload, &offset, 128)
	if !ok || offset != len(payload) || !utf8.Valid(decodedScope) || !utf8.Valid(decodedOwner) || !utf8.Valid(secondBytes) ||
		string(decodedScope) != scope || string(decodedOwner) != owner || expiryValue <= uint64(now) || expiryValue > uint64(maxUnixSecond) ||
		firstValue > math.MaxInt64 || len(secondBytes) == 0 {
		return 0, "", ErrInvalidRequest
	}
	first := int64(firstValue)
	second := string(secondBytes)
	return first, second, nil
}
