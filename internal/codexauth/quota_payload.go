package codexauth

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

type QuotaData struct {
	PlanType              string        `json:"plan_type,omitempty"`
	WorkspaceName         string        `json:"workspace_name,omitempty"`
	Buckets               []QuotaBucket `json:"buckets"`
	ResetCreditsAvailable *int64        `json:"reset_credits_available,omitempty"`
	Partial               bool          `json:"partial"`
}
type QuotaBucket struct {
	ID           string        `json:"id"`
	Name         string        `json:"name,omitempty"`
	PlanType     string        `json:"plan_type,omitempty"`
	Windows      []QuotaWindow `json:"windows"`
	Credits      *QuotaCredits `json:"credits,omitempty"`
	ReachedType  string        `json:"reached_type,omitempty"`
	Allowed      *bool         `json:"allowed,omitempty"`
	LimitReached *bool         `json:"limit_reached,omitempty"`
}
type QuotaWindow struct {
	Name          string   `json:"name"`
	UsedPercent   *float64 `json:"used_percent"`
	WindowSeconds *int64   `json:"window_seconds"`
	ResetsAt      *int64   `json:"resets_at"`
}
type QuotaCredits struct {
	HasCredits *bool  `json:"has_credits"`
	Unlimited  *bool  `json:"unlimited"`
	Balance    string `json:"balance,omitempty"`
	Unit       string `json:"unit,omitempty"`
}

// Optional malformed members and independent buckets never obscure usable
// values. Missing fields are unknown; window lengths and units are not inferred.
func decodeQuota(raw []byte) (QuotaData, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return QuotaData{}, err
	}
	if root == nil {
		return QuotaData{}, errors.New("expected quota object")
	}
	out := QuotaData{PlanType: scalar(root["plan_type"]), WorkspaceName: scalar(root["workspace_name"]), Buckets: []QuotaBucket{}}
	recognized := false
	for _, key := range []string{"plan_type", "rate_limit", "additional_rate_limits", "credits", "rate_limits", "rate_limits_by_limit_id", "rateLimits", "rateLimitsByLimitId", "rate_limit_reset_credits"} {
		if _, ok := root[key]; ok {
			recognized = true
		}
	}
	if !recognized {
		return out, errors.New("expected quota fields")
	}
	multi := object(root["rate_limits_by_limit_id"])
	if multi == nil {
		multi = object(root["rateLimitsByLimitId"])
	}
	if len(multi) > 0 {
		keys := sortedKeys(multi)
		for _, id := range keys {
			if len(out.Buckets) >= 128 {
				out.Partial = true
				break
			}
			if row := object(multi[id]); row != nil {
				out.Buckets = append(out.Buckets, parseBucket(id, row, out.PlanType, &out.Partial))
			} else {
				out.Partial = true
			}
		}
	} else {
		single := object(root["rateLimits"])
		if single == nil {
			single = object(root["rate_limits"])
		}
		if single != nil {
			out.Buckets = append(out.Buckets, parseBucket("codex", single, out.PlanType, &out.Partial))
		} else {
			out.Buckets = append(out.Buckets, parseBucket("codex", root, out.PlanType, &out.Partial))
		}
	}
	var extra []json.RawMessage
	if value, ok := root["additional_rate_limits"]; ok && string(value) != "null" {
		if json.Unmarshal(value, &extra) != nil {
			out.Partial = true
		}
		for _, value := range extra {
			if len(out.Buckets) >= 128 {
				out.Partial = true
				break
			}
			row := object(value)
			id := scalar(row["metered_feature"])
			if row == nil || id == "" {
				out.Partial = true
				continue
			}
			bucket := parseBucket(id, row, out.PlanType, &out.Partial)
			found := false
			for i := range out.Buckets {
				if out.Buckets[i].ID == id {
					out.Buckets[i] = bucket
					found = true
					break
				}
			}
			if !found {
				out.Buckets = append(out.Buckets, bucket)
			}
		}
	}
	if reset := object(root["rate_limit_reset_credits"]); reset != nil {
		out.ResetCreditsAvailable = numberInt(reset["available_count"])
	}
	return out, nil
}
func parseBucket(id string, row map[string]json.RawMessage, plan string, partial *bool) QuotaBucket {
	if v := scalar(row["limit_id"]); v != "" {
		id = v
	}
	if v := scalar(row["limitId"]); v != "" {
		id = v
	}
	b := QuotaBucket{ID: diagnostic.SafeScalar(id), Name: scalar(row["limit_name"]), PlanType: scalar(row["plan_type"]), Windows: []QuotaWindow{}}
	if b.Name == "" {
		b.Name = scalar(row["limitName"])
	}
	if b.PlanType == "" {
		b.PlanType = scalar(row["planType"])
	}
	if b.PlanType == "" {
		b.PlanType = plan
	}
	limit := object(row["rate_limit"])
	if limit == nil {
		limit = row
	}
	b.Allowed = boolValue(limit["allowed"])
	b.LimitReached = boolValue(limit["limit_reached"])
	for _, name := range sortedKeys(limit) {
		if name != "primary" && name != "secondary" && !strings.HasSuffix(name, "_window") {
			continue
		}
		value := limit[name]
		if string(value) == "null" {
			continue
		}
		window := object(value)
		if window == nil {
			*partial = true
			continue
		}
		w := QuotaWindow{Name: diagnostic.SafeScalar(name), UsedPercent: numberFloat(window["used_percent"]), WindowSeconds: numberInt(window["limit_window_seconds"]), ResetsAt: numberInt(window["reset_at"])}
		if w.UsedPercent == nil {
			w.UsedPercent = numberFloat(window["usedPercent"])
		}
		if w.WindowSeconds == nil {
			if minutes := numberInt(window["windowDurationMins"]); minutes != nil && *minutes <= 5256000 {
				seconds := *minutes * 60
				w.WindowSeconds = &seconds
			}
		}
		if w.ResetsAt == nil {
			w.ResetsAt = numberInt(window["resetsAt"])
		}
		if w.WindowSeconds != nil && *w.WindowSeconds <= 0 {
			w.WindowSeconds = nil
		}
		b.Windows = append(b.Windows, w)
		if len(b.Windows) >= 16 {
			*partial = true
			break
		}
	}
	if credits := object(row["credits"]); credits != nil {
		b.Credits = &QuotaCredits{HasCredits: boolValue(credits["has_credits"]), Unlimited: boolValue(credits["unlimited"]), Balance: scalar(credits["balance"]), Unit: scalar(credits["unit"])}
		if b.Credits.HasCredits == nil {
			b.Credits.HasCredits = boolValue(credits["hasCredits"])
		}
	}
	b.ReachedType = scalar(row["rate_limit_reached_type"])
	if reached := object(row["rate_limit_reached_type"]); reached != nil {
		b.ReachedType = scalar(reached["type"])
		if b.ReachedType == "" {
			b.ReachedType = scalar(reached["kind"])
		}
	}
	if b.ReachedType == "" {
		b.ReachedType = scalar(row["rateLimitReachedType"])
	}
	return b
}
func sortedKeys(row map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(row))
	for key := range row {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func object(raw json.RawMessage) map[string]json.RawMessage {
	var out map[string]json.RawMessage
	_ = json.Unmarshal(raw, &out)
	return out
}
func scalar(raw json.RawMessage) string {
	var out string
	_ = json.Unmarshal(raw, &out)
	return diagnostic.SafeScalar(out)
}
func numberInt(raw json.RawMessage) *int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out int64
	if json.Unmarshal(raw, &out) != nil || out < 0 || out > 253402300799 {
		return nil
	}
	return &out
}
func numberFloat(raw json.RawMessage) *float64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out float64
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return &out
}
func boolValue(raw json.RawMessage) *bool {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out bool
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return &out
}
