package openai

import (
	"fmt"
	"strings"
	"testing"
)

func TestEmbeddingGuardPreservesLiteralAndDecodedStringProtection(t *testing.T) {
	for _, tc := range []struct {
		name, secret, first, second string
		want                        bool
	}{
		{"literal", "QUFB", `"QUFBQUFBQUFBQUFB"`, `"QgAAAAAAAAAAAAAA"`, true},
		{"escaped", "QUFB", `"\u0051\u0055\u0046\u0042QUFBQUFBQUFB"`, `"QgAAAAAAAAAAAAAA"`, true},
		{"split", "QUFBQUFBQUFBQUFBQgAAAAAAAAAAAAAA", `"QUFBQUFBQUFBQUFB"`, `"QgAAAAAAAAAAAAAA"`, true},
		{"safe", "different-secret", `"QUFBQUFBQUFBQUFB"`, `"QgAAAAAAAAAAAAAA"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each vector is a separately valid float32 sequence. The semantic
			// detector still preserves the existing normalized array path rule.
			wire := []byte(fmt.Sprintf(`{"object":"list","model":"public","data":[{"object":"embedding","index":0,"embedding":%s},{"object":"embedding","index":1,"embedding":%s}]}`, tc.first, tc.second))
			guard := newResponseGuard([]byte(tc.secret))
			defer guard.Clear()
			if got := guard.containsEmbeddingProjection(wire); got != tc.want {
				t.Fatalf("new guard=%v want %v", got, tc.want)
			}
			original := newResponseGuard([]byte(tc.secret))
			defer original.Clear()
			if got := original.ContainsJSON(wire, wire); got != tc.want {
				t.Fatalf("original guard=%v want %v", got, tc.want)
			}
		})
	}
	guard := newResponseGuard([]byte("public-secret"))
	defer guard.Clear()
	wire := []byte(strings.Replace(embeddingResponse("float", `null`), "private/upstream", `public-\u0073ecret`, 1))
	if !guard.containsEmbeddingProjection(wire) {
		t.Fatal("decoded model reflection escaped")
	}
}
