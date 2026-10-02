// Package gatewaypolicy holds administrator-attested Gateway model capabilities.
package gatewaypolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const (
	OpenAIChat              = "openai_chat"
	OpenAIResponses         = "openai_responses"
	AnthropicEffort         = "anthropic_effort"
	AnthropicAdaptive       = "anthropic_adaptive"
	AnthropicAlwaysAdaptive = "anthropic_always_adaptive"
	RejectStore             = "reject"
	OpenAIStore             = "openai"
	OmitFalse               = "omit_false"
	RejectCache             = "reject"
	AnthropicCache          = "anthropic"
	MaxEntries              = 128
)

// Policy declares only capabilities verified by the administrator.
type Policy struct {
	Adapter         string   `json:"adapter"`
	Efforts         []string `json:"efforts"`
	MaxOutputTokens int64    `json:"max_output_tokens"`
	Storage         string   `json:"storage"`
	Cache           string   `json:"cache"`
}
type Entry struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	Policy
}

// Model is a value snapshot reused by preflight and every physical attempt.
type Model struct {
	Adapter         string
	MaxOutputTokens int64
	Storage         string
	Cache           string
	efforts         uint8
}

func (m Model) HasReasoning() bool { return m.efforts != 0 }
func (m Model) AllowsEffort(effort string) bool {
	bit := effortBit(effort)
	return bit != 0 && m.efforts&bit != 0
}

type Target struct{ BaseURL, Model string }

func (t Target) canonical() Target { t.BaseURL = strings.TrimRight(t.BaseURL, "/"); return t }

// Config is immutable after construction. An empty config grants no model-specific capabilities.
type Config struct {
	models  map[Target]Model
	entries []Entry
}

func (c Config) Lookup(baseURL, model string) Model {
	return c.models[(Target{baseURL, model}).canonical()]
}
func (c Config) Entries() []Entry {
	result := append([]Entry(nil), c.entries...)
	for i := range result {
		result[i].Efforts = append([]string(nil), result[i].Efforts...)
	}
	return result
}

// ValidationError names the rejected field without including caller-supplied values.
type ValidationError struct{ Field string }

func (e *ValidationError) Error() string { return "invalid Gateway model capability: " + e.Field }
func invalid(field string) error         { return &ValidationError{Field: field} }

func Validate(entry Entry) (Entry, Model, error) {
	u, err := url.Parse(entry.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(entry.BaseURL) != entry.BaseURL || len(entry.BaseURL) > 8192 {
		return Entry{}, Model{}, invalid("base_url")
	}
	if entry.Model == "" || strings.TrimSpace(entry.Model) != entry.Model || !utf8.ValidString(entry.Model) || utf8.RuneCountInString(entry.Model) > 512 || strings.ContainsFunc(entry.Model, unicode.IsControl) {
		return Entry{}, Model{}, invalid("model")
	}
	var allowed uint8
	switch entry.Adapter {
	case OpenAIChat, OpenAIResponses:
		allowed = effortBit("none") | effortBit("minimal") | effortBit("low") | effortBit("medium") | effortBit("high") | effortBit("xhigh")
	case AnthropicEffort, AnthropicAdaptive, AnthropicAlwaysAdaptive:
		allowed = effortBit("low") | effortBit("medium") | effortBit("high") | effortBit("max")
	default:
		return Entry{}, Model{}, invalid("adapter")
	}
	if entry.Adapter == AnthropicAlwaysAdaptive {
		allowed |= effortBit("xhigh")
	}
	if entry.Adapter == OpenAIResponses {
		allowed |= effortBit("max")
	}
	m := Model{Adapter: entry.Adapter, MaxOutputTokens: entry.MaxOutputTokens, Storage: entry.Storage, Cache: entry.Cache}
	if m.MaxOutputTokens < 0 || m.MaxOutputTokens > 2147483647 || strings.HasPrefix(entry.Adapter, "anthropic_") && m.MaxOutputTokens == 0 {
		return Entry{}, Model{}, invalid("max_output_tokens")
	}
	for _, effort := range entry.Efforts {
		bit := effortBit(effort)
		if bit == 0 || allowed&bit == 0 || m.efforts&bit != 0 {
			return Entry{}, Model{}, invalid("efforts")
		}
		m.efforts |= bit
	}
	switch m.Storage {
	case "":
		m.Storage = RejectStore
	case RejectStore, OmitFalse:
	case OpenAIStore:
		if entry.Adapter != OpenAIChat && entry.Adapter != OpenAIResponses {
			return Entry{}, Model{}, invalid("storage")
		}
	default:
		return Entry{}, Model{}, invalid("storage")
	}
	switch m.Cache {
	case "":
		m.Cache = RejectCache
	case RejectCache:
	case AnthropicCache:
		if !strings.HasPrefix(entry.Adapter, "anthropic_") {
			return Entry{}, Model{}, invalid("cache")
		}
	default:
		return Entry{}, Model{}, invalid("cache")
	}
	entry.BaseURL = strings.TrimRight(entry.BaseURL, "/")
	entry.Storage, entry.Cache = m.Storage, m.Cache
	entry.Efforts = append([]string{}, entry.Efforts...)
	sort.Slice(entry.Efforts, func(i, j int) bool { return effortBit(entry.Efforts[i]) < effortBit(entry.Efforts[j]) })
	return entry, m, nil
}
func DecodePolicy(raw []byte) (Policy, error) {
	var p Policy
	if strictjson.ValidateObject(raw) != nil {
		return p, invalid("policy")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil {
		return Policy{}, invalid("policy")
	}
	return p, nil
}
func Parse(raw string) (Config, error) {
	if strings.TrimSpace(raw) == "" {
		return Config{}, nil
	}
	if len(raw) > 64<<10 || strictjson.ValidateObjectWithFieldLimit([]byte(raw), 4096) != nil {
		return Config{}, errors.New("invalid Gateway model capabilities")
	}
	var document struct {
		Models []Entry `json:"models"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || document.Models == nil || len(document.Models) > MaxEntries {
		return Config{}, errors.New("invalid Gateway model capabilities")
	}
	c := Config{models: make(map[Target]Model, len(document.Models))}
	for _, entry := range document.Models {
		normalized, model, err := Validate(entry)
		if err != nil {
			return Config{}, err
		}
		key := Target{normalized.BaseURL, normalized.Model}
		if _, duplicate := c.models[key]; duplicate {
			return Config{}, invalid("duplicate model")
		}
		c.models[key] = model
		c.entries = append(c.entries, normalized)
	}
	return c, nil
}
func effortBit(value string) uint8 {
	for index, effort := range [...]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if value == effort {
			return 1 << index
		}
	}
	return 0
}
func KnownEffort(value string) bool { return effortBit(value) != 0 }
