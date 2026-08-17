package openai

import (
	"bytes"
	"strings"
	"testing"
)

const validCompletion = `{
  "id":"chatcmpl-1",
  "object":"chat.completion",
  "created":1700000000,
  "model":"upstream/model",
  "choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
  "usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}
}`

const validChunk = `{
  "id":"chatcmpl-1",
  "object":"chat.completion.chunk",
  "created":1700000000,
  "model":"upstream/model",
  "choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]
}`

func TestValidateCompletionAndChunkUsage(t *testing.T) {
	usage, err := validateCompletion([]byte(validCompletion))
	if err != nil {
		t.Fatalf("validateCompletion: %v", err)
	}
	if !usage.Present || usage.PromptTokens != 2 || usage.CompletionTokens != 3 || usage.TotalTokens != 5 {
		t.Fatalf("usage = %+v", usage)
	}

	compact, usage, err := validateChunk([]byte(validChunk))
	if err != nil {
		t.Fatalf("validateChunk: %v", err)
	}
	defer clear(compact)
	if usage.Present || bytes.Contains(compact, []byte{'\n'}) || !bytes.Contains(compact, []byte(`"chat.completion.chunk"`)) {
		t.Fatalf("compact=%q usage=%+v", compact, usage)
	}

	usageChunk := `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"upstream/model","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18}}`
	compact, usage, err = validateChunk([]byte(usageChunk))
	if err != nil {
		t.Fatalf("usage chunk: %v", err)
	}
	clear(compact)
	if !usage.Present || usage.TotalTokens != 18 {
		t.Fatalf("usage chunk = %+v", usage)
	}
}

func TestValidateCompletionRejectsMalformedSuccessShapes(t *testing.T) {
	tests := []string{
		`{}`,
		`{"error":{"message":"secret"}}`,
		`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[]}`,
		`{"id":"x","object":"wrong","created":1,"model":"m","choices":[{"index":0,"message":{}}]}`,
		`{"id":"x","object":"chat.completion","created":-1,"model":"m","choices":[{"index":0,"message":{}}]}`,
		`{"id":"x","object":"chat.completion","created":1.5,"model":"m","choices":[{"index":0,"message":{}}]}`,
		`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":-1,"message":{}}]}`,
		`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0}]}`,
		`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`,
		`{"id":"x","id":"y","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{}}]}`,
		`{"id":"x\u000a","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{}}]}`,
		`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{}}]} trailing`,
	}
	for i, body := range tests {
		if _, err := validateCompletion([]byte(body)); err == nil {
			t.Errorf("case %d accepted malformed response: %s", i, body)
		}
	}
}

func TestValidateChunkRejectsErrorAndUnsafeFraming(t *testing.T) {
	bad := []string{
		`{"error":{"message":"raw upstream"}}`,
		`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"message":{}}]}`,
		`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":null}`,
		`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":null}]}`,
	}
	for _, body := range bad {
		if compact, _, err := validateChunk([]byte(body)); err == nil {
			clear(compact)
			t.Errorf("accepted bad chunk: %s", body)
		}
	}

	multiline := strings.Replace(validChunk, `"created":1700000000`, `"created":
1700000000`, 1)
	compact, _, err := validateChunk([]byte(multiline))
	if err != nil {
		t.Fatalf("valid multiline JSON rejected: %v", err)
	}
	defer clear(compact)
	if bytes.Contains(compact, []byte{'\n'}) {
		t.Fatalf("compact chunk retained a framing newline: %q", compact)
	}
}

func TestSensitiveGuardMatchesAcrossRollingWindowSizes(t *testing.T) {
	for _, length := range []int{1, 2, 7, 8, 17, 64, 257, 4096} {
		pattern := make([]byte, length)
		for i := range pattern {
			pattern[i] = byte(33 + (i*37)%90)
		}
		guard := newSensitiveGuard(pattern)
		prefix := append(bytes.Repeat([]byte{'z'}, length+3), pattern...)
		prefix = append(prefix, bytes.Repeat([]byte{'q'}, length+2)...)
		matched := false
		for offset := 0; offset < len(prefix); {
			next := min(len(prefix), offset+1+(offset%13))
			matched = guard.Contains(prefix[offset:next]) || matched
			offset = next
		}
		if !matched {
			t.Errorf("guard missed pattern length %d", length)
		}
		guard.Clear()
		clear(pattern)
		clear(prefix)
	}
}

func TestSensitiveGuardDetectsExactAndSplitMaterial(t *testing.T) {
	secret := []byte("sk-sensitive-012345")
	ciphertext := []byte("nbsec:v1:opaque-ciphertext")
	guard := newSensitiveGuard(secret, ciphertext)
	clear(secret)
	clear(ciphertext)
	defer guard.Clear()

	if guard.Contains([]byte("prefix sk-sensitive-")) {
		t.Fatal("guard matched an incomplete secret")
	}
	if !guard.Contains([]byte("012345 suffix")) {
		t.Fatal("guard missed a secret spanning writes")
	}

	second := newSensitiveGuard([]byte("cipher-marker"))
	defer second.Clear()
	if second.Contains([]byte("safe output")) {
		t.Fatal("guard false positive")
	}
	if !second.Contains([]byte("xxcipher-markeryy")) {
		t.Fatal("guard missed embedded material")
	}
}
