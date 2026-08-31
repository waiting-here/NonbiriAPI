package reports

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type fixedReportCursorDeriver struct{}

func (fixedReportCursorDeriver) DeriveGenerationTwoSubkey(info []byte) ([]byte, error) {
	if !bytes.Equal(info, []byte(cursorInfo)) {
		return nil, errors.New("unexpected cursor purpose")
	}
	return bytes.Repeat([]byte{0x42}, sha256.Size), nil
}

func reportCursorAtomOffset(t *testing.T, payload []byte) int {
	t.Helper()
	offset := 1
	for range 2 {
		if len(payload)-offset < 4 {
			t.Fatal("cursor field length is truncated")
		}
		length := int(binary.BigEndian.Uint32(payload[offset : offset+4]))
		offset += 4
		if length > len(payload)-offset {
			t.Fatal("cursor field is truncated")
		}
		offset += length
	}
	if len(payload)-offset < 10 {
		t.Fatal("cursor expiry/count is truncated")
	}
	return offset + 10
}

func resignReportCursor(t *testing.T, raw []byte, mutate func([]byte)) string {
	t.Helper()
	if len(raw) <= sha256.Size {
		t.Fatal("cursor is truncated before mutation")
	}
	payload := append([]byte(nil), raw[:len(raw)-sha256.Size]...)
	mutate(payload)
	key := bytes.Repeat([]byte{0x42}, sha256.Size)
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
}

func TestReportCursorUsesFrozenTypedAtomWire(t *testing.T) {
	codec, err := newCursorCodec(fixedReportCursorDeriver{})
	if err != nil {
		t.Fatal(err)
	}
	defer codec.Close()

	const (
		scope  = "report-cases/v1"
		owner  = "admin-7"
		second = "rpc_AAAAAAAAAAAAAAAAAAAAAQ"
		expiry = int64(1_800_086_400)
	)
	token, err := codec.encodeExpiry(scope, owner, 1_800_000_000, second, expiry)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "AQAAAA9yZXBvcnQtY2FzZXMvdjEAAAAHYWRtaW4tNwAAAABrSyOAAAIBAAAAAGtJ0gACAAAAGnJwY19BQUFBQUFBQUFBQUFBQUFBQUFBQUFRVeb2MOKBEnZogIUzMSf1nTE6r2ZZonAcHgfPncCj0Xo"
	if token != expected {
		t.Fatalf("cursor golden=%q", token)
	}
	first, decodedSecond, err := codec.decode(token, scope, owner, expiry-1)
	if err != nil || first != 1_800_000_000 || decodedSecond != second {
		t.Fatalf("decoded cursor first=%d second=%q err=%v", first, decodedSecond, err)
	}

	for name, decode := range map[string]func() error{
		"cross scope": func() error { _, _, err := codec.decode(token, materialCursorScope, owner, expiry-1); return err },
		"cross owner": func() error { _, _, err := codec.decode(token, scope, "admin-8", expiry-1); return err },
		"at expiry":   func() error { _, _, err := codec.decode(token, scope, owner, expiry); return err },
		"padding":     func() error { _, _, err := codec.decode(token+"=", scope, owner, expiry-1); return err },
	} {
		if err := decode(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s error=%v", name, err)
		}
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	atomOffset := reportCursorAtomOffset(t, raw[:len(raw)-sha256.Size])
	wrongType := resignReportCursor(t, raw, func(payload []byte) { payload[atomOffset] = db.CursorText })
	if _, _, err := codec.decode(wrongType, scope, owner, expiry-1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("wrong atom type error=%v", err)
	}
	wrongCount := resignReportCursor(t, raw, func(payload []byte) {
		binary.BigEndian.PutUint16(payload[atomOffset-2:atomOffset], 1)
	})
	if _, _, err := codec.decode(wrongCount, scope, owner, expiry-1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("wrong atom count error=%v", err)
	}
	uintOverflow := resignReportCursor(t, raw, func(payload []byte) {
		binary.BigEndian.PutUint64(payload[atomOffset+1:atomOffset+9], ^uint64(0))
	})
	if _, _, err := codec.decode(uintOverflow, scope, owner, expiry-1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("uint overflow error=%v", err)
	}
	tampered := append([]byte(nil), raw...)
	tampered[1] ^= 1
	if _, _, err := codec.decode(base64.RawURLEncoding.EncodeToString(tampered), scope, owner, expiry-1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("tampered cursor error=%v", err)
	}
	if _, _, err := codec.decode(base64.RawURLEncoding.EncodeToString(raw[:len(raw)-1]), scope, owner, expiry-1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("truncated cursor error=%v", err)
	}
}

func TestReportCursorCloseClearsRuntime(t *testing.T) {
	codec, err := newCursorCodec(fixedReportCursorDeriver{})
	if err != nil {
		t.Fatal(err)
	}
	token, err := codec.encodeExpiry(caseCursorScope, "admin-7", 1, "rpc_AAAAAAAAAAAAAAAAAAAAAQ", reportTestNow+1)
	if err != nil {
		t.Fatal(err)
	}
	if err := codec.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := codec.encodeExpiry(caseCursorScope, "admin-7", 1, "rpc_AAAAAAAAAAAAAAAAAAAAAQ", reportTestNow+1); !errors.Is(err, ErrClosed) {
		t.Fatalf("encode after close error=%v", err)
	}
	if _, _, err := codec.decode(token, caseCursorScope, "admin-7", reportTestNow); !errors.Is(err, ErrClosed) {
		t.Fatalf("decode after close error=%v", err)
	}
}
