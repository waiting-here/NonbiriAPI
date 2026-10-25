package charityrouting

import (
	"encoding/json"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"reflect"
	"strings"
)

// Schema follows the exact request wire types, including nullable fields, so
// model tools cannot silently diverge from the existing API parsers.
func agentTypeSchema(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		return map[string]any{"anyOf": []any{agentTypeSchema(t.Elem()), map[string]any{"type": "null"}}}
	}
	if t.Kind() == reflect.Struct && (strings.HasPrefix(t.Name(), "RequestField[") || strings.HasPrefix(t.Name(), "NullableField[")) {
		f, _ := t.FieldByName("Value")
		return agentTypeSchema(f.Type)
	}
	out := map[string]any{}
	switch t.Kind() {
	case reflect.String:
		out["type"] = "string"
		if t == reflect.TypeOf(contract.NativeService("")) {
			out["enum"] = contract.AllNativeServices()
		}
	case reflect.Bool:
		out["type"] = "boolean"
	case reflect.Int, reflect.Int64, reflect.Int32:
		out["type"] = "integer"
	case reflect.Float64:
		out["type"] = "number"
	case reflect.Slice, reflect.Array:
		out["type"] = "array"
		out["items"] = agentTypeSchema(t.Elem())
	case reflect.Map:
		out["type"] = "object"
		out["additionalProperties"] = agentTypeSchema(t.Elem())
	case reflect.Struct:
		props := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if f.Anonymous {
				child := agentTypeSchema(f.Type)
				if fields, ok := child["properties"].(map[string]any); ok {
					for k, v := range fields {
						props[k] = v
					}
				}
				continue
			}
			if f.PkgPath != "" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			v := agentTypeSchema(f.Type)
			v["description"] = strings.ReplaceAll(name, "_", " ")
			if name != "upstream_model_id" && (strings.HasSuffix(name, "_id") || strings.Contains(name, "revision")) {
				if v["type"] == "string" {
					v["pattern"] = "^[0-9]+$"
				}
				v["description"] = "Decimal string identifier or exact current revision; never guess or increment a stale version."
			}
			props[name] = v
		}

		switch t.Name() {
		case "BindingTargetInput":
			out["required"] = []string{"donation_key_id", "upstream_model_id", "native_services"}
		case "tokenPricesWire":
			out["required"] = []string{"uncached_input", "cache_write_input", "cache_read_input", "output"}
		case "discountWire":
			out["required"] = []string{"enabled", "percent", "start_at", "end_at"}
		case "pricingWire":
			out["required"] = []string{"mode"}
			out["description"] = "per_request requires user_price and donor_reward; per_token requires user_prices and donor_rewards. Decimal credits have at most three fractional digits; token rates preserve API precision."
		}
		out["type"] = "object"
		out["properties"] = props
		out["additionalProperties"] = false
	}
	return out
}
func agentSchema(value any, description string, required []string, keep ...string) json.RawMessage {
	out := agentTypeSchema(reflect.TypeOf(value))
	out["description"] = description
	if required == nil {
		required = []string{}
	}
	out["required"] = required
	if len(keep) > 0 {
		allow := map[string]bool{}
		for _, k := range keep {
			allow[k] = true
		}
		props := out["properties"].(map[string]any)
		for key := range props {
			if !allow[key] {
				delete(props, key)
			}
		}
	}
	raw, _ := json.Marshal(out)
	return raw
}

func AgentOperationSchemas() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"charity.model.create":   agentSchema(modelCreateWire{}, "target_id must be empty. Create a model with the same fields and constraints as the management API. affinity_ttl_seconds is admin-only. Transport/model types and role policy retain API defaults when omitted.", []string{"provider", "model", "enabled", "pricing", "discount", "flatten_tool_calls"}),
		"charity.model.update":   agentSchema(modelPatchWire{}, "target_id is model ID. Change supplied fields atomically at expected_revision. Pricing, settings and role policy each require their own grant. affinity_ttl_seconds is admin-only. Null token_reserve_credits clears it; omit other unchanged fields.", []string{"expected_revision"}),
		"charity.model.delete":   agentSchema(ModelDelete{}, "target_id is model ID. Delete at exact expected_revision; confirmation is a nonempty acknowledgement.", []string{"expected_revision", "confirmation"}),
		"charity.binding.add":    agentSchema(agentBindingInput{}, "target_id is existing charity model ID. Add one binding group with one or more targets. Each target requires donation_key_id, upstream_model_id and a nonempty native_services array. Different upstream models are allowed. At least one retained service must serve the model; no discovery/network occurs. expected_binding_revision is model binding_revision, initially 0.", []string{"expected_binding_revision", "targets"}, "expected_binding_revision", "targets"),
		"charity.binding.update": agentSchema(agentBindingInput{}, "Replace all targets of one binding atomically. target_id is model ID. native_services is an explicit nonempty subset of approved services for each target; retain supported extra services. expected_binding_revision pins the entire model binding collection.", []string{"expected_binding_revision", "binding_id", "targets"}),
		"charity.binding.delete": agentSchema(agentBindingInput{}, "Delete one binding and compact order while preserving other bindings. target_id is model ID; expected_binding_revision pins its binding collection.", []string{"expected_binding_revision", "binding_id"}, "expected_binding_revision", "binding_id"),
	}
}
func AgentReadSchemas() map[string]json.RawMessage {
	return map[string]json.RawMessage{"charity_models": agentSchema(agentReadInput{}, "Read authorized charity models; query is a literal substring. model_id includes safe bindings and binding_revision. page defaults 1, page_size 50, maximum 100.", nil)}
}
