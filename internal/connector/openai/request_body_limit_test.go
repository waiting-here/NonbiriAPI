package openai

import (
	"github.com/waiting-here/NonbiriAPI/internal/requestbody"
	"strings"
	"testing"
)

func TestLargeConfiguredBodiesSurviveProtocolClonesAndRewrites(t *testing.T) {
	limit := 12 * requestbody.MiB
	content := strings.Repeat("x", 11*int(requestbody.MiB))
	chat := `{"model":"provider/model","messages":[{"role":"user","content":"` + content + `"}]}`
	request, err := DecodeChatRequest(strings.NewReader(chat), limit)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	clone := request.CloneForAttempt()
	defer clone.Clear()
	body, err := clone.marshalUpstream("upstream-model", "valid-safety-id")
	if err != nil || len(body) < len(content) || clone.RequestBodyLimit() != limit {
		t.Fatalf("chat rewrite length=%d err=%v", len(body), err)
	}
	if _, err = DecodeChatRequest(strings.NewReader(chat), requestbody.DefaultBytes); err == nil {
		t.Fatal("default limit accepted 11 MiB")
	}
	embedding := `{"model":"provider/model","input":"` + content + `"}`
	vector, err := DecodeEmbeddingRequest(strings.NewReader(embedding), limit)
	if err != nil {
		t.Fatal(err)
	}
	defer vector.Clear()
	other := vector.CloneForAttempt()
	defer other.Clear()
	body, err = other.marshalUpstream("upstream-model", "valid-safety-id")
	if err != nil || len(body) < len(content) || other.RequestBodyLimit() != limit {
		t.Fatalf("embedding rewrite length=%d err=%v", len(body), err)
	}
}
