package activities

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	activityCursorVersion byte  = 1
	maxCursorBytes              = 512
	cursorLifetime        int64 = 24 * 60 * 60
)

type cursorCodec struct {
	keys CursorKeyDeriver
}

func (c cursorCodec) encode(scope, owner string, expiry uint64, atoms []db.CursorAtom) (string, error) {
	key, err := c.derive()
	if err != nil {
		return "", ErrUnavailable
	}
	defer clear(key)
	if scope == "" || !utf8.ValidString(scope) || len(scope) > maxCursorBytes ||
		!utf8.ValidString(owner) || len(owner) > maxCursorBytes || expiry > uint64(maxUnixSecond) || len(atoms) > 65535 {
		return "", ErrInvalidRequest
	}
	payload := []byte{activityCursorVersion}
	payload = appendCursorField(payload, []byte(scope))
	payload = appendCursorField(payload, []byte(owner))
	var u64 [8]byte
	binary.BigEndian.PutUint64(u64[:], expiry)
	payload = append(payload, u64[:]...)
	var u16 [2]byte
	binary.BigEndian.PutUint16(u16[:], uint16(len(atoms)))
	payload = append(payload, u16[:]...)
	for _, atom := range atoms {
		encoded, ok := encodeCursorAtom(atom)
		if !ok {
			return "", ErrInvalidRequest
		}
		payload = append(payload, encoded...)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	raw := append(payload, mac.Sum(nil)...)
	token := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	if len(token) > maxCursorBytes {
		return "", ErrInvalidRequest
	}
	return token, nil
}

func (c cursorCodec) decode(token, expectedScope, expectedOwner string, now uint64, kinds ...byte) ([]db.CursorAtom, error) {
	key, err := c.derive()
	if err != nil {
		return nil, ErrUnavailable
	}
	defer clear(key)
	if token == "" || len(token) > maxCursorBytes || expectedScope == "" ||
		len(expectedScope) > maxCursorBytes || len(expectedOwner) > maxCursorBytes ||
		!utf8.ValidString(expectedScope) || !utf8.ValidString(expectedOwner) {
		return nil, ErrInvalidRequest
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < 1+4+4+8+2+sha256.Size {
		clear(raw)
		return nil, ErrInvalidRequest
	}
	defer clear(raw)
	payload := raw[:len(raw)-sha256.Size]
	provided := raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(provided, mac.Sum(nil)) || payload[0] != activityCursorVersion {
		return nil, ErrInvalidRequest
	}
	position := 1
	readField := func() ([]byte, bool) {
		if len(payload)-position < 4 {
			return nil, false
		}
		n := int(binary.BigEndian.Uint32(payload[position : position+4]))
		position += 4
		if n < 0 || n > maxCursorBytes || n > len(payload)-position {
			return nil, false
		}
		value := payload[position : position+n]
		position += n
		return value, true
	}
	scope, ok := readField()
	if !ok || len(scope) == 0 || !utf8.Valid(scope) {
		return nil, ErrInvalidRequest
	}
	owner, ok := readField()
	if !ok || !utf8.Valid(owner) || len(payload)-position < 10 {
		return nil, ErrInvalidRequest
	}
	expiry := binary.BigEndian.Uint64(payload[position : position+8])
	position += 8
	count := int(binary.BigEndian.Uint16(payload[position : position+2]))
	position += 2
	if string(scope) != expectedScope || string(owner) != expectedOwner || expiry > uint64(maxUnixSecond) || expiry <= now || count != len(kinds) {
		return nil, ErrInvalidRequest
	}
	atoms := make([]db.CursorAtom, 0, count)
	for index := 0; index < count; index++ {
		atom, next, valid := decodeCursorAtom(payload, position)
		if !valid || atom.Kind != kinds[index] {
			return nil, ErrInvalidRequest
		}
		position = next
		atoms = append(atoms, atom)
	}
	if position != len(payload) {
		return nil, ErrInvalidRequest
	}
	canonical, err := c.encode(expectedScope, expectedOwner, expiry, atoms)
	if err != nil || canonical != token {
		return nil, ErrInvalidRequest
	}
	return atoms, nil
}

func (c cursorCodec) derive() ([]byte, error) {
	if isNilInterface(c.keys) {
		return nil, ErrUnavailable
	}
	key, err := c.keys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil || len(key) != sha256.Size {
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}

func appendCursorField(destination, value []byte) []byte {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	destination = append(destination, size[:]...)
	return append(destination, value...)
}

func encodeCursorAtom(atom db.CursorAtom) ([]byte, bool) {
	switch atom.Kind {
	case db.CursorNull:
		return []byte{db.CursorNull}, true
	case db.CursorUint:
		var encoded [9]byte
		encoded[0] = db.CursorUint
		binary.BigEndian.PutUint64(encoded[1:], atom.Uint)
		return encoded[:], true
	case db.CursorText:
		if !utf8.ValidString(atom.Text) || len(atom.Text) > maxCursorBytes {
			return nil, false
		}
		return append([]byte{db.CursorText}, appendCursorField(nil, []byte(atom.Text))...), true
	case db.CursorBytes:
		if len(atom.Bytes) > maxCursorBytes {
			return nil, false
		}
		return append([]byte{db.CursorBytes}, appendCursorField(nil, atom.Bytes)...), true
	case db.CursorU128:
		return append([]byte{db.CursorU128}, atom.U128[:]...), true
	case db.CursorSM128:
		encoded, err := db.EncodeSM128(atom.Signed)
		return append([]byte{db.CursorSM128}, encoded...), err == nil
	default:
		return nil, false
	}
}

func decodeCursorAtom(payload []byte, position int) (db.CursorAtom, int, bool) {
	var atom db.CursorAtom
	if position >= len(payload) {
		return atom, position, false
	}
	atom.Kind = payload[position]
	position++
	switch atom.Kind {
	case db.CursorNull:
		return atom, position, true
	case db.CursorUint:
		if len(payload)-position < 8 {
			return atom, position, false
		}
		atom.Uint = binary.BigEndian.Uint64(payload[position : position+8])
		return atom, position + 8, true
	case db.CursorText, db.CursorBytes:
		if len(payload)-position < 4 {
			return atom, position, false
		}
		n := int(binary.BigEndian.Uint32(payload[position : position+4]))
		position += 4
		if n < 0 || n > maxCursorBytes || n > len(payload)-position {
			return atom, position, false
		}
		value := append([]byte(nil), payload[position:position+n]...)
		if atom.Kind == db.CursorText {
			if !utf8.Valid(value) {
				return atom, position, false
			}
			atom.Text = string(value)
		} else {
			atom.Bytes = value
		}
		return atom, position + n, true
	case db.CursorU128:
		if len(payload)-position < 16 {
			return atom, position, false
		}
		copy(atom.U128[:], payload[position:position+16])
		return atom, position + 16, true
	case db.CursorSM128:
		if len(payload)-position < 17 {
			return atom, position, false
		}
		value, err := db.DecodeSM128(payload[position : position+17])
		if err != nil {
			return atom, position, false
		}
		atom.Signed = value
		return atom, position + 17, true
	default:
		return atom, position, false
	}
}
