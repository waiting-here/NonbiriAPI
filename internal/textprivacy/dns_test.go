package textprivacy

import "testing"

func TestDNSRedactorHostLabelsAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, base, input, want string
	}{
		{"host and identifiers", "https://api.example.com:443/v1", "https://API.EXAMPLE.COM/v1 example EXAMPLE example_error example-gpt notexample exampleish api com v1", "https://[redacted]/v1 [redacted] [redacted] [redacted]_error [redacted]-gpt notexample exampleish api com v1"},
		{"Unicode boundaries", "https://example.com/v1", "例example example例 éexample exampleé ١example example１ (example)", "例example example例 éexample exampleé ١example example１ ([redacted])"},
		{"multiple suffix labels", "https://eu.api.example.co.uk./v1", "EU.API.EXAMPLE.CO.UK example eu api co.uk com", "[redacted] [redacted] [redacted] api co.uk com"},
		{"whole hyphenated label", "https://my-example.test/path-marker", "my-example MY-EXAMPLE_error example my path-marker notmy-example my-exampleish", "[redacted] [redacted]_error example my path-marker notmy-example my-exampleish"},
		{"common labels only", "https://api.com/v1", "api.com api com", "[redacted] api com"},
		{"IPv4 separate", "https://192.0.2.7/v1", "192.0.2.7 192 api", "192.0.2.7 192 api"},
		{"IPv6 separate", "https://[2001:db8::1]/v1", "2001:db8::1 db8", "2001:db8::1 db8"},
		{"binary retained", "https://example.test/v1", "example\xff_example", "[redacted]\xff_[redacted]"},
		{"punycode exact", "https://xn--bcher-kva.example/v1", "XN--BCHER-KVA bücher bcher", "[redacted] bücher bcher"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewDNSRedactor(tc.base).Redact(tc.input); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDNSRedactorSourcesAreLocalAndLongestMatchWins(t *testing.T) {
	r := NewDNSRedactor("https://api.example.com/v1", "https://api.example.com/other", "https://other.test/v1")
	if got := r.Redact("api.example.com example other unrelated"); got != "[redacted] [redacted] [redacted] unrelated" {
		t.Fatal(got)
	}
	for _, base := range []string{"", "not a URL", "file://example.com/path", "https://example..com", "https://例子.test"} {
		if got := NewDNSRedactor(base).Redact("example unrelated"); got != "example unrelated" {
			t.Fatalf("invalid source %q changed text: %q", base, got)
		}
	}
}
