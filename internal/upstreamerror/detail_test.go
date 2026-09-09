package upstreamerror

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

func testContext(secrets ...string) Context {
	return Context{BaseURL: "https://private.example.invalid/v1", PrivateModel: "private-model", ContainsSecret: func(value []byte) bool {
		for _, secret := range secrets {
			if bytes.Contains(value, []byte(secret)) {
				return true
			}
		}
		return false
	}}
}

func TestDescriptionsRetainUsefulErrorsWithoutSourceOrCredentials(t *testing.T) {
	for _, tc := range []struct{ name, body, message, code string }{
		{"openai", `{"error":{"message":"Token budget exceeded; requested 8192, maximum 4096.","code":"context_length_exceeded","type":"invalid_request_error"}}`, "Token budget exceeded; requested 8192, maximum 4096.", "context_length_exceeded"},
		{"anthropic", `{"type":"error","error":{"type":"overloaded_error","message":"Temporarily overloaded. Please retry."}}`, "Temporarily overloaded. Please retry.", "overloaded_error"},
		{"string error", `{"error":"No capacity for this request","code":503}`, "No capacity for this request", "503"},
		{"plain", "Quota exhausted. Retry in 5 seconds.", "Quota exhausted. Retry in 5 seconds.", ""},
		{"url", `{"error":{"message":"Request to https://private.example.invalid/v1/chat?token=opaque-secret failed; check input.","code":"invalid_input"}}`, "Request to [redacted] failed; check input.", "invalid_input"},
		{"host", `{"message":"PRIVATE.EXAMPLE.INVALID:443 rejected model private-model","code":"model_not_found"}`, "[redacted]:443 rejected model [model]", "model_not_found"},
		{"addresses", `{"message":"Connections to 203.0.113.2:443 and [2001:db8::5] failed."}`, "Connections to [redacted] and [[redacted]] failed.", ""},
		{"ipv6 punctuation", `{"message":"Connection failed at 2001:db8::5."}`, "Connection failed at [redacted]", ""},
		{"escaped host", `{"message":"Connect to \u0061pi.example.invalid failed"}`, "Connect to [redacted] failed", ""},
		{"encoded", `{"message":"https%3A%2F%2Fapi.example.invalid%2Fv1 and api&#46;example&#46;invalid failed"}`, "[redacted] and [redacted] failed", ""},
		{"secret", `{"message":"Incorrect key opaque-secret; retry with another credential.","code":"invalid_api_key"}`, "Incorrect key [redacted]; retry with another credential.", "invalid_api_key"},
		{"unicode secret", `{"message":"密钥：\u79d8密密钥不可用"}`, "密钥：[redacted]不可用", ""},
		{"encoded secret", `{"message":"Key opaque%2Dsecret was rejected"}`, "Key [redacted] was rejected", ""},
		{"controls", `{"message":"opaque\u0000-secret was rejected\ntry again\u202e"}`, "[redacted] was rejected try again", ""},
		{"ignored fields", `{"error":{"message":"Quota exhausted","code":"quota"},"request":{"Authorization":"opaque-secret","url":"https://private.example.invalid"}}`, "Quota exhausted", "quota"},
		{"secret code", `{"message":"Bad credential","code":"opaque-secret"}`, "Bad credential", ""},
		{"host code", `{"message":"Failed","code":"api.example.invalid"}`, "Failed", ""},
		{"upstream account", `{"message":"Account donor@private.example.invalid has no quota"}`, "Account [redacted] has no quota", ""},
		{"generic auth", `{"message":"Bearer unrelated-token was rejected"}`, "[redacted] was rejected", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := testContext("opaque-secret", "秘密密钥").Parse([]byte(tc.body))
			if value.Message() != tc.message || value.Code() != tc.code {
				t.Fatalf("detail=%+v, want message %q code %q", value, tc.message, tc.code)
			}
		})
	}
}

func TestDescriptionParsingFailsClosedAndBoundsWork(t *testing.T) {
	context := testContext("opaque-secret")
	for _, body := range []string{
		"", "<html><body>Proxy failure</body></html>", "[]", `"error text"`,
		`{"error":{"message":"safe","message":"opaque-secret"}}`,
		`{"message":"safe","ignored":{"a":1,"a":2}}`,
		`{"message":"safe"} {"message":"second"}`, `{"message":"\ud800"}`,
		`{"message":`, `{"error":["opaque-secret"]}`, string([]byte{0xff}),
		strings.Repeat("x", MaxBodyBytes+1),
	} {
		if detail := context.Parse([]byte(body)); detail != (Detail{}) {
			t.Fatalf("unexpected detail for invalid input: %+v", detail)
		}
	}
	if got := (Context{}).Parse([]byte(`{"message":"safe"}`)); got != (Detail{}) {
		t.Fatal("missing secret detector must fail closed")
	}
	message := strings.Repeat("长", 600) + " opaque-secret"
	body, _ := json.Marshal(map[string]string{"message": message})
	detail := context.Parse(body)
	if len(detail.Message()) > 1024 || !utf8.ValidString(detail.Message()) || strings.Contains(detail.Message(), "opaque-secret") {
		t.Fatal("message bound or redaction failed")
	}
	for _, limit := range []int64{1, 12, MaxBodyBytes} {
		reader := &countReader{Reader: strings.NewReader(strings.Repeat("x", MaxBodyBytes+100))}
		if got := context.Read(reader, limit); got != (Detail{}) || int64(reader.read) != limit+1 {
			t.Fatalf("bounded reader result=%+v read=%d limit=%d", got, reader.read, limit)
		}
	}
	if detail := context.Read(failedReader{}, MaxBodyBytes); detail != (Detail{}) {
		t.Fatal("read failure exposed a partial body")
	}
}

func TestSourceContextCannotLeakThroughFormattingOrLogging(t *testing.T) {
	context := testContext("opaque-secret")
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("failure", "context", context)
	output := logs.String() + fmt.Sprintf("%s %v %+v %#v", context, context, context, context)
	for _, forbidden := range []string{"private.example", "private-model", "opaque-secret"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("context exposed %q", forbidden)
		}
	}
}

func TestSecretRedactionHandlesRepeatedAndOverlappingPatterns(t *testing.T) {
	context := testContext("abcd", "bc", "秘密")
	if got := context.clean("abcd and 秘密 and bc"); strings.Contains(got, "bc") || strings.Contains(got, "秘密") || got == "" {
		t.Fatalf("repeated redaction=%q", got)
	}
	if got := testContext("secret").clean(strings.Repeat("secret ", 20)); got != "" {
		t.Fatal("excessive repetitions must use generic fallback")
	}
	if got := testContext("redacted").clean("redacted"); got != "" {
		t.Fatal("a marker colliding with sensitive material must fail closed")
	}
}

func TestErrorEventClassificationPreservesCompletionProtocol(t *testing.T) {
	for _, body := range []string{`{"error":{"message":"error"}}`, `{"\u0065rror":"error"}`} {
		if !IsEvent([]byte(body)) {
			t.Fatal("explicit error was not recognized")
		}
	}
	for _, body := range []string{`{"choices":[{"delta":{"content":"error"}}]}`, `{"error":null}`, `{"error":{},"error":null}`, `[]`, "not JSON"} {
		if IsEvent([]byte(body)) {
			t.Fatal("ordinary or ambiguous data treated as an error event")
		}
	}
}

type countReader struct {
	io.Reader
	read int
}

func FuzzErrorDescriptionBoundary(f *testing.F) {
	for _, seed := range []string{
		`{"error":{"message":"Quota exhausted","code":"quota"}}`,
		`{"message":"opaque%2Dsecret at private&#46;example&#46;invalid"}`,
		`{"message":"user@private.example.invalid [2001:db8::1]"}`,
		`{"message":"opaque-secret","message":"ambiguous"}`, "plain error", "<html>error</html>",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		detail := testContext("opaque-secret").Parse(body)
		if !utf8.ValidString(detail.Message()) || len(detail.Message()) > 1024 || len(detail.Code()) > 64 {
			t.Fatal("error exceeded its output bounds")
		}
		for _, forbidden := range []string{"opaque-secret", "private.example.invalid", "private-model"} {
			if strings.Contains(detail.Message(), forbidden) || strings.Contains(detail.Code(), forbidden) {
				t.Fatal("sensitive error detail escaped")
			}
		}
	})
}

func (r *countReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errors.New("read interrupted") }
