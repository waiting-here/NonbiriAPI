package forward

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
)

func (f *forwardFixture) addAnthropicRoute(t *testing.T, userID int64, baseURL, provider, model, upstreamModel, upstreamSecret string) fixtureRoute {
	t.Helper()
	canonical, err := f.stack.ValidateBaseURL(baseURL)
	if err != nil {
		t.Fatalf("ValidateBaseURL: %v", err)
	}
	endpointRow, err := f.store.CreateEndpoint(context.Background(), userID, string(endpoint.ConnectorAnthropicCompatible), canonical, "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	keyRow, err := f.store.CreateEndpointKey(context.Background(), userID, endpointRow.ID, []byte(upstreamSecret), "head", "tail", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := f.store.GetEndpointKeyCiphertext(context.Background(), userID, endpointRow.ID, keyRow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceFetchedModels(context.Background(), userID, endpointRow.ID, keyRow.ID, []db.FetchedModel{{
		EndpointKeyID: keyRow.ID, UpstreamModelID: upstreamModel, Provider: "anthropic", Status: "ok",
	}}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	modelRow, err := f.store.CreateModel(context.Background(), userID, provider, model, "ordered", false, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := f.store.CreateBinding(context.Background(), userID, modelRow.ID, keyRow.ID, upstreamModel, 0, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	return fixtureRoute{
		modelID: modelRow.ID, endpoint: endpointRow.ID, keyID: keyRow.ID,
		bindingID: binding.ID, cipher: ciphertext, fullName: provider + "/" + model,
	}
}

func TestAnthropicConnectorRunsThroughSharedPersonalRoute(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/v1/messages" {
			t.Errorf("request=%s %s", request.Method, request.URL)
		}
		if request.Header.Get("X-Api-Key") != "sk-anthropic-route" || request.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Errorf("headers=%v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		defer clear(body)
		var outbound struct {
			Model     string `json:"model"`
			MaxTokens int64  `json:"max_tokens"`
			Metadata  struct {
				UserID string `json:"user_id"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(body, &outbound); err != nil {
			t.Errorf("body=%s err=%v", body, err)
		}
		if outbound.Model != "claude-upstream" || outbound.MaxTokens != 65536 || !strings.HasPrefix(outbound.Metadata.UserID, "nbu_v3_") {
			t.Errorf("outbound=%+v", outbound)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"msg_route","type":"message","role":"assistant","model":"claude-upstream","content":[{"type":"text","text":"anthropic ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":2,"cache_creation_input_tokens":3,"cache_read_input_tokens":5,"output_tokens":7}}`)
	}))
	defer upstream.Close()

	fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{}, nil, nil)
	user := fixture.addUser(t, "anthropic-route")
	fixture.addAnthropicRoute(t, user.id, upstream.URL+"/v1", "p", "m", "claude-upstream", "sk-anthropic-route")
	fixture.codec.opens.Store(0)
	body := `{"model":"p/m","messages":[{"role":"system","content":"system"},{"role":"user","content":"hi"}]}`
	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, body))
	if response.Code != http.StatusOK || hits.Load() != 1 || fixture.codec.opens.Load() != 1 {
		t.Fatalf("status=%d body=%s hits=%d opens=%d", response.Code, response.Body.String(), hits.Load(), fixture.codec.opens.Load())
	}
	var completion struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &completion); err != nil {
		t.Fatal(err)
	}
	if completion.Model != "p/m" || len(completion.Choices) != 1 || completion.Choices[0].Message.Content != "anthropic ok" || completion.Usage.PromptTokens != 10 || completion.Usage.CompletionTokens != 7 || completion.Usage.TotalTokens != 17 {
		t.Fatalf("completion=%+v", completion)
	}
}
