package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func modelsPage(ids []string, hasMore bool) string {
	type entry struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	page := struct {
		Data    []entry `json:"data"`
		HasMore bool    `json:"has_more"`
		FirstID *string `json:"first_id"`
		LastID  *string `json:"last_id"`
	}{Data: make([]entry, 0, len(ids)), HasMore: hasMore}
	for _, id := range ids {
		page.Data = append(page.Data, entry{ID: id, Type: "model"})
	}
	if len(ids) > 0 {
		page.FirstID = &ids[0]
		page.LastID = &ids[len(ids)-1]
	}
	body, err := json.Marshal(page)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func discoveryInput(client backend.EndpointClient, plaintext, ciphertext []byte) connectorcontract.DiscoveryInput {
	return connectorcontract.DiscoveryInput{
		Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }},
		Target: connectorcontract.NewTarget(
			connectorcontract.TypeAnthropicCompatible,
			"https://api.example/v1",
			"",
		),
		Credential: connectorcontract.NewShortLivedSecret(plaintext, ciphertext),
	}
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func TestModelDiscoveryPaginationHeadersAndCredentialLifetime(t *testing.T) {
	requests := 0
	client := &fakeEndpointClient{baseURL: "https://api.example/v1"}
	client.do = func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet || request.URL.Path != "/v1/models" {
			t.Fatalf("request=%s %s", request.Method, request.URL)
		}
		if request.Header.Get("X-Api-Key") != "sk-discovery" || request.Header.Get("Anthropic-Version") != AnthropicVersion || request.Header.Get("Accept") != "application/json" {
			t.Fatalf("headers=%v", request.Header)
		}
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatalf("caller headers leaked: %v", request.Header)
		}
		query := request.URL.Query()
		if query.Get("limit") != "100" {
			t.Fatalf("limit=%q", query.Get("limit"))
		}
		switch requests {
		case 1:
			if query.Has("after_id") {
				t.Fatalf("first cursor=%q", query.Get("after_id"))
			}
			return fakeResponse(http.StatusOK, "application/json", modelsPage([]string{"claude-a", "claude/b"}, true)), nil
		case 2:
			if query.Get("after_id") != "claude/b" {
				t.Fatalf("second cursor=%q", query.Get("after_id"))
			}
			return fakeResponse(http.StatusOK, "application/json; charset=utf-8", modelsPage([]string{"claude-c"}, false)), nil
		default:
			t.Fatalf("unexpected request %d", requests)
			return nil, nil
		}
	}
	plaintext := []byte("sk-discovery")
	ciphertext := []byte("cipher-discovery")
	result := (ModelDiscoverer{}).Discover(context.Background(), discoveryInput(client, plaintext, ciphertext))
	if result.Diagnostic != "" || len(result.Models) != 3 || result.Models[0].ID != "claude-a" || result.Models[1].ID != "claude/b" || result.Models[2].ID != "claude-c" {
		t.Fatalf("result=%+v", result)
	}
	for _, model := range result.Models {
		if model.Provider != "anthropic" {
			t.Fatalf("provider=%q", model.Provider)
		}
	}
	if requests != 2 || !allZero(plaintext) || !allZero(ciphertext) {
		t.Fatalf("requests=%d plaintext=%v ciphertext=%v", requests, plaintext, ciphertext)
	}
}

func TestModelDiscoveryFailureBoundaries(t *testing.T) {
	largeBody := strings.Repeat("x", MaxModelsResponseBytes+1)
	tests := []struct {
		name      string
		ctx       func() context.Context
		do        func(*http.Request) (*http.Response, error)
		wantDiag  string
		wantCalls int
	}{
		{name: "404", do: func(*http.Request) (*http.Response, error) {
			return fakeResponse(http.StatusNotFound, "application/json", `{}`), nil
		}, wantDiag: "upstream returned status 404", wantCalls: 1},
		{name: "nonstandard response", do: func(*http.Request) (*http.Response, error) {
			return fakeResponse(http.StatusOK, "application/json", `{"data":[],"has_more":false,"unknown":1}`), nil
		}, wantDiag: "invalid upstream models response", wantCalls: 1},
		{name: "wrong media type", do: func(*http.Request) (*http.Response, error) {
			return fakeResponse(http.StatusOK, "text/plain", `{}`), nil
		}, wantDiag: "upstream response content type was invalid", wantCalls: 1},
		{name: "response limit", do: func(*http.Request) (*http.Response, error) {
			return fakeResponse(http.StatusOK, "application/json", largeBody), nil
		}, wantDiag: "upstream models response exceeded its limit", wantCalls: 1},
		{name: "canceled", ctx: func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }, do: func(request *http.Request) (*http.Response, error) { return nil, request.Context().Err() }, wantDiag: "model discovery canceled", wantCalls: 0},
		{name: "transport", do: func(*http.Request) (*http.Response, error) { return nil, errors.New("private transport detail") }, wantDiag: "upstream request failed", wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(request *http.Request) (*http.Response, error) {
				calls++
				return test.do(request)
			}}
			ctx := context.Background()
			if test.ctx != nil {
				ctx = test.ctx()
			}
			result := (ModelDiscoverer{}).Discover(ctx, discoveryInput(client, []byte("sk-key"), []byte("cipher")))
			if len(result.Models) != 0 || result.Diagnostic != test.wantDiag || calls != test.wantCalls || strings.Contains(result.Diagnostic, "private") {
				t.Fatalf("result=%+v calls=%d", result, calls)
			}
		})
	}
}

func TestModelDiscoveryCountPageCursorAndDuplicateLimits(t *testing.T) {
	tests := []struct {
		name string
		do   func(int, *http.Request) (*http.Response, error)
		want int
	}{
		{
			name: "twentieth page still has more",
			do: func(call int, _ *http.Request) (*http.Response, error) {
				return fakeResponse(http.StatusOK, "application/json", modelsPage([]string{"m-" + strconv.Itoa(call)}, true)), nil
			},
			want: MaxDiscoveryPages,
		},
		{
			name: "model count exceeds maximum",
			do: func(call int, _ *http.Request) (*http.Response, error) {
				ids := make([]string, 100)
				for index := range ids {
					ids[index] = fmt.Sprintf("m-%04d", (call-1)*100+index)
				}
				return fakeResponse(http.StatusOK, "application/json", modelsPage(ids, true)), nil
			},
			want: 10,
		},
		{
			name: "duplicate id",
			do: func(call int, _ *http.Request) (*http.Response, error) {
				return fakeResponse(http.StatusOK, "application/json", modelsPage([]string{"same"}, call == 1)), nil
			},
			want: 2,
		},
		{
			name: "cursor byte limit",
			do: func(_ int, _ *http.Request) (*http.Response, error) {
				return fakeResponse(http.StatusOK, "application/json", modelsPage([]string{strings.Repeat("界", 300)}, true)), nil
			},
			want: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(request *http.Request) (*http.Response, error) {
				calls++
				return test.do(calls, request)
			}}
			result := (ModelDiscoverer{}).Discover(context.Background(), discoveryInput(client, []byte("sk-key"), []byte("cipher")))
			if result.Diagnostic != "invalid upstream models response" || len(result.Models) != 0 || calls != test.want {
				t.Fatalf("result=%+v calls=%d want=%d", result, calls, test.want)
			}
		})
	}
}

func TestModelDiscoveryRejectsCredentialReflectionBeforeCache(t *testing.T) {
	key := `sk-"quoted`
	ciphertext := "cipher-sensitive"
	tests := []struct {
		name string
		body string
	}{
		{name: "exact plaintext", body: modelsPage([]string{key}, false)},
		{name: "plaintext substring", body: modelsPage([]string{"prefix-" + key + "-suffix"}, false)},
		{name: "exact ciphertext", body: modelsPage([]string{ciphertext}, false)},
		{name: "ciphertext substring", body: modelsPage([]string{"prefix-" + ciphertext + "-suffix"}, false)},
		// The raw bytes do not contain sk-secret, but JSON decoding does. The
		// semantic guard must reject this before a model can enter the cache.
		{name: "JSON unicode escape", body: `{"data":[{"id":"sk-\u0073ecret","type":"model"}],"has_more":false,"first_id":"sk-\u0073ecret","last_id":"sk-\u0073ecret"}`},
		{name: "ciphertext JSON unicode escape", body: `{"data":[{"id":"cipher-\u0073ensitive","type":"model"}],"has_more":false,"first_id":"cipher-\u0073ensitive","last_id":"cipher-\u0073ensitive"}`},
		{name: "JSON surrogate pair", body: `{"data":[{"id":"model-\ud83d\ude00","type":"model"}],"has_more":false,"first_id":"model-\ud83d\ude00","last_id":"model-\ud83d\ude00"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plain := key
			if test.name == "JSON unicode escape" {
				plain = "sk-secret"
			}
			cipher := ciphertext
			if test.name == "ciphertext JSON unicode escape" {
				cipher = "cipher-sensitive"
			}
			if test.name == "JSON surrogate pair" {
				plain = "model-😀"
			}
			client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
				return fakeResponse(http.StatusOK, "application/json", test.body), nil
			}}
			result := (ModelDiscoverer{}).Discover(context.Background(), discoveryInput(client, []byte(plain), []byte(cipher)))
			if result.Diagnostic != "upstream models response was rejected" || len(result.Models) != 0 || strings.Contains(result.Diagnostic, plain) || strings.Contains(result.Diagnostic, cipher) {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestParseModelsPageAcceptsCurrentOfficialAndCompatibleModelInfoShape(t *testing.T) {
	body := []byte(`{
		"data":[{
			"id":"claude-opus-4-1","type":"model","display_name":"Claude Opus 4.1","created_at":"2026-08-23T00:00:00Z",
			"capabilities":{
				"batch":{"supported":true},"citations":{"supported":true},"code_execution":{"supported":false},
				"context_management":{"supported":true,"clear_thinking_20251015":{"supported":true},"clear_tool_uses_20250919":{"supported":true},"compact_20260112":null},
				"effort":{"supported":true,"high":{"supported":true},"low":{"supported":true},"max":{"supported":false},"medium":{"supported":true},"xhigh":null},
				"image_input":{"supported":true},"pdf_input":{"supported":true},"structured_outputs":{"supported":true},
				"thinking":{"supported":true,"types":{"adaptive":{"supported":true},"enabled":{"supported":true}}}
			},
			"max_input_tokens":200000,"max_tokens":64000
		},{"id":"claude-null-capabilities","type":"model","capabilities":null,"max_input_tokens":null,"max_tokens":null}],
		"has_more":false,"first_id":"claude-opus-4-1","last_id":"claude-null-capabilities"
	}`)
	models, hasMore, lastID, err := parseModelsPage(body)
	if err != nil || hasMore || lastID != "claude-null-capabilities" || len(models) != 2 || models[0].ID != "claude-opus-4-1" || models[1].ID != "claude-null-capabilities" {
		t.Fatalf("models=%+v has_more=%v last_id=%q err=%v", models, hasMore, lastID, err)
	}
}

func TestParseModelsPageRejectsInvalidPaginationAndModelInfo(t *testing.T) {
	baseEntry := `{"id":"m1","type":"model"}`
	tests := []struct {
		name string
		body string
	}{
		{name: "missing first id", body: `{"data":[` + baseEntry + `],"has_more":false,"last_id":"m1"}`},
		{name: "missing last id", body: `{"data":[` + baseEntry + `],"has_more":false,"first_id":"m1"}`},
		{name: "first mismatch", body: `{"data":[` + baseEntry + `],"has_more":false,"first_id":"other","last_id":"m1"}`},
		{name: "last mismatch", body: `{"data":[` + baseEntry + `],"has_more":false,"first_id":"m1","last_id":"other"}`},
		{name: "empty nonnull anchors", body: `{"data":[],"has_more":false,"first_id":"m1","last_id":"m1"}`},
		{name: "empty has more", body: `{"data":[],"has_more":true,"first_id":null,"last_id":null}`},
		{name: "negative max input", body: `{"data":[{"id":"m1","type":"model","max_input_tokens":-1}],"has_more":false,"first_id":"m1","last_id":"m1"}`},
		{name: "fraction max tokens", body: `{"data":[{"id":"m1","type":"model","max_tokens":1.5}],"has_more":false,"first_id":"m1","last_id":"m1"}`},
		{name: "invalid created at", body: `{"data":[{"id":"m1","type":"model","created_at":"not-a-time"}],"has_more":false,"first_id":"m1","last_id":"m1"}`},
		{name: "null display name", body: `{"data":[{"id":"m1","type":"model","display_name":null}],"has_more":false,"first_id":"m1","last_id":"m1"}`},
		{name: "null created at", body: `{"data":[{"id":"m1","type":"model","created_at":null}],"has_more":false,"first_id":"m1","last_id":"m1"}`},
		{name: "unknown capability", body: `{"data":[{"id":"m1","type":"model","capabilities":{"future":{"supported":true}}}],"has_more":false,"first_id":"m1","last_id":"m1"}`},
		{name: "unknown nested capability", body: `{"data":[{"id":"m1","type":"model","capabilities":{"batch":{"supported":true,"future":1}}}],"has_more":false,"first_id":"m1","last_id":"m1"}`},
		{name: "missing required capability", body: `{"data":[{"id":"m1","type":"model","capabilities":{"batch":{"supported":true}}}],"has_more":false,"first_id":"m1","last_id":"m1"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			models, _, _, err := parseModelsPage([]byte(test.body))
			if !errors.Is(err, ErrInvalidResponse) || len(models) != 0 {
				t.Fatalf("models=%+v err=%v", models, err)
			}
		})
	}
}

func TestModelsPageURLAddsEncodedCursor(t *testing.T) {
	got, err := modelsPageURL("https://api.example/v1", "claude/a b")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/v1/models" || parsed.Query().Get("limit") != "100" || parsed.Query().Get("after_id") != "claude/a b" {
		t.Fatalf("URL=%s", got)
	}
}
