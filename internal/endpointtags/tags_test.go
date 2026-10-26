package endpointtags

import (
	"slices"
	"testing"
)

func TestDomainsUseOnlyNormalizedHostBoundaries(t *testing.T) {
	for _, address := range []string{"https://API.Foo.Example.COM.:8443/v1", "http://api.foo.example.com/other?host=notexample.com"} {
		if got := Domains(address); !slices.Equal(got, []string{"api.foo.example.com", "foo.example.com", "example.com", "com"}) {
			t.Fatal(address, got)
		}
	}
	for _, address := range []string{"https://notexample.com/v1", "https://example.com.evil.test/path"} {
		if slices.Contains(Domains(address), "example.com") {
			t.Fatal("suffix escaped domain boundary", address)
		}
	}
	for _, address := range []string{"https://127.0.0.1/v1", "https://[::1]:443/v1", "https://example.com@evil.test/v1", "invalid", "https://example..com"} {
		if got := Domains(address); len(got) != 0 {
			t.Fatal(address, got)
		}
	}
	if Pattern("*.EXAMPLE.com.") != "*.example.com" {
		t.Fatal("normalization")
	}
	for _, invalid := range []string{"example.com", "https://*.example.com", "*.example.com:443", "*.example.com/v1", "*.127.0.0.1", "*.*.example.com", "*.中文.com"} {
		if Pattern(invalid) != "" {
			t.Fatal("invalid pattern accepted", invalid)
		}
	}
}
