package anthropic

import (
	"bytes"
	"errors"
	"testing"
)

func TestMarshalJSONNoEscapeLimitedPreservesHTMLAndExactBoundary(t *testing.T) {
	type envelope struct {
		Value string `json:"value"`
	}

	html, err := marshalJSONNoEscapeLimited(envelope{Value: `<tag attr="x">&value</tag>`}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(html)
	if !bytes.Contains(html, []byte(`<tag attr=\"x\">&value</tag>`)) || bytes.Contains(html, []byte(`\u003c`)) || bytes.Contains(html, []byte(`\u003e`)) || bytes.Contains(html, []byte(`\u0026`)) {
		t.Fatalf("translated JSON unexpectedly HTML-escaped: %s", html)
	}

	empty, err := marshalJSONNoEscapeLimited(envelope{}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	base := len(empty)
	clear(empty)
	for _, test := range []struct {
		name  string
		limit int64
	}{
		{name: "translated_request_8MiB", limit: MaxTranslatedRequestBytes},
		{name: "caller_nonstream_8MiB", limit: MaxCallerJSONResponseBytes},
	} {
		t.Run(test.name, func(t *testing.T) {
			limit := test.limit
			payload := envelope{Value: string(bytes.Repeat([]byte{'x'}, int(limit)-base))}
			exact, err := marshalJSONNoEscapeLimited(payload, limit)
			if err != nil || int64(len(exact)) != limit {
				clear(exact)
				t.Fatalf("exact boundary len=%d err=%v want=%d", len(exact), err, limit)
			}
			clear(exact)
			payload.Value += "x"
			over, err := marshalJSONNoEscapeLimited(payload, limit)
			clear(over)
			if !errors.Is(err, errJSONOutputLimit) {
				t.Fatalf("limit+1 err=%v", err)
			}
		})
	}
}
func TestCallerStreamBudgetExactLimitAndEventBoundary(t *testing.T) {
	budget := newCallerStreamBudget()
	normalLimit := MaxCallerStreamBytes - budget.errorFrameSize
	remaining := normalLimit
	for remaining > 0 {
		chunk := min(remaining, int64(MaxCallerSSEEventBytes))
		if err := budget.consume(int(chunk), false); err != nil {
			t.Fatalf("consume exact chunk=%d remaining=%d: %v", chunk, remaining, err)
		}
		remaining -= chunk
	}
	if budget.generated != normalLimit {
		t.Fatalf("generated=%d want=%d", budget.generated, normalLimit)
	}
	if err := budget.consume(1, false); !errors.Is(err, errCallerStreamLimit) {
		t.Fatalf("normal limit+1 err=%v", err)
	}
	if err := budget.consume(int(budget.errorFrameSize), true); err != nil {
		t.Fatalf("reserved error frame: %v", err)
	}
	if budget.generated != MaxCallerStreamBytes {
		t.Fatalf("generated with reserved error=%d want=%d", budget.generated, MaxCallerStreamBytes)
	}
	if err := budget.consume(1, true); !errors.Is(err, errCallerStreamLimit) {
		t.Fatalf("stream limit+1 err=%v", err)
	}

	events := newCallerStreamBudget()
	if err := events.consume(MaxCallerSSEEventBytes, false); err != nil {
		t.Fatalf("exact event: %v", err)
	}
	if err := events.consume(MaxCallerSSEEventBytes+1, false); !errors.Is(err, errCallerStreamLimit) {
		t.Fatalf("event limit+1 err=%v", err)
	}
}
