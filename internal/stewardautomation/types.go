// Package stewardautomation composes the CallerKey-only steward controls.
package stewardautomation

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

const DonationsPath = "/api/steward/automation/donations"
const BindingsPath = "/api/steward/automation/model-bindings"
const FailurePolicyPath = "/api/steward/automation/donation-key-failure-policy"
const maxKeys = 100

var errInvalid = errors.New("invalid automation input")
var errDiscovery = errors.New("fresh discovery failed")
var errModelMissing = errors.New("model missing from fresh discovery")

type discoveryFailureError struct{ class string }

func (e *discoveryFailureError) Error() string { return "fresh discovery failed" }
func (e *discoveryFailureError) Unwrap() error { return errDiscovery }

type optionalTime struct {
	set   bool
	value *int64
}

func (v *optionalTime) UnmarshalJSON(data []byte) error {
	v.set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		v.value = nil
		return nil
	}
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	v.value = &value
	return nil
}

type endpointInput struct {
	ConnectorType string `json:"connector_type"`
	BaseURL       string `json:"base_url"`
	Note          string `json:"note"`
	Enabled       *bool  `json:"enabled"`
}

type keyInput struct {
	FailureDisableThreshold *string                   `json:"failure_disable_threshold"`
	Secret                  string                    `json:"secret"`
	Note                    string                    `json:"note"`
	Enabled                 *bool                     `json:"enabled"`
	ForceStoreFalse         bool                      `json:"force_store_false"`
	MaxConcurrency          int64                     `json:"max_concurrency"`
	MaxRPM                  int64                     `json:"max_rpm"`
	AuthorizedExpiresAt     *int64                    `json:"authorized_expires_at"`
	ExpiresAt               optionalTime              `json:"expires_at"`
	PriceLimit              *string                   `json:"price_limit"`
	CallsLimit              *string                   `json:"calls_limit"`
	TokensLimit             *string                   `json:"tokens_limit"`
	InputTokensLimit        *string                   `json:"input_tokens_limit"`
	OutputTokensLimit       *string                   `json:"output_tokens_limit"`
	InputTokenReserve       *string                   `json:"input_token_reserve"`
	OutputTokenReserve      *string                   `json:"output_token_reserve"`
	TokenReserve            int64                     `json:"token_reserve"`
	CharityEnabled          *bool                     `json:"charity_enabled"`
	SafeNote                string                    `json:"safe_note"`
	RecurringLimits         []donationquota.RuleInput `json:"recurring_limits"`
}

type createInput struct {
	DiscordPublicThanks *bool          `json:"discord_public_thanks"`
	Endpoint            *endpointInput `json:"endpoint"`
	Description         string         `json:"description"`
	ReviewNote          string         `json:"review_note"`
	Keys                []keyInput     `json:"keys"`
}

type createdKey struct {
	EndpointKeyID string `json:"endpoint_key_id"`
	DonationKeyID string `json:"donation_key_id"`
}

type createdDonation struct {
	EndpointID string       `json:"endpoint_id"`
	DonationID string       `json:"donation_id"`
	Keys       []createdKey `json:"keys"`
}

type bindingInput struct {
	CharityModelID  string   `json:"charity_model_id"`
	DonationKeyIDs  []string `json:"donation_key_ids"`
	UpstreamModelID string   `json:"upstream_model_id"`
	Manual          bool     `json:"manual"`
}

type bindingResult struct {
	DonationKeyID string `json:"donation_key_id"`
	Status        string `json:"status"`
	Code          string `json:"code,omitempty"`
	Message       string `json:"message,omitempty"`
}

type bindingsResult struct {
	CharityModelID string          `json:"charity_model_id"`
	Results        []bindingResult `json:"results"`
}

func enabled(value *bool) bool { return value == nil || *value }

func numericID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, errInvalid
	}
	return id, nil
}

func validModelID(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 512 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
