package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const googleChatHistory = `{"role":"assistant","content":"answer","reasoning_content":"consider","extra_content":{"google":{"thought_signature":"text+/opaque="}},"tool_calls":[{"id":"call","type":"function","function":{"name":"clock","arguments":"{\"n\":9007199254740993}"},"extra_content":{"google":{"thought_signature":"tool+/opaque="}}}]}`

func compiledHistory(t *testing.T, body []byte) json.RawMessage {
	t.Helper()
	var value struct {
		Prompt []struct {
			Content json.RawMessage `json:"content"`
		} `json:"prompt"`
	}
	if json.Unmarshal(body, &value) != nil || len(value.Prompt) == 0 {
		t.Fatal(string(body))
	}
	return value.Prompt[0].Content
}

func equalParts(t *testing.T, actual []byte, expected string) {
	t.Helper()
	if !sameJSON([]byte(`{"parts":`+string(actual)+`}`), []byte(`{"parts":`+expected+`}`)) {
		t.Fatalf("parts = %s, want %s", actual, expected)
	}
}

func TestGatewayGoogleChatSignaturesBindToTheirParts(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		t.Run(string(kind), func(t *testing.T) {
			request := chat(t, `{"model":"public","messages":[`+googleChatHistory+`,{"role":"tool","tool_call_id":"call","content":"done"}]}`)
			defer request.Clear()
			body, err := compileChatForType(kind, request, "server-owner", nil)
			if err != nil {
				t.Fatal(err)
			}
			equalParts(t, compiledHistory(t, body), `[{"type":"reasoning","text":"consider"},{"type":"text","text":"answer","providerOptions":{"google":{"thoughtSignature":"text+/opaque="}}},{"type":"tool-call","toolCallId":"call","toolName":"clock","input":{"n":9007199254740993},"providerOptions":{"google":{"thoughtSignature":"tool+/opaque="}}}]`)
			if !strings.Contains(string(body), `"gateway":{"user":"server-owner"}`) || strings.Contains(string(body), "providerMetadata") {
				t.Fatal(string(body))
			}
		})
	}
	// With no visible text, the message signature remains on its reasoning part.
	request := chat(t, `{"model":"public","messages":[{"role":"assistant","content":null,"reasoning_content":"consider","extra_content":{"google":{"thought_signature":"reason"}}}]}`)
	defer request.Clear()
	body, err := compileChat(request, "")
	if err != nil {
		t.Fatal(err)
	}
	equalParts(t, compiledHistory(t, body), `[{"type":"reasoning","text":"consider","providerOptions":{"google":{"thoughtSignature":"reason"}}}]`)
}

func TestGatewayGoogleNativePartsPreserveOrderAndOptionalCallID(t *testing.T) {
	const native = `[{"text":"consider","thought":true,"thoughtSignature":"reason"},{"text":"ans"},{"functionCall":{"name":"clock","args":{"n":9007199254740993}},"thoughtSignature":"tool+/opaque="},{"text":"wer","thoughtSignature":"text+/opaque="}]`
	const want = `[{"type":"reasoning","text":"consider","providerOptions":{"google":{"thoughtSignature":"reason"}}},{"type":"text","text":"ans"},{"type":"tool-call","toolCallId":"call","toolName":"clock","input":{"n":9007199254740993},"providerOptions":{"google":{"thoughtSignature":"tool+/opaque="}}},{"type":"text","text":"wer","providerOptions":{"google":{"thoughtSignature":"text+/opaque="}}}]`
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		// Both native signatures and their standard Chat copies must agree.
		message := strings.Replace(googleChatHistory, `"google":{"thought_signature":"text+/opaque="}`, `"google":{"thought_signature":"text+/opaque=","parts":`+native+`}`, 1)
		request := chat(t, `{"model":"public","messages":[`+message+`]}`)
		body, err := compileChatForType(kind, request, "", nil)
		request.Clear()
		if err != nil {
			t.Fatal(err)
		}
		equalParts(t, compiledHistory(t, body), want)
	}
}

func TestGatewayGoogleStandardSignaturesMergeWithGatewayHistory(t *testing.T) {
	const native = `[{"type":"reasoning","text":"consider"},{"type":"text","text":"ans"},{"type":"tool-call","toolCallId":"call","toolName":"clock","input":"{\"n\":9007199254740993}","providerMetadata":{"google":{"thoughtSignature":"tool+/opaque="}}},{"type":"text","text":"wer","providerMetadata":{"google":{"thoughtSignature":"text+/opaque="}}}]`
	message := strings.Replace(googleChatHistory, `"extra_content":{"google":{"thought_signature":"text+/opaque="}}`, `"extra_content":{"google":{"thought_signature":"text+/opaque="},"gateway":{"content":`+native+`}}`, 1)
	request := chat(t, `{"model":"public","messages":[`+message+`]}`)
	defer request.Clear()
	body, err := compileChat(request, "")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(strings.ReplaceAll(native, "providerMetadata", "providerOptions"), `"input":"{\"n\":9007199254740993}"`, `"input":{"n":9007199254740993}`)
	equalParts(t, compiledHistory(t, body), want)
	for _, signature := range []string{"text+/opaque=", "tool+/opaque="} {
		// Change only the native copy, leaving the standard Chat signature intact.
		changedNative := strings.Replace(native, signature, "different", 1)
		changed := strings.Replace(message, native, changedNative, 1)
		replay := chat(t, `{"model":"public","messages":[`+changed+`]}`)
		_, err := compileChat(replay, "")
		replay.Clear()
		if err == nil {
			t.Fatal("conflicting Gateway signature accepted")
		}
	}
}

func TestGatewayGoogleMessageSignatureDoesNotMoveToLaterReasoning(t *testing.T) {
	request := chat(t, `{"model":"public","messages":[{"role":"assistant","content":"answer","reasoning_content":"consider","extra_content":{"google":{"thought_signature":"visible","parts":[{"text":"answer"},{"text":"consider","thought":true,"thoughtSignature":"reason"}]}}}]}`)
	defer request.Clear()
	body, err := compileChat(request, "")
	if err != nil {
		t.Fatal(err)
	}
	equalParts(t, compiledHistory(t, body), `[{"type":"text","text":"answer","providerOptions":{"google":{"thoughtSignature":"visible"}}},{"type":"reasoning","text":"consider","providerOptions":{"google":{"thoughtSignature":"reason"}}}]`)
}

func TestGatewayGoogleParallelCallSignatureIsNotCopied(t *testing.T) {
	const message = `{"role":"assistant","content":null,"tool_calls":[{"id":"a","type":"function","function":{"name":"clock","arguments":"{}"},"extra_content":{"google":{"thought_signature":"first"}}},{"id":"b","type":"function","function":{"name":"clock","arguments":"{}"}}],"extra_content":{"google":{"parts":[{"functionCall":{"name":"clock"},"thoughtSignature":"first"},{"functionCall":{"name":"clock"}}]}}}`
	request := chat(t, `{"model":"public","messages":[`+message+`]}`)
	defer request.Clear()
	body, err := compileChat(request, "")
	if err != nil {
		t.Fatal(err)
	}
	equalParts(t, compiledHistory(t, body), `[{"type":"tool-call","toolCallId":"a","toolName":"clock","input":{},"providerOptions":{"google":{"thoughtSignature":"first"}}},{"type":"tool-call","toolCallId":"b","toolName":"clock","input":{}}]`)
}

func TestGatewayGoogleContinuationRejectsLossAndConflicts(t *testing.T) {
	native := `[{"text":"consider","thought":true},{"text":"answer","thoughtSignature":"text+/opaque="},{"functionCall":{"name":"clock","args":{"n":9007199254740993}},"thoughtSignature":"tool+/opaque="}]`
	withNative := func(value string) string {
		return strings.Replace(googleChatHistory, `"google":{"thought_signature":"text+/opaque="}`, `"google":{"parts":`+value+`}`, 1)
	}
	cases := map[string]string{
		"message credential":            strings.Replace(googleChatHistory, `"thought_signature":"text+/opaque="`, `"thought_signature":"text+/opaque=","apiKey":"override"`, 1),
		"tool routing":                  strings.Replace(googleChatHistory, `"thought_signature":"tool+/opaque="`, `"thought_signature":"tool+/opaque=","providerOptions":{"gateway":{"order":["other"]}}`, 1),
		"empty signature":               strings.Replace(googleChatHistory, `"tool+/opaque="`, `""`, 1),
		"nonstring signature":           strings.Replace(googleChatHistory, `"tool+/opaque="`, `123`, 1),
		"invalid affinity":              strings.Replace(googleChatHistory, `"extra_content":{"google":`, `"extra_content":{"nonbiri":{"continuation_id":"bad"},"google":`, 1),
		"no message attachment":         `{"role":"assistant","content":null,"extra_content":{"google":{"thought_signature":"sig"}},"tool_calls":[{"id":"call","type":"function","function":{"name":"clock","arguments":"{}"}}]}`,
		"user signature":                `{"role":"user","content":"hi","extra_content":{"google":{"thought_signature":"sig"}}}`,
		"native media":                  withNative(`[{"inlineData":{"mimeType":"image/png","data":"YWJj"}}]`),
		"standalone signature":          withNative(`[{"thoughtSignature":"opaque"}]`),
		"native wrong text":             withNative(strings.Replace(native, `"answer"`, `"changed"`, 1)),
		"native wrong arguments":        withNative(strings.Replace(native, "9007199254740993", "9007199254740992", 1)),
		"native wrong ID":               withNative(strings.Replace(native, `"functionCall":{`, `"functionCall":{"id":"different",`, 1)),
		"native wrong name":             withNative(strings.Replace(native, `"clock"`, `"different"`, 1)),
		"native call extension":         withNative(strings.Replace(native, `"functionCall":{`, `"functionCall":{"willContinue":true,`, 1)),
		"conflicting tool signature":    withNative(strings.Replace(native, "tool+/opaque=", "different", 1)),
		"conflicting message signature": strings.Replace(withNative(native), `"google":{"parts":`, `"google":{"thought_signature":"different","parts":`, 1),
		"gateway credential":            strings.Replace(googleChatHistory, `"google":{"thought_signature":"text+/opaque="}`, `"gateway":{"content":[{"type":"reasoning","text":"consider"},{"type":"text","text":"answer","providerMetadata":{"google":{"apiKey":"override"}}},{"type":"tool-call","toolCallId":"call","toolName":"clock","input":"{\"n\":9007199254740993}"}]}`, 1),
	}
	for name, message := range cases {
		t.Run(name, func(t *testing.T) {
			request := chat(t, `{"model":"public","messages":[`+message+`]}`)
			defer request.Clear()
			if _, err := compileChat(request, ""); err == nil {
				t.Fatal("unrepresentable continuation accepted")
			}
		})
	}
	// A public projection mismatch is diagnosed without logging the signature.
	request := chat(t, `{"model":"public","messages":[`+withNative(strings.Replace(native, `"answer"`, `"changed"`, 1))+`]}`)
	defer request.Clear()
	_, err := compileChat(request, "")
	var rejection *contract.RequestRejection
	if !errors.As(err, &rejection) || rejection.Field != "extra_content.google.parts" || strings.Contains(err.Error(), "opaque") {
		t.Fatal(err)
	}
}

func TestGatewayGoogleChatJSONAndSSEContinuationRoundTrip(t *testing.T) {
	const native = `[{"type":"text","text":"next","providerMetadata":{"google":{"thoughtSignature":"next-text"}}},{"type":"tool-call","toolCallId":"next-call","toolName":"clock","input":"{}","providerMetadata":{"google":{"thoughtSignature":"next-tool"}}}]`
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", kind, streaming), func(t *testing.T) {
				upstream := `{"content":` + native + `,"finishReason":{"unified":"tool-calls"},"usage":` + knownUsage + `}`
				if streaming {
					upstream = sse(`{"type":"text-start","id":"t"}`, `{"type":"text-delta","id":"t","delta":"next"}`, `{"type":"text-end","id":"t","providerMetadata":{"google":{"thoughtSignature":"next-text"}}}`, `{"type":"tool-call","toolCallId":"next-call","toolName":"clock","input":"{}","providerMetadata":{"google":{"thoughtSignature":"next-tool"}}}`, `{"type":"finish","finishReason":{"unified":"tool-calls"},"usage":`+knownUsage+`}`)
				}
				b := &fakeBackend{do: func(request *http.Request) (*http.Response, error) {
					body, err := io.ReadAll(request.Body)
					if err != nil || !strings.Contains(string(body), `"thoughtSignature":"tool+/opaque="`) || strings.Contains(string(body), "thought_signature") {
						t.Fatalf("outgoing = %s, err=%v", body, err)
					}
					return response(upstream, "application/json"), nil
				}}
				a, _ := NewAdapter(b)
				request := chat(t, fmt.Sprintf(`{"model":"public","messages":[%s,{"role":"tool","tool_call_id":"call","content":"done"}],"stream":%t}`, googleChatHistory, streaming))
				defer request.Clear()
				w := httptest.NewRecorder()
				result := a.AttemptWithPolicy(context.Background(), w, contract.NewTarget(kind, target().BaseURL(), "private"), secret(), request, nil, "", contract.AttemptPolicy{ContinuationID: strings.Repeat("A", 43)})
				if !result.Success || !result.Usage.Present || b.calls != 1 {
					t.Fatal(result, w.Body.String())
				}
				message := googleReplayMessage(t, w.Body.String(), streaming)
				if !strings.Contains(string(message), "continuation_id") {
					t.Fatal(string(message))
				}
				replay := chat(t, `{"model":"public","messages":[`+string(message)+`,{"role":"tool","tool_call_id":"next-call","content":"done"}]}`)
				defer replay.Clear()
				body, err := compileChatForType(kind, replay, "", nil)
				if err != nil {
					t.Fatal(err, string(message))
				}
				equalParts(t, compiledHistory(t, body), strings.ReplaceAll(strings.ReplaceAll(native, "providerMetadata", "providerOptions"), `"input":"{}"`, `"input":{}`))
			})
		}
	}
}

func TestGatewayGoogleStreamLateToolSignature(t *testing.T) {
	for _, at := range []string{"tool-input-delta", "tool-input-end"} {
		for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
			t.Run(string(kind)+"/"+at, func(t *testing.T) {
				delta, end := `{"type":"tool-input-delta","id":"next-call","delta":"{}"}`, `{"type":"tool-input-end","id":"next-call"}`
				metadata := `,"providerMetadata":{"google":{"thoughtSignature":"late"}}}`
				if at == "tool-input-delta" {
					delta = strings.TrimSuffix(delta, "}") + metadata
				} else {
					end = strings.TrimSuffix(end, "}") + metadata
				}
				body := sse(`{"type":"tool-input-start","id":"next-call","toolName":"clock"}`, delta, end, `{"type":"tool-call","toolCallId":"next-call","toolName":"clock","input":"{}"}`, `{"type":"finish","finishReason":{"unified":"tool-calls"},"usage":`+knownUsage+`}`)
				b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(body, "text/event-stream"), nil }}
				a, _ := NewAdapter(b)
				request := chat(t, `{"model":"public","messages":[{"role":"user","content":"hi"}],"stream":true}`)
				defer request.Clear()
				w := httptest.NewRecorder()
				result := a.Attempt(context.Background(), w, contract.NewTarget(kind, target().BaseURL(), "private"), secret(), request, nil, "")
				if !result.Success || !result.Usage.Present {
					t.Fatal(result, w.Body.String())
				}
				message := googleReplayMessage(t, w.Body.String(), true)
				replay := chat(t, `{"model":"public","messages":[`+string(message)+`,{"role":"tool","tool_call_id":"next-call","content":"done"}]}`)
				defer replay.Clear()
				compiled, err := compileChatForType(kind, replay, "", nil)
				if err != nil {
					t.Fatal(err, string(message))
				}
				equalParts(t, compiledHistory(t, compiled), `[{"type":"tool-call","toolCallId":"next-call","toolName":"clock","input":{},"providerOptions":{"google":{"thoughtSignature":"late"}}}]`)
			})
		}
	}
}

// Keep actual emitted text, incremental tool arguments and the terminal
// continuation when constructing the next Chat turn.
func googleReplayMessage(t *testing.T, body string, streaming bool) []byte {
	t.Helper()
	var envelope struct {
		Choices []struct {
			Message json.RawMessage `json:"message"`
			Delta   object          `json:"delta"`
		} `json:"choices"`
	}
	if !streaming {
		if json.Unmarshal([]byte(body), &envelope) != nil || len(envelope.Choices) != 1 {
			t.Fatal(body)
		}
		return envelope.Choices[0].Message
	}
	message := object{"role": json.RawMessage(`"assistant"`)}
	var content strings.Builder
	calls := []toolCall{}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		envelope.Choices = nil
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &envelope) != nil || len(envelope.Choices) != 1 {
			t.Fatal(line)
		}
		delta := envelope.Choices[0].Delta
		if raw, present := delta["content"]; present {
			value, ok := text(raw)
			if !ok {
				t.Fatal(line)
			}
			content.WriteString(value)
		}
		if raw, present := delta["extra_content"]; present {
			message["extra_content"] = raw
		}
		if raw, present := delta["tool_calls"]; present {
			var chunks []struct {
				Index int `json:"index"`
				toolCall
			}
			if json.Unmarshal(raw, &chunks) != nil {
				t.Fatal(line)
			}
			for _, chunk := range chunks {
				if chunk.Index < 0 || chunk.Index > len(calls) {
					t.Fatal(line)
				}
				if chunk.Index == len(calls) {
					calls = append(calls, toolCall{})
				}
				call := &calls[chunk.Index]
				if chunk.ID != "" {
					call.ID, call.Type, call.Function.Name = chunk.ID, chunk.Type, chunk.Function.Name
				}
				call.Function.Arguments += chunk.Function.Arguments
			}
		}
	}
	if len(calls) > 0 {
		message["tool_calls"], _ = json.Marshal(calls)
	}
	message["content"], _ = json.Marshal(content.String())
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
