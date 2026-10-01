package openai

import (
	"bytes"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
)

// ApplyRolePolicy changes extra roles once on an independent logical request.
func (r *ChatRequest) ApplyRolePolicy(policy rolepolicy.Policy) (*ChatRequest, error) {
	if _, err := policy.Canonical(); err != nil {
		return nil, ErrInvalidRequest
	}
	clone := r.CloneForAttempt()
	if clone == nil {
		return nil, ErrInvalidRequest
	}
	if policy.IsNative() {
		return clone, nil
	}
	fail := func() (*ChatRequest, error) { clone.Clear(); return nil, ErrInvalidRequest }
	found := false
	for index, field := range clone.fields {
		if field.name != "messages" {
			continue
		}
		found = true
		var messages []json.RawMessage
		if json.Unmarshal(field.value, &messages) != nil || messages == nil {
			return fail()
		}
		defer func() {
			for _, message := range messages {
				clear(message)
			}
		}()
		for i, raw := range messages {
			fields, err := decodeJSONObject(raw, 128)
			if err != nil {
				return fail()
			}
			roleIndex := -1
			associated := false
			for j, f := range fields {
				if f.name == "role" {
					roleIndex = j
				}
				if f.name == "tool_calls" || f.name == "tool_call_id" || f.name == "function_call" {
					associated = true
				}
			}
			var role string
			if roleIndex < 0 || !jsonString(fields[roleIndex].value, &role) {
				clearFields(fields)
				return fail()
			}
			action := policy.Action(role)
			if associated || rolepolicy.CoreRole(role) {
				clearFields(fields)
				continue
			}
			switch action {
			case "native":
			case "passthrough":
				clone.extraRolesPassthrough = true
			case "reject":
				clearFields(fields)
				return fail()
			case "system", "user", "assistant":
				clear(fields[roleIndex].value)
				fields[roleIndex].value, _ = json.Marshal(action)
				var buffer bytes.Buffer
				buffer.WriteByte('{')
				for j, f := range fields {
					if j > 0 {
						buffer.WriteByte(',')
					}
					name, _ := json.Marshal(f.name)
					buffer.Write(name)
					buffer.WriteByte(':')
					buffer.Write(f.value)
				}
				buffer.WriteByte('}')
				clear(messages[i])
				messages[i] = append(json.RawMessage(nil), buffer.Bytes()...)
			default:
				clearFields(fields)
				return fail()
			}
			clearFields(fields)
		}
		transformed, err := json.Marshal(messages)
		for _, message := range messages {
			clear(message)
		}
		if err != nil {
			return fail()
		}
		clear(clone.fields[index].value)
		clone.fields[index].value = transformed
	}
	if !found {
		return fail()
	}
	clone.requirements = projectCapabilities(clone.fields, clone.Stream)
	encoded, err := clone.LogicalBody()
	tooLarge := int64(len(encoded)) > clone.RequestBodyLimit()
	clear(encoded)
	if err != nil || tooLarge {
		return fail()
	}
	return clone, nil
}

// SupportsRolePassthrough excludes protocols that translate or reject extra roles.
func (r *ChatRequest) SupportsRolePassthrough(kind string) bool {
	return r != nil && (!r.extraRolesPassthrough || kind == "openai-compatible")
}

// InheritRoleRequirements preserves logical role fidelity across an independent
// body-adaptation copy without applying the policy a second time.
func (r *ChatRequest) InheritRoleRequirements(source *ChatRequest) {
	if r != nil && source != nil {
		r.extraRolesPassthrough = source.extraRolesPassthrough
	}
}
