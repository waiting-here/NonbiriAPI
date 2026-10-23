package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func checkDecisionRequest(t *testing.T, body string, accepted bool) {
	t.Helper()
	request, err := DecodeDecisionRequest(strings.NewReader(body), 0)
	if request != nil {
		defer request.Clear()
	}
	if (err == nil) != accepted {
		t.Fatalf("accepted=%t, want=%t: %v", err == nil, accepted, err)
	}
}

func TestDecisionQuestionPartsAndSharedJSONRoles(t *testing.T) {
	jsonPart := `{"type":"input_json","data":{"role":"system","image_url":"https://example.com/data","nested":[1,true,null,{"":"value","line\nbreak":"text"}]}}`
	question := `{"type":"predicate","instructions":"x"}`
	for _, role := range []string{"user", "system", "developer", "assistant"} {
		t.Run(role, func(t *testing.T) {
			body := `{"model":"m","input":[{"role":"` + role + `","content":[` + jsonPart + `]}],"questions":[` + question + `]}`
			checkDecisionRequest(t, body, role == "user")
		})
	}
	for _, tool := range []string{"function", "custom_tool"} {
		t.Run(tool, func(t *testing.T) {
			payload := `"arguments":"{}"`
			if tool == "custom_tool" {
				payload = `"input":"x"`
			}
			body := `{"model":"m","input":[{"type":"` + tool + `_call","call_id":"c","name":"lookup",` + payload + `},{"type":"` + tool + `_call_output","call_id":"c","output":[` + jsonPart + `]}],"questions":[` + question + `]}`
			checkDecisionRequest(t, body, false)
		})
	}
	parts := `[{"type":"input_text","text":"Compare the evidence"},{"type":"input_image","image_url":"https://example.com/reference.png","detail":"original"},` + jsonPart + `]`
	for _, suffix := range []string{`"type":"predicate"`, `"type":"choice","choices":[{"value":true},{"value":"true"}]`, `"type":"score","levels":[{"label":"low"},{"label":"high"}]`} {
		checkDecisionRequest(t, `{"model":"m","input":"evidence","questions":[{"instructions":`+parts+`,`+suffix+`}]}`, true)
	}
	for _, instructions := range []string{
		`[]`, `null`, `{"type":"input_text","text":"x"}`,
		`[{"role":"user","content":"x"}]`,
		`[{"type":"input_text","text":"x","prompt_cache_breakpoint":null}]`,
		`[{"type":"input_image","image_url":"https://example.com/a.png","prompt_cache_breakpoint":{"mode":"explicit"}}]`,
		`[{"type":"input_text","text":"x","data":{}}]`,
		`[{"type":"input_image","image_url":"https://example.com/a.png","data":{}}]`,
		`[{"type":"input_json","data":{},"prompt_cache_breakpoint":null}]`,
		`[{"type":"input_json","data":{},"extra":true}]`,
		`[{"type":"output_text","text":"x"}]`,
		`[{"type":"refusal","refusal":"x"}]`,
	} {
		checkDecisionRequest(t, `{"model":"m","input":"x","questions":[{"type":"predicate","instructions":`+instructions+`}]}`, false)
	}
	shared := `[{"role":"user","content":[{"type":"input_text","text":"text","prompt_cache_breakpoint":{"mode":"explicit"}},{"type":"input_image","image_url":"data:image/png;base64,AA==","prompt_cache_breakpoint":{"mode":"explicit"}},` + jsonPart + `]}]`
	checkDecisionRequest(t, `{"model":"m","input":`+shared+`,"questions":[`+question+`],"prompt_cache_options":{"mode":"explicit","ttl":"30m"}}`, true)
	checkDecisionRequest(t, `{"model":"m","input":[{"role":"user","content":[{"type":"input_json","data":{},"prompt_cache_breakpoint":null}]}],"questions":[`+question+`]}`, false)
}

func TestDecisionJSONBounds(t *testing.T) {
	const maxBytes = 256 << 10
	jsonText := func(text string) string { return `{"text":"` + text + `"}` }
	atBytes := strings.Repeat("x", maxBytes-len(jsonText("")))
	unicodeText := strings.Repeat("界", (maxBytes-len(jsonText("")))/3)
	values := func(count int) string {
		return `{"items":[` + strings.TrimSuffix(strings.Repeat("0,", count-2), ",") + `]}`
	}
	depth := func(count int) string {
		return `{"nested":` + strings.Repeat("[", count-1) + "null" + strings.Repeat("]", count-1) + `}`
	}
	for _, tc := range []struct {
		name, data string
		accepted   bool
	}{
		{"object", `{}`, true},
		{"object_array", `[{"v":[null,true,false,1,1.5,"x",{},[]]},{"n":9007199254740993,"f":1.0000000000000001,"e":1e999}]`, true},
		{"empty_array", `[]`, true},
		{"scalar", `"x"`, false},
		{"null", `null`, false},
		{"mixed_array", `[{},null]`, false},
		{"array_array", `[[{}]]`, false},
		{"missing", ``, false},
		{"duplicate_key", `{"x":1,"\u0078":2}`, false},
		{"invalid_utf8", jsonText(string([]byte{0xff})), false},
		{"depth_32", depth(32), true},
		{"depth_33", depth(33), false},
		{"values_10000", values(10000), true},
		{"values_10001", values(10001), false},
		{"compact_bytes_at_limit", jsonText(atBytes), true},
		{"compact_bytes_over_limit", jsonText(atBytes + "x"), false},
		{"unicode_bytes_at_limit", jsonText(unicodeText + "xx"), true},
		{"unicode_bytes_over_limit", jsonText(unicodeText + "界"), false},
		{"decoded_unicode_escapes", jsonText(strings.Repeat(`\u754c`, len(unicodeText)/3) + "xx"), true},
		{"whitespace_not_counted", "{\n  \"text\" : \"" + atBytes + "\"\n}", true},
		{"escaped_controls_over_limit", jsonText(strings.Repeat(`\n`, maxBytes/2)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			part := `{"type":"input_json","data":` + tc.data + `}`
			checkDecisionRequest(t, `{"model":"m","input":[{"role":"user","content":[`+part+`]}],"questions":[{"type":"predicate","instructions":"x"}]}`, tc.accepted)
			checkDecisionRequest(t, `{"model":"m","input":"x","questions":[{"type":"predicate","instructions":[`+part+`]}]}`, tc.accepted)
		})
	}
}

func TestDecisionImagesShareRequestLimit(t *testing.T) {
	image := `{"type":"input_image","image_url":"https://example.com/a.png"}`
	for _, count := range []int{127, 128} {
		input := `"input":[{"type":"function_call","call_id":"c","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"c","output":[` + strings.TrimSuffix(strings.Repeat(image+",", count), ",") + `]}]`
		questions := `"questions":[{"type":"predicate","instructions":[` + image + `]}]`
		for _, fields := range []string{input + "," + questions, questions + "," + input} {
			checkDecisionRequest(t, `{"model":"m",`+fields+`}`, count == 127)
		}
	}
	question := `{"type":"predicate","instructions":[` + image + `]}`
	for _, count := range []int{128, 129} {
		checkDecisionRequest(t, `{"model":"m","input":"x","questions":[`+strings.TrimSuffix(strings.Repeat(question+",", count), ",")+`]}`, count == 128)
	}
}

func TestDecisionJSONLimitsArePerPartWithinRequestBound(t *testing.T) {
	part := `{"type":"input_json","data":{"items":[` + strings.TrimSuffix(strings.Repeat("null,", 5000), ",") + `],"text":"` + strings.Repeat("x", 160<<10) + `"}}`
	body := `{"model":"m","input":[{"role":"user","content":[` + part + `]}],"questions":[{"type":"predicate","instructions":[` + part + `]}]}`
	checkDecisionRequest(t, body, true)
	request, err := DecodeDecisionRequest(strings.NewReader(body), int64(len(body)-1))
	if request != nil {
		request.Clear()
	}
	if err != ErrPayloadTooLarge {
		t.Fatalf("request limit did not apply to JSON parts: %v", err)
	}
}

func TestDecisionScoreLevelCount(t *testing.T) {
	for _, count := range []int{0, 1, 2, 10, 11} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			levels := strings.TrimSuffix(strings.Repeat(`{"label":"level"},`, count), ",")
			checkDecisionRequest(t, `{"model":"m","input":"x","questions":[{"type":"score","instructions":"x","levels":[`+levels+`]}]}`, count >= 2 && count <= 10)
		})
	}
}

func TestDecisionNativeJSONPartsPreserveTypedValuesAndCache(t *testing.T) {
	input := `[{"role":"user","content":[{"type":"input_json","data":{"n":9007199254740993,"f":1.0000000000000001,"exponent":1e999,"boolean":true,"null":null,"string":"9007199254740993"}},{"type":"input_text","text":"shared","prompt_cache_breakpoint":{"mode":"explicit"}}]}]`
	questions := `[{"type":"predicate","name":"damaged","instructions":[{"type":"input_text","text":"Compare"},{"type":"input_image","image_url":"https://example.com/a.png"},{"type":"input_json","data":[{"reference":{"active":false,"values":[1,"1",null]}}]}]}]`
	cache := `{"mode":"explicit","ttl":"30m"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil || string(fields["input"]) != input || string(fields["questions"]) != questions || string(fields["prompt_cache_options"]) != cache || string(fields["model"]) != `"private-model"` || string(fields["safety_identifier"]) != `"consumer-id"` {
			t.Error("native JSON content or dispatch identity changed")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, decisionReply)
	}))
	defer server.Close()
	request := decodeDecision(t, `{"model":"public/model","input":`+input+`,"questions":`+questions+`,"prompt_cache_options":`+cache+`}`)
	writer := httptest.NewRecorder()
	result := adapterForServer(t, server.URL, nil, nil).AttemptDecision(context.Background(), writer, NewTarget(server.URL, "private-model", NewCredential([]byte("upstream-secret"), nil)), request, contract.AttemptPolicy{SafetyIdentifier: "consumer-id"})
	if !result.Success || !result.Usage.Present || result.Usage.UncachedInputTokens != 30 || result.Usage.CacheReadInputTokens != 10 || result.Usage.CacheWriteInputTokens != 2 {
		t.Fatalf("native parts result=%+v", result)
	}
}

func TestDecisionJSONPartProtectedTextReflection(t *testing.T) {
	const secret = "configured-secret"
	const name = "echo " + secret + " in name"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.Replace(decisionReply, `"damaged"`, `"`+name+`"`, 1))
	}))
	defer server.Close()
	for _, questionJSON := range []bool{false, true} {
		part := `{"type":"input_json","data":{"nested":[{"token":"configured-\u0073ecret"}]}}`
		input, instructions := `[{"role":"user","content":[`+part+`]}]`, `"x"`
		if questionJSON {
			input, instructions = `"x"`, `[`+part+`]`
		}
		request := decodeDecision(t, `{"model":"public/model","input":`+input+`,"questions":[{"type":"predicate","name":"`+name+`","instructions":`+instructions+`}]}`)
		writer := httptest.NewRecorder()
		result := adapterForServer(t, server.URL, nil, nil).AttemptDecision(context.Background(), writer, NewTarget(server.URL, "private-model", NewCredential([]byte("upstream-secret"), nil)), request, contract.AttemptPolicy{SafetyIdentifier: "consumer-id", ResponseSecrets: [][]byte{[]byte(secret)}})
		if result.Success || result.Committed || writer.Body.Len() != 0 {
			t.Fatalf("protected JSON text escaped: %+v", result)
		}
	}
}
