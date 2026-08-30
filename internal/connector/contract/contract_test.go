package contract

import (
	"fmt"
	"testing"
)

func TestShortLivedSecretTransfersExactlyOnceAndRedactsFormatting(t *testing.T) {
	plaintext := []byte("plain-secret")
	ciphertext := []byte("cipher-secret")
	secret := NewShortLivedSecret(plaintext, ciphertext)
	if got := fmt.Sprintf("%v %#v", secret, secret); got != "[redacted upstream credential] [redacted upstream credential]" {
		t.Fatalf("secret formatting was not redacted: %q", got)
	}
	gotPlain, gotCipher, ok := secret.Take()
	if !ok || string(gotPlain) != "plain-secret" || string(gotCipher) != "cipher-secret" {
		t.Fatalf("first Take = %q %q %v", gotPlain, gotCipher, ok)
	}
	if secondPlain, secondCipher, secondOK := secret.Take(); secondOK || secondPlain != nil || secondCipher != nil {
		t.Fatalf("second Take unexpectedly succeeded: %q %q %v", secondPlain, secondCipher, secondOK)
	}
	clear(gotPlain)
	clear(gotCipher)
	secret.Clear()
}

func TestCapabilitySetRequiresAllBits(t *testing.T) {
	set := CapabilitySet(CapabilityText | CapabilityStream)
	if !set.Has(CapabilityText) || !set.HasAll(CapabilitySet(CapabilityText|CapabilityStream)) {
		t.Fatal("declared capability was not found")
	}
	if set.Has(CapabilityTools) || set.HasAll(CapabilitySet(CapabilityText|CapabilityTools)) {
		t.Fatal("undeclared capability was admitted")
	}
}

func TestDiscoveryResultSucceededRequiresCompleteTypedFacts(t *testing.T) {
	tests := []struct {
		name   string
		result DiscoveryResult
		want   bool
	}{
		{
			name: "nonempty success",
			result: DiscoveryResult{
				Models:           []DiscoveredModel{{ID: "model-a"}},
				Failure:          DiscoveryFailureNone,
				UpstreamStatus:   200,
				ResponseReceived: true,
			},
			want: true,
		},
		{
			name: "empty success",
			result: DiscoveryResult{
				Models:           []DiscoveredModel{},
				Failure:          DiscoveryFailureNone,
				UpstreamStatus:   204,
				ResponseReceived: true,
			},
			want: true,
		},
		{name: "zero value", result: DiscoveryResult{}},
		{
			name: "unknown failure",
			result: DiscoveryResult{
				Failure:          DiscoveryFailureKind("future"),
				UpstreamStatus:   200,
				ResponseReceived: true,
			},
		},
		{
			name: "typed failure",
			result: DiscoveryResult{
				Failure:          DiscoveryFailureProtocol,
				UpstreamStatus:   200,
				ResponseReceived: true,
			},
		},
		{
			name: "no response",
			result: DiscoveryResult{
				Failure:        DiscoveryFailureNone,
				UpstreamStatus: 200,
			},
		},
		{
			name: "non-2xx status",
			result: DiscoveryResult{
				Failure:          DiscoveryFailureNone,
				UpstreamStatus:   500,
				ResponseReceived: true,
			},
		},
		{
			name: "diagnostic on otherwise successful facts",
			result: DiscoveryResult{
				Failure:          DiscoveryFailureNone,
				UpstreamStatus:   200,
				ResponseReceived: true,
				Diagnostic:       "unexpected diagnostic",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.result.Succeeded(); got != test.want {
				t.Fatalf("Succeeded() = %v, want %v for %+v", got, test.want, test.result)
			}
		})
	}
}

func TestDiscoveryFailureKindsHaveFrozenSafeValues(t *testing.T) {
	want := []DiscoveryFailureKind{
		DiscoveryFailureNone,
		DiscoveryFailureAuth,
		DiscoveryFailureRateLimit,
		DiscoveryFailureTimeout,
		DiscoveryFailureProtocol,
		DiscoveryFailureTransport,
		DiscoveryFailureInterrupted,
	}
	got := []string{"none", "auth", "rate_limit", "timeout", "protocol", "transport", "interrupted"}
	for index, kind := range want {
		if string(kind) != got[index] {
			t.Fatalf("failure kind %d = %q, want %q", index, kind, got[index])
		}
	}
}
