package adminapi

// Site configuration registry: the authoritative known-key set for runtime
// site_config values, plus the typed parse/validate/read helpers. Only the
// keys below (and the alert_prefs_* namespace) exist; any other key is
// strictly not_found on read and write. Text is bounded and
// control-character-free; integers are canonical decimal and range-checked;
// locale values are exactly "zh" or "en". Values never carry secrets or
// upstream material by construction.

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

// Known site_config keys (the authoritative set enforced by the handler).
const (
	KeySiteName                  = "site_name"
	KeyDefaultLocale             = "default_locale"
	KeyDefaultEndpointLimit      = "default_endpoint_limit"
	KeyDefaultEndpointKeyLimit   = "default_endpoint_key_limit"
	KeyDefaultModelLimit         = "default_model_limit"
	KeyDefaultBindingLimit       = "default_binding_limit"
	KeyDefaultRPMPerUser         = "default_rpm_per_user"
	KeyGlobalRPM                 = "global_rpm"
	KeyDefaultPerEndpointConc    = "default_per_endpoint_concurrency"
	KeyEgressGlobalConc          = "egress_global_concurrency"
	KeyDiscordGuildID            = "discord_guild_id"
	KeyDiscordRoleID             = "discord_role_id"
	KeyOAuthStartRateLimit       = "oauth_start_rate_limit"
	KeyOAuthStartRateWindowSecs  = "oauth_start_rate_window_seconds"
	KeyOAuthStartRatePenaltySecs = "oauth_start_rate_penalty_seconds"
	alertPrefsPrefix             = "alert_prefs_"
)

// Value bounds. RPM caps share the limiter's bounded event-store ceiling
// (ratelimit's default maxEvents), so every accepted value can be applied to
// the runtime controller without failing.
const (
	maxSiteNameBytes      = 256
	maxDiscordGateBytes   = 128
	maxAlertPrefsBytes    = 512
	maxSiteConfigKeyLen   = 128 // mirrors the repository site_config key bound
	maxResourceLimitValue = 10000
	maxRPMValue           = 4096
	maxConcurrencyValue   = 100000
	// OAuth start admission bounds. The per-IP limit stays below the ratelimit
	// IPThrottle default MaxHitsPerKey (4096) so a live reconfigure can never
	// exceed the bounded per-key hit store; the window/penalty ceilings are one
	// hour, well beyond the ten-minute OAuth state TTL but still finite.
	maxOAuthStartRateLimit       = 1000
	maxOAuthStartRateWindowSecs  = 3600
	maxOAuthStartRatePenaltySecs = 3600
)

type valueKind int

const (
	kindText valueKind = iota
	kindLocale
	kindInt
)

type keySpec struct {
	kind       valueKind
	min, max   int
	def        int  // int keys: effective default when unset
	allowEmpty bool // text keys: "" is a valid value (blank pauses the gate)
}

// knownSiteConfig maps every exact known key to its typed spec.
var knownSiteConfig = map[string]keySpec{
	KeySiteName:                  {kind: kindText, allowEmpty: false, max: maxSiteNameBytes},
	KeyDefaultLocale:             {kind: kindLocale},
	KeyDefaultEndpointLimit:      {kind: kindInt, min: 0, max: maxResourceLimitValue, def: db.DefaultEndpointLimit},
	KeyDefaultEndpointKeyLimit:   {kind: kindInt, min: 1, max: maxResourceLimitValue, def: db.DefaultEndpointKeyLimit},
	KeyDefaultModelLimit:         {kind: kindInt, min: 1, max: maxResourceLimitValue, def: db.DefaultModelLimit},
	KeyDefaultBindingLimit:       {kind: kindInt, min: 1, max: maxResourceLimitValue, def: db.DefaultBindingLimit},
	KeyDefaultRPMPerUser:         {kind: kindInt, min: 1, max: maxRPMValue, def: ratelimit.DefaultRPMPerUserLimit},
	KeyGlobalRPM:                 {kind: kindInt, min: 1, max: maxRPMValue, def: ratelimit.DefaultRPMGlobalLimit},
	KeyDefaultPerEndpointConc:    {kind: kindInt, min: 1, max: maxConcurrencyValue, def: egress.DefaultPerEndpointConcurrency},
	KeyEgressGlobalConc:          {kind: kindInt, min: 1, max: maxConcurrencyValue, def: egress.DefaultGlobalConcurrency},
	KeyDiscordGuildID:            {kind: kindText, allowEmpty: true, max: maxDiscordGateBytes},
	KeyDiscordRoleID:             {kind: kindText, allowEmpty: true, max: maxDiscordGateBytes},
	KeyOAuthStartRateLimit:       {kind: kindInt, min: 0, max: maxOAuthStartRateLimit, def: ratelimit.DefaultOAuthStartRateLimit},
	KeyOAuthStartRateWindowSecs:  {kind: kindInt, min: 1, max: maxOAuthStartRateWindowSecs, def: ratelimit.DefaultOAuthStartRateWindowSeconds},
	KeyOAuthStartRatePenaltySecs: {kind: kindInt, min: 0, max: maxOAuthStartRatePenaltySecs, def: ratelimit.DefaultOAuthStartRatePenaltySeconds},
}

// knownSiteConfigKey reports whether key is in the authoritative set
// (including the alert_prefs_* namespace). Keys longer than the repository
// bound can never be stored and are not known.
func knownSiteConfigKey(key string) bool {
	if len(key) > maxSiteConfigKeyLen {
		return false
	}
	if _, ok := knownSiteConfig[key]; ok {
		return true
	}
	return strings.HasPrefix(key, alertPrefsPrefix)
}

// textMaxFor returns the byte bound for a text key.
func textMaxFor(key string) int {
	if spec, ok := knownSiteConfig[key]; ok && spec.kind == kindText {
		if spec.max > 0 {
			return spec.max
		}
	}
	if strings.HasPrefix(key, alertPrefsPrefix) {
		return maxAlertPrefsBytes
	}
	return maxSiteNameBytes
}

func validConfigText(value string, maxBytes int) bool {
	if len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x7f {
			return false
		}
	}
	return true
}

// parseCanonicalInt accepts exactly the canonical decimal form of an integer
// (no sign, no leading zeros, no surrounding whitespace).
func parseCanonicalInt(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("configuration value is empty")
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if strconv.FormatInt(int64(n), 10) != raw {
		return 0, errors.New("configuration value is not a canonical integer")
	}
	return n, nil
}

// typedSiteConfigValue converts a stored site_config string into the wire
// value for a known key. Int keys fall back to the documented default when
// unset or when a stored value is not a canonical integer in range, so a
// manually corrupted row never breaks the read path; text keys fall back to
// "" when out of bounds. alert_prefs_* keys are bounded text.
func typedSiteConfigValue(key, stored string) any {
	if spec, ok := knownSiteConfig[key]; ok {
		switch spec.kind {
		case kindInt:
			if n, err := parseCanonicalInt(stored); err == nil && n >= spec.min && n <= spec.max {
				return n
			}
			return spec.def
		case kindLocale:
			if stored == "zh" || stored == "en" {
				return stored
			}
			return ""
		default:
			if validConfigText(stored, textMaxFor(key)) {
				return stored
			}
			return ""
		}
	}
	if strings.HasPrefix(key, alertPrefsPrefix) {
		if validConfigText(stored, maxAlertPrefsBytes) {
			return stored
		}
		return ""
	}
	return nil
}

// validateSiteConfigValue checks a JSON request value against the key's typed
// spec and returns the canonical stored string. A non-JSON type, an
// out-of-range or non-integral number, or an out-of-bounds text is
// invalid_request. JSON null is always rejected: clearing an int cap is
// expressed through the per-user/user-level NULL semantics, and clearing a
// text value is expressed as "".
func validateSiteConfigValue(key string, raw json.RawMessage) (string, httperr.Error) {
	invalid := httperr.New(httperr.CodeInvalidRequest, "invalid configuration value")
	if spec, ok := knownSiteConfig[key]; ok {
		switch spec.kind {
		case kindInt:
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			var decoded any
			if err := dec.Decode(&decoded); err != nil {
				return "", invalid
			}
			num, ok := decoded.(json.Number)
			if !ok {
				return "", invalid
			}
			n, err := num.Int64()
			if err != nil || strconv.FormatInt(n, 10) != num.String() {
				return "", invalid
			}
			if n < int64(spec.min) || n > int64(spec.max) {
				return "", invalid
			}
			return num.String(), httperr.Error{}
		case kindLocale:
			var value string
			if err := json.Unmarshal(raw, &value); err != nil || (value != "zh" && value != "en") {
				return "", invalid
			}
			return value, httperr.Error{}
		default:
			var value string
			if err := json.Unmarshal(raw, &value); err != nil || !validConfigText(value, textMaxFor(key)) {
				return "", invalid
			}
			if !spec.allowEmpty && value == "" {
				return "", invalid
			}
			return value, httperr.Error{}
		}
	}
	if strings.HasPrefix(key, alertPrefsPrefix) {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || !validConfigText(value, maxAlertPrefsBytes) {
			return "", invalid
		}
		return value, httperr.Error{}
	}
	return "", httperr.New(httperr.CodeNotFound, "configuration key not found")
}
