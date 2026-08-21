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
	if !usage.Present || usage.UncachedInputTokens != 2 || usage.CacheWriteInputTokens != 0 || usage.CacheReadInputTokens != 0 || usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", usage)
	}

	compact, usage, malformed, err := validateChunk([]byte(validChunk))
	if err != nil || malformed {
		t.Fatalf("validateChunk: err=%v malformed=%v", err, malformed)
	}
	defer clear(compact)
	if usage.Present || bytes.Contains(compact, []byte{'\n'}) || !bytes.Contains(compact, []byte(`"chat.completion.chunk"`)) {
		t.Fatalf("compact=%q usage=%+v", compact, usage)
	}

	usageChunk := `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"upstream/model","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18}}`
	compact, usage, malformed, err = validateChunk([]byte(usageChunk))
	if err != nil || malformed {
		t.Fatalf("usage chunk: err=%v malformed=%v", err, malformed)
	}
	clear(compact)
	if !usage.Present || usage.UncachedInputTokens != 7 || usage.OutputTokens != 11 {
		t.Fatalf("usage chunk = %+v", usage)
	}
}

// TestUsageNormalizationMatrix covers the frozen four-bucket normalization
// rules for the non-stream path: official shapes with the details sub-object,
// early models without cache writes, exact sub-item sums, and every malformed
// shape degrading the whole usage to unknown without failing the response.
func TestUsageNormalizationMatrix(t *testing.T) {
	completion := func(usage string) []byte {
		return []byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{}}],"usage":` + usage + `}`)
	}
	tests := []struct {
		name      string
		usage     string
		wantKnown bool
		uncached  int64
		write     int64
		read      int64
		output    int64
	}{
		{
			name:      "official form with cached read",
			usage:     `{"prompt_tokens":2006,"completion_tokens":300,"total_tokens":2306,"prompt_tokens_details":{"cached_tokens":1920,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}`,
			wantKnown: true, uncached: 86, read: 1920, output: 300,
		},
		{
			name:      "cache write present",
			usage:     `{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"prompt_tokens_details":{"cached_tokens":40,"cache_write_tokens":30}}`,
			wantKnown: true, uncached: 30, write: 30, read: 40, output: 10,
		},
		{
			name:      "early model without cache write",
			usage:     `{"prompt_tokens":50,"completion_tokens":5,"total_tokens":55,"prompt_tokens_details":{"cached_tokens":20}}`,
			wantKnown: true, uncached: 30, read: 20, output: 5,
		},
		{
			name:      "read plus write equals prompt",
			usage:     `{"prompt_tokens":70,"completion_tokens":1,"total_tokens":71,"prompt_tokens_details":{"cached_tokens":40,"cache_write_tokens":30}}`,
			wantKnown: true, write: 30, read: 40, output: 1,
		},
		{
			name:      "null details object treated as absent",
			usage:     `{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9,"prompt_tokens_details":null}`,
			wantKnown: true, uncached: 7, output: 2,
		},
		{
			name:  "read plus write exceeds prompt",
			usage: `{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":6,"cache_write_tokens":6}}`,
		},
		{
			name:  "negative prompt",
			usage: `{"prompt_tokens":-1,"completion_tokens":1,"total_tokens":0}`,
		},
		{
			name:  "negative cache read",
			usage: `{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":-1}}`,
		},
		{
			name:  "negative cache write",
			usage: `{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11,"prompt_tokens_details":{"cache_write_tokens":-5}}`,
		},
		{
			name:  "float token count",
			usage: `{"prompt_tokens":10.5,"completion_tokens":1,"total_tokens":11}`,
		},
		{
			name:  "string token count",
			usage: `{"prompt_tokens":"10","completion_tokens":1,"total_tokens":11}`,
		},
		{
			name:  "float cache read",
			usage: `{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":1.0}}`,
		},
		{
			name:  "missing completion tokens",
			usage: `{"prompt_tokens":10,"total_tokens":10}`,
		},
		{
			name:  "missing total tokens",
			usage: `{"prompt_tokens":10,"completion_tokens":1}`,
		},
		{
			name:  "details not an object",
			usage: `{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11,"prompt_tokens_details":[1]}`,
		},
		{
			name:  "usage not an object",
			usage: `[1,2]`,
		},
		{
			name:  "int64 overflow boundary on cache sum",
			usage: `{"prompt_tokens":9223372036854775807,"completion_tokens":1,"total_tokens":9223372036854775808,"prompt_tokens_details":{"cached_tokens":9223372036854775807,"cache_write_tokens":1}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usage, err := validateCompletion(completion(test.usage))
			if err != nil {
				t.Fatalf("validateCompletion rejected response over usage: %v", err)
			}
			if usage.Present != test.wantKnown {
				t.Fatalf("usage.Present = %v, want %v (%+v)", usage.Present, test.wantKnown, usage)
			}
			if !test.wantKnown {
				return
			}
			if usage.UncachedInputTokens != test.uncached || usage.CacheWriteInputTokens != test.write ||
				usage.CacheReadInputTokens != test.read || usage.OutputTokens != test.output {
				t.Fatalf("buckets = %+v", usage)
			}
		})
	}

	// Max-int64 boundary that stays consistent must parse exactly.
	usage, err := validateCompletion(completion(`{"prompt_tokens":9223372036854775807,"completion_tokens":0,"total_tokens":9223372036854775807,"prompt_tokens_details":{"cached_tokens":9223372036854775807}}`))
	if err != nil || !usage.Present || usage.CacheReadInputTokens != 1<<63-1 || usage.UncachedInputTokens != 0 {
		t.Fatalf("max boundary usage=%+v err=%v", usage, err)
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
		if compact, _, _, err := validateChunk([]byte(body)); err == nil {
			clear(compact)
			t.Errorf("accepted bad chunk: %s", body)
		}
	}

	multiline := strings.Replace(validChunk, `"created":1700000000`, `"created":
1700000000`, 1)
	compact, _, _, err := validateChunk([]byte(multiline))
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
