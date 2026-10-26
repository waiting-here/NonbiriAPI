package donation

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
		"donation.review":             agentSchema(agentReviewWire{}, "target_id is donation ID. Approve or reject the complete current pending snapshot using the signature returned by intelligence or a donations read; preserve all prior approvals and current key settings. force_reject revokes the whole donation and requires a reason and its separate grant.", []string{"expected_revision", "decision", "reason"}),
		"donation.handle":             agentSchema(agentRefWire{}, "Mark the donation processed. expected_revision is handling.revision, not donation.revision.", []string{"expected_revision"}, "expected_revision"),
		"donation.key.update":         agentSchema(agentKeyWire{}, "target_id is donation ID. Change only supplied key settings; null clears nullable limits within donor authorization. expected_revision is donation.revision. Changing max_concurrency/max_rpm also requires expected_key_limits_revision. Supply input_token_reserve and output_token_reserve together. enabled pauses/resumes the managed key; no owner discovery permission is implied.", []string{"key_id", "expected_revision"}),
		"donation.key.failure_policy": agentSchema(agentRefWire{}, "Set decimal failure threshold; 0 disables threshold. expected_revision is donation.revision.", []string{"key_id", "expected_revision", "threshold"}, "key_id", "expected_revision", "threshold"),
		"donation.key.reset":          agentSchema(agentRefWire{}, "Reset key-level failure generation. expected_revision is donation.revision.", []string{"key_id", "expected_revision"}, "key_id", "expected_revision"),
		"donation.use.reset":          agentSchema(agentRefWire{}, "Reset one approved native service failure generation. expected_revision is that use revision.", []string{"use_id", "expected_revision"}, "use_id", "expected_revision"),
	}
}
func AgentReadSchemas() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"donations":       agentSchema(agentReadInput{}, "Read all currently authorized donations matching optional donation_id/status/owner_id filters: data plus decimal-string total, safe current uses and pending review signature. Owner filter requires identity visibility. No credential material. More than 5000 rows, 1 MiB or the available model context fails the whole read; narrow these filters and retry.", nil, "donation_id", "status", "owner_id"),
		"donation_models": agentSchema(agentReadInput{}, "Read all authorized saved manual and pinned automatic catalog entries matching optional donation_id, donation_key_id, source (manual/automatic), or query (model/name substring). Returns data plus decimal-string total, donation_id and key_id. No discovery refresh. More than 5000 rows, 1 MiB or available model context fails the whole read; narrow these filters and retry.", nil, "donation_id", "donation_key_id", "source", "query"),
		"review_history":  agentSchema(agentReadInput{}, "Read all retained authorized review decisions matching optional donation_id and decision. Returns data plus decimal-string total and donation_id; identities follow the current grant. Historical decisions do not grant authority. More than 5000 rows, 1 MiB or available model context fails the whole read; narrow these filters and retry.", nil, "donation_id", "decision"),
	}
}
