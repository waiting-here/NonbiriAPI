package returnmarker

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testKeys byte

func (k testKeys) DeriveGenerationTwoSubkey(info []byte) ([]byte, error) {
	if string(info) != keyPurpose {
		panic("incorrect key scope")
	}
	return bytes.Repeat([]byte{byte(k)}, 32), nil
}

func testMarker(t *testing.T) *Marker {
	t.Helper()
	m, err := New(testKeys(1))
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Unix(2000000000, 0) }
	return m
}

func markedRequest(t *testing.T, m *Marker) *http.Request {
	t.Helper()
	id := "req_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 16))
	ctx := WithSource(context.Background(), Source{RequestID: id, EndpointID: 3, EndpointKeyID: 5, DonationKeyID: 7})
	r := httptest.NewRequest("POST", "https://provider.example/v1/chat/completions", nil).WithContext(ctx)
	r.Header["nonbiri-request-origin"] = []string{"forged"}
	m.Stamp(r, "https://provider.example/v1")
	return r
}

func TestMarkerAuthenticatesOpaqueBoundedSource(t *testing.T) {
	m := testMarker(t)
	r := markedRequest(t, m)
	evidence, ok := m.Verify(r.Header)
	if !ok || evidence.EndpointID != 3 || evidence.EndpointKeyID != 5 || evidence.DonationKeyID != 7 || !validRequestID(evidence.RequestID) {
		t.Fatal("source did not survive authenticated round trip")
	}
	encoded := r.Header.Get(Header)
	if len(encoded) > maxHeaderBytes || len(r.Header) != 1 {
		t.Fatal("marker was not bounded or did not override headers")
	}
	wire, _ := base64.RawURLEncoding.DecodeString(encoded[3:])
	if bytes.Contains(wire, []byte(evidence.RequestID)) || bytes.Contains(wire, []byte("provider.example")) {
		t.Fatal("readable source in marker")
	}
	m.Stamp(r, "https://provider.example/v1")
	if r.Header.Get(Header) == encoded {
		t.Fatal("marker reused nonce")
	}
	other, _ := New(testKeys(2))
	if _, ok := other.Verify(r.Header); ok {
		t.Fatal("other installation accepted marker")
	}
}

func TestMarkerIgnoresInvalidExpiredAndDuplicateFields(t *testing.T) {
	m := testMarker(t)
	r := markedRequest(t, m)
	value := r.Header.Get(Header)
	changed := []byte(value)
	changed[len(changed)/2] = 'A'
	if string(changed) == value {
		changed[len(changed)/2] = 'B'
	}
	for name, headers := range map[string]http.Header{
		"missing": {}, "forged": {Header: {"v1.forged"}}, "tampered": {Header: {string(changed)}},
		"duplicate": {Header: {value, value}}, "case_duplicate": {Header: {value}, "nonbiri-request-origin": {value}},
		"empty_duplicate": {Header: {value}, "nonbiri-request-origin": {}}, "combined": {Header: {value + "," + value}},
		"newline": {Header: {value[:20] + "\r\n" + value[20:]}},
		"long":    {Header: {strings.Repeat("A", maxHeaderBytes+1)}}, "version": {Header: {"v2." + value[3:]}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := m.Verify(headers); ok {
				t.Fatal("accepted invalid header")
			}
		})
	}
	for _, elapsed := range []time.Duration{-time.Second, Lifetime, Lifetime + time.Second} {
		m.now = func() time.Time { return time.Unix(2000000000, 0).Add(elapsed) }
		if _, ok := m.Verify(r.Header); ok {
			t.Fatal("accepted invalid timestamp", elapsed)
		}
	}
	m.now = func() time.Time { return time.Unix(2000000000, 0).Add(Lifetime - time.Second) }
	if _, ok := m.Verify(r.Header); !ok {
		t.Fatal("premature expiry")
	}
}
