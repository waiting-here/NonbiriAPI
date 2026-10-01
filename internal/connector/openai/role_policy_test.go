package openai

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
)

func TestRolePolicyMappingPreservesOrderContentAndToolAssociations(t *testing.T) {
	raw := `{"model":"p/m","messages":[{"role":"developer","content":"first"},{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/a"}}]},{"role":"critic","content":"third"},{"role":"assistant","content":"<mx_tool history"},{"role":"critic","content":null,"tool_calls":[{"id":"call_a"}]},{"role":"tool","tool_call_id":"call_a","content":"result"}]}`
	request, err := DecodeChatRequest(strings.NewReader(raw), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	before, _ := request.LogicalBody()
	defer clear(before)
	policy := rolepolicy.Default()
	policy.Rules["developer"] = "system"
	policy.Rules["critic"] = "assistant"
	policy.DefaultAction = "reject"
	mapped, err := request.ApplyRolePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer mapped.Clear()
	messageRaw, _ := mapped.RawField("messages")
	defer clear(messageRaw)
	var messages []map[string]json.RawMessage
	if err = json.Unmarshal(messageRaw, &messages); err != nil {
		t.Fatal(err)
	}
	want := []string{"system", "user", "assistant", "assistant", "critic", "tool"}
	for i, message := range messages {
		var role string
		json.Unmarshal(message["role"], &role)
		if role != want[i] {
			t.Fatalf("role at %d=%s", i, role)
		}
	}
	if !bytes.Equal(messages[1]["content"], []byte(`[{"type":"image_url","image_url":{"url":"https://example.test/a"}}]`)) {
		t.Fatal("image changed")
	}
	if !mapped.Requirements().Capabilities().Has(contract.CapabilitySystem) || mapped.Requirements().Capabilities().Has(contract.CapabilityDeveloper) {
		t.Fatal("capabilities not recomputed")
	}
	after, _ := request.LogicalBody()
	defer clear(after)
	if !bytes.Equal(before, after) {
		t.Fatal("caller request mutated")
	}
	policy.Rules["developer"] = "user"
	if string(messages[0]["role"]) != `"system"` {
		t.Fatal("mapped request changed with policy")
	}
}

func TestRolePolicyNativeShapePassthroughAndReject(t *testing.T) {
	for _, raw := range []string{`{"model":"p/m","messages":[{"role":null,"content":"x"}]}`, `{"model":"p/m","messages":"legacy"}`} {
		r, err := DecodeChatRequest(strings.NewReader(raw), MaxRequestBodyBytes)
		if err != nil {
			t.Fatal(err)
		}
		before, _ := r.LogicalBody()
		clone, err := r.ApplyRolePolicy(rolepolicy.Default())
		if err != nil {
			t.Fatal("native tightened old shape")
		}
		after, _ := clone.LogicalBody()
		if !bytes.Equal(before, after) {
			t.Fatal("native changed body")
		}
		clear(before)
		clear(after)
		clone.Clear()
		explicit := rolepolicy.Default()
		explicit.DefaultAction = "user"
		if mapped, err := r.ApplyRolePolicy(explicit); err == nil {
			mapped.Clear()
			t.Fatal("invalid role fabricated")
		}
		r.Clear()
	}
	r, err := DecodeChatRequest(strings.NewReader(`{"model":"p/m","messages":[{"role":"developer","content":"x"}]}`), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Clear()
	p := rolepolicy.Default()
	p.Rules["developer"] = "passthrough"
	clone, err := r.ApplyRolePolicy(p)
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Clear()
	if !clone.SupportsRolePassthrough("openai-compatible") || clone.SupportsRolePassthrough("anthropic-compatible") || clone.SupportsRolePassthrough("ai-sdk-gateway-v3") {
		t.Fatal("passthrough falsely translated")
	}
	p.Rules["developer"] = "reject"
	if mapped, err := r.ApplyRolePolicy(p); err == nil {
		mapped.Clear()
		t.Fatal("rejected role accepted")
	}
}
