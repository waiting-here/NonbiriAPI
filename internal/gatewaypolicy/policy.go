// Package gatewaypolicy holds administrator-attested Gateway model capabilities.
package gatewaypolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
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
)

// Model describes one verified adapter/model pair, not a vendor-name heuristic.
type Model struct {
	Adapter         string
	MaxOutputTokens int64
	Storage         string
	efforts         uint8
}

func (m Model) HasReasoning() bool { return m.efforts != 0 }

func (m Model) AllowsEffort(effort string) bool {
	bit := effortBit(effort)
	return bit != 0 && m.efforts&bit != 0
}

type key struct{ baseURL, model string }

// Config is immutable after Parse. An empty config grants no model-specific capabilities.
type Config struct{ models map[key]Model }

func (c Config) Lookup(baseURL, model string) Model {
	return c.models[key{strings.TrimRight(baseURL, "/"), model}]
}

func Parse(raw string) (Config, error) {
	if strings.TrimSpace(raw) == "" {
		return Config{}, nil
	}
	invalid := errors.New("invalid Gateway model capabilities")
	if len(raw) > 64<<10 || strictjson.ValidateObjectWithFieldLimit([]byte(raw), 4096) != nil {
		return Config{}, invalid
	}
	var document struct {
		Models []struct {
			BaseURL         string   `json:"base_url"`
			Model           string   `json:"model"`
			Adapter         string   `json:"adapter"`
			Efforts         []string `json:"efforts"`
			MaxOutputTokens int64    `json:"max_output_tokens"`
			Storage         string   `json:"storage"`
		} `json:"models"`
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || document.Models == nil || len(document.Models) > 128 {
		return Config{}, invalid
	}
	result := Config{models: make(map[key]Model, len(document.Models))}
	for _, entry := range document.Models {
		u, err := url.Parse(entry.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(entry.BaseURL) != entry.BaseURL {
			return Config{}, invalid
		}
		if entry.Model == "" || strings.TrimSpace(entry.Model) != entry.Model || !utf8.ValidString(entry.Model) || utf8.RuneCountInString(entry.Model) > 512 || strings.ContainsFunc(entry.Model, unicode.IsControl) {
			return Config{}, invalid
		}
		var allowed uint8
		switch entry.Adapter {
		case OpenAIChat, OpenAIResponses:
			allowed = effortBit("none") | effortBit("minimal") | effortBit("low") | effortBit("medium") | effortBit("high") | effortBit("xhigh")
		case AnthropicEffort, AnthropicAdaptive, AnthropicAlwaysAdaptive:
			allowed = effortBit("low") | effortBit("medium") | effortBit("high") | effortBit("max")
		default:
			return Config{}, invalid
		}
		if entry.Adapter == AnthropicAlwaysAdaptive {
			allowed |= effortBit("xhigh")
		}
		if entry.Adapter == OpenAIResponses {
			allowed |= effortBit("max")
		}
		model := Model{Adapter: entry.Adapter, MaxOutputTokens: entry.MaxOutputTokens, Storage: entry.Storage}
		if model.MaxOutputTokens < 0 || model.MaxOutputTokens > 2147483647 ||
			(entry.Adapter == AnthropicEffort || entry.Adapter == AnthropicAdaptive || entry.Adapter == AnthropicAlwaysAdaptive) && model.MaxOutputTokens == 0 {
			return Config{}, invalid
		}
		for _, effort := range entry.Efforts {
			bit := effortBit(effort)
			if bit == 0 || allowed&bit == 0 || model.efforts&bit != 0 {
				return Config{}, invalid
			}
			model.efforts |= bit
		}
		switch model.Storage {
		case "":
			model.Storage = RejectStore
		case RejectStore, OmitFalse:
		case OpenAIStore:
			if entry.Adapter != OpenAIChat && entry.Adapter != OpenAIResponses {
				return Config{}, invalid
			}
		default:
			return Config{}, invalid
		}
		identity := key{strings.TrimRight(entry.BaseURL, "/"), entry.Model}
		if _, duplicate := result.models[identity]; duplicate {
			return Config{}, invalid
		}
		result.models[identity] = model
	}
	return result, nil
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
