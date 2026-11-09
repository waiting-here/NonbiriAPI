package contract

import (
	"strings"
	"testing"
)

func TestRerankPath(t *testing.T) {
	for _, path := range []string{"", "/rerank", "/compatible-api/v1/reranks", "/v1/%E6%8E%92%E5%BA%8F"} {
		if err := ValidateRerankPath(path); err != nil {
			t.Errorf("%q: %v", path, err)
		}
	}
	for _, path := range []string{"https://other.example/rerank", "//other.example/rerank", "rerank", "/x?key=y", "/x#f", "/../rerank", "/x/%2e%2e/rerank", "/%2fother", "/x%2fy", "/x%5cy", "/x%252fy", "/x%0ay", "/x y", "/" + strings.Repeat("x", 2048)} {
		if err := ValidateRerankPath(path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	for _, tt := range []struct{ path, want string }{{"", "https://provider.example/api/v1/rerank"}, {"/compatible-api/v1/reranks", "https://provider.example/compatible-api/v1/reranks"}} {
		got, err := RerankURL("https://provider.example/api/v1", tt.path)
		if err != nil || got != tt.want {
			t.Fatal(got, err)
		}
	}
	if SupportsRerankPath(TypeAISDKGatewayV4) || !SupportsRerankPath(TypeOpenAICompatible) || !SupportsRerankPath(TypeJinaRerank) {
		t.Fatal("HTTP capability mismatch")
	}
}
