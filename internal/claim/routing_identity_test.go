package claim

import (
	"errors"
	"testing"
)

func TestPhysicalCharityKeyIDUsesResourceGenerationAndOrigin(t *testing.T) {
	base, err := physicalCharityKeyID(7, 11, "HTTPS://EXAMPLE.COM./api/v1")
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := physicalCharityKeyID(7, 11, "https://example.com:443/other/path")
	if err != nil || base != equivalent {
		t.Fatalf("equivalent origin changed identity: %v", err)
	}
	for _, tc := range []struct {
		keyID    int64
		secretID int64
		baseURL  string
	}{
		{8, 11, "https://example.com/v1"},
		{7, 12, "https://example.com/v1"},
		{7, 11, "https://example.com:8443/v1"},
		{7, 11, "http://example.com/v1"},
	} {
		other, err := physicalCharityKeyID(tc.keyID, tc.secretID, tc.baseURL)
		if err != nil || other == base {
			t.Fatalf("distinct physical resource collided: %+v, %v", tc, err)
		}
	}
	for _, tc := range []struct {
		keyID    int64
		secretID int64
		baseURL  string
	}{
		{0, 11, "https://example.com"},
		{7, 0, "https://example.com"},
		{7, 11, "not a URL"},
	} {
		if _, err := physicalCharityKeyID(tc.keyID, tc.secretID, tc.baseURL); !errors.Is(err, ErrInvariant) {
			t.Fatalf("invalid physical identity %+v: %v", tc, err)
		}
	}
}
