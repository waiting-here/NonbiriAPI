package riskaudit

import (
	"encoding/json"
	"net/netip"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type FieldQuality struct {
	Truncated bool `json:"truncated,omitempty"`
	Multiple  bool `json:"multiple,omitempty"`
	Invalid   bool `json:"invalid,omitempty"`
}
type Source struct {
	EffectiveIP       string                  `json:"effective_ip"`
	IPQuality         string                  `json:"ip_quality"`
	UserAgent         string                  `json:"user_agent"`
	Origin            string                  `json:"origin"`
	Referer           string                  `json:"referer"`
	HTTPReferer       string                  `json:"http_referer"`
	OpenRouterTitle   string                  `json:"openrouter_title"`
	LegacyTitle       string                  `json:"legacy_title"`
	SDKLang           string                  `json:"sdk_lang"`
	SDKVersion        string                  `json:"sdk_version"`
	SDKRuntime        string                  `json:"sdk_runtime"`
	SDKRuntimeVersion string                  `json:"sdk_runtime_version"`
	Quality           map[string]FieldQuality `json:"quality,omitempty"`
}

func (s Source) field(name string) (string, bool) {
	switch name {
	case "effective_ip":
		return s.EffectiveIP, true
	case "user_agent":
		return s.UserAgent, true
	case "origin":
		return s.Origin, true
	case "referer":
		return s.Referer, true
	case "http_referer":
		return s.HTTPReferer, true
	case "openrouter_title":
		return s.OpenRouterTitle, true
	case "legacy_title":
		return s.LegacyTitle, true
	case "sdk_lang":
		return s.SDKLang, true
	case "sdk_version":
		return s.SDKVersion, true
	case "sdk_runtime":
		return s.SDKRuntime, true
	case "sdk_runtime_version":
		return s.SDKRuntimeVersion, true
	default:
		return "", false
	}
}
func decodeSource(raw string) (Source, error) {
	var out Source
	if len(raw) > 8192 || !utf8.ValidString(raw) || json.Unmarshal([]byte(raw), &out) != nil {
		return Source{}, ErrInvalid
	}
	return out, nil
}
func normalizeIP(raw string) (string, bool) {
	ip, err := netip.ParseAddr(raw)
	if err != nil || ip.Zone() != "" {
		return "", false
	}
	return ip.Unmap().String(), true
}

type Condition struct {
	Field         string `json:"field"`
	Operator      string `json:"operator"`
	Value         string `json:"value"`
	CaseSensitive bool   `json:"case_sensitive"`
}
type Rule struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Status          string      `json:"status"`
	Enabled         bool        `json:"enabled"`
	Revision        int64       `json:"revision"`
	Conditions      []Condition `json:"conditions"`
	EvidenceNote    string      `json:"evidence_note"`
	EvidenceURL     string      `json:"evidence_url"`
	CreatedByRole   string      `json:"created_by_role"`
	CreatedByUserID *int64      `json:"created_by_user_id,string"`
	UpdatedByRole   string      `json:"updated_by_role"`
	UpdatedByUserID *int64      `json:"updated_by_user_id,string"`
	CreatedAt       int64       `json:"created_at"`
	UpdatedAt       int64       `json:"updated_at"`
}

func safeText(s string, min, max int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) < min || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}
func validateRule(r Rule) error {
	if !safeText(r.Name, 1, 120) || (r.Status != "suspected" && r.Status != "confirmed") || !safeText(r.EvidenceNote, 0, 4096) || len(r.EvidenceNote) > 4096 || len(r.EvidenceURL) > 2048 || len(r.Conditions) < 1 || len(r.Conditions) > 8 {
		return ErrInvalid
	}
	if r.EvidenceURL != "" {
		u, e := url.Parse(r.EvidenceURL)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			return ErrInvalid
		}
	}
	for _, c := range r.Conditions {
		if _, ok := (Source{}).field(c.Field); !ok {
			return ErrInvalid
		}
		if c.Operator != "equals" && c.Operator != "contains" && c.Operator != "prefix" {
			return ErrInvalid
		}
		if !safeText(c.Value, 1, 256) {
			return ErrInvalid
		}
	}
	return nil
}

type Match struct {
	RuleID       string   `json:"rule_id"`
	Revision     int64    `json:"revision"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	EvidenceNote string   `json:"evidence_note"`
	EvidenceURL  string   `json:"evidence_url"`
	Quality      string   `json:"quality"`
	Fields       []string `json:"fields"`
}

// MatchRules uses a finite AND within each rule and OR between rules. A
// truncated or absent field cannot establish a match, including equality.
func MatchRules(source Source, rules []Rule) []Match {
	return matchRules(source, rules, true)
}

// Frozen task rules were validated when the task was read. Avoid repeatedly
// validating large evidence notes for every candidate in the same batch.
func matchRules(source Source, rules []Rule, validate bool) []Match {
	result := make([]Match, 0)
	lower := make(map[string]string)
	for i, r := range rules {
		if i >= MaxRules {
			break
		}
		if !r.Enabled || (validate && validateRule(r) != nil) {
			continue
		}
		matched := true
		quality := "self_reported"
		fields := make([]string, 0, len(r.Conditions))
		for _, c := range r.Conditions {
			value, ok := source.field(c.Field)
			q := source.Quality[c.Field]
			if !ok || value == "" || q.Truncated || q.Invalid {
				matched = false
				break
			}
			if c.Field == "effective_ip" {
				value, ok = normalizeIP(value)
				if !ok || (source.IPQuality != "direct_peer" && source.IPQuality != "trusted_forwarded") {
					matched = false
					break
				}
			}
			if q.Multiple {
				quality = "multiple_values"
			}
			needle := c.Value
			if !c.CaseSensitive {
				folded, exists := lower[c.Field]
				if !exists {
					folded = strings.ToLower(value)
					lower[c.Field] = folded
				}
				value = folded
				needle = strings.ToLower(needle)
			}
			switch c.Operator {
			case "equals":
				matched = value == needle
			case "contains":
				matched = strings.Contains(value, needle)
			case "prefix":
				matched = strings.HasPrefix(value, needle)
			}
			if !matched {
				break
			}
			fields = append(fields, c.Field)
		}
		if matched {
			result = append(result, Match{RuleID: r.ID, Revision: r.Revision, Name: r.Name, Status: r.Status, EvidenceNote: r.EvidenceNote, EvidenceURL: r.EvidenceURL, Quality: quality, Fields: fields})
		}
	}
	return result
}
