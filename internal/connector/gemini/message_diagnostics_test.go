package gemini

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
)

func TestMessageDiagnosticsKeepPositionsWithoutRequestValues(t *testing.T) {
	const tool = `{"role":"assistant","tool_calls":[{"type":"function","id":"PRIVATE_ID","function":{"name":"read","arguments":"{}"}}]}`
	for _, tc := range []struct{ messages, field, reason string }{
		{`[null]`, "messages[0]", "expected a message object"},
		{`[{"role":"user","content":"ok","function_call":{ }}]`, "messages[0].function_call", "message attribute cannot be preserved by Gemini"},
		{`[{"role":"user","content":"ok","annotations":[ null ]}]`, "messages[0].annotations", "message attribute cannot be preserved by Gemini"},
		{`[{"role":"user","content":"ok","extra_content":{ "PRIVATE_KEY":null }}]`, "messages[0].extra_content", "message attribute is not valid for this role"},
		{`[{"role":"user","content":"ok"},{"role":"PRIVATE_ROLE"}]`, "messages[1].role", "expected a supported message role"},
		{`[{"role":"user","content":"first"},{"role":"developer","content":"PRIVATE_TEXT"}]`, "messages[1].role", "Gemini accepts system and developer instructions only before conversation turns"},
		{`[{"role":"user","content":"x","PRIVATE_KEY":null}]`, "messages[0]", "message contains an unsupported attribute"},
		{`[{"role":"assistant","content":"x","refusal":"PRIVATE_TEXT"}]`, "messages[0].refusal", "message attribute cannot be preserved by Gemini"},
		{`[{"role":"user","content":"x","reasoning_content":"PRIVATE_TEXT"}]`, "messages[0].reasoning_content", "message attribute is not valid for this role"},
		{`[{"role":"user","content":[{"type":"text","text":"a"},{"type":"PRIVATE_TYPE"}]}]`, "messages[0].content[1].type", "unsupported Gemini content block type"},
		{`[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://PRIVATE_URL"}}]}]`, "messages[0].content[0].image_url.url", "Gemini requires an inline base64 PNG, JPEG, GIF or WebP image"},
		{`[{"role":"assistant","tool_calls":[{"type":"function","id":"PRIVATE_ID","function":{"name":"read","arguments":"PRIVATE_ARGUMENT"}}]}]`, "messages[0].tool_calls[0].function.arguments", "function arguments must be a JSON object encoded as a string"},
		{`[{"role":"assistant","tool_calls":[{"type":"function","id":"PRIVATE_ID","extra_content":{"google":{"parts":[{"text":"PRIVATE_TEXT"}]}},"function":{"name":"read","arguments":"{}"}}]}]`, "messages[0].tool_calls[0].extra_content", "invalid Google continuation metadata"},
		{"[" + tool + "]", "messages[0].tool_calls", "assistant tool calls are missing one or more results"},
		{"[" + tool + `,{"role":"user","content":"PRIVATE_TEXT"}]`, "messages[1].role", "tool results must follow their assistant call before the next turn"},
		{"[" + tool + `,{"role":"tool","tool_call_id":"OTHER_PRIVATE","content":"ok"}]`, "messages[1].tool_call_id", "tool result must match an unanswered tool call"},
		{"[" + tool + `,{"role":"tool","tool_call_id":"PRIVATE_ID","content":[]}]`, "messages[1].content", "tool result content must be a string"},
		{`[{"role":"system","content":"PRIVATE_TEXT"}]`, "messages", "at least one conversation turn must follow the instructions"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			_, _, err := messages([]byte(tc.messages))
			var rejection *contract.RequestRejection
			if !errors.As(err, &rejection) || rejection.Field != tc.field || rejection.Reason != tc.reason {
				t.Fatalf("unexpected rejection: %v", err)
			}
			detail := requestattempt.NewDetail(rejection.Field, rejection.Reason)
			if detail == nil || detail.Field != tc.field || detail.Suggestion == "" || strings.Contains(requestattempt.EncodeDetail(detail), "PRIVATE") {
				t.Fatal(detail)
			}
		})
	}
}

func TestEmptyOptionalMessagePlaceholdersPreserveInstructionsAndConversation(t *testing.T) {
	raw := `[
		{"role":"system","content":"system","name":"","tool_calls":[
		],"tool_call_id":null},
		{"role":"developer","content":"developer","refusal":null,"audio":null},
		{"role":"user","content":"user","reasoning_content":"","extra_content":{
		},"function_call":null},
		{"role":"assistant","content":"reply","annotations":[   ],"tool_call_id":""}
	]`
	contents, system, err := messages([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	instructions, _ := json.Marshal(system)
	turns, _ := json.Marshal(contents)
	if string(instructions) != `[{"text":"system"},{"text":"developer"}]` || string(turns) != `[{"parts":[{"text":"user"}],"role":"user"},{"parts":[{"text":"reply"}],"role":"model"}]` {
		t.Fatal(string(instructions), string(turns))
	}
}

func TestWhitespaceOnlyOptionalFieldsPreserveMappedRoleAndPairedResult(t *testing.T) {
	const turns = `[
		{"role":"developer","content":"directive","tool_calls":[
		]},
		{"role":"assistant","annotations":[	],"tool_calls":[{"type":"function","id":"c","function":{"name":"read","arguments":"{}"},"extra_content":{
		}}]},
		{"role":"tool","tool_call_id":"c","content":"done","extra_content":{
		}}
	]`
	logical := request(t, `{"model":"m","messages":`+turns+"}")
	policy := rolepolicy.Default()
	policy.Rules["developer"] = "user"
	mapped, err := logical.ApplyRolePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer mapped.Clear()
	body, err := Compile(mapped, "m", contract.AttemptPolicy{SafetyIdentifier: "PUBLIC"})
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, body, `{"labels":{"safety_identifier":"public"},"contents":[
		{"role":"user","parts":[{"text":"directive"}]},
		{"role":"model","parts":[{"functionCall":{"id":"c","name":"read","args":{}}}]},
		{"role":"user","parts":[{"functionResponse":{"id":"c","name":"read","response":{"result":"done"}}}]}
	]}`)
}
