package stewardautomation

import (
	"encoding/json"
	"net/http"
	"strings"
)

const PersonalPrefix = "/api/automation/"
const StewardPrefix = "/api/steward/automation/"

type automationRoute struct {
	kind     string
	ids      []string
	personal bool
	methods  []string
}

func matchAutomationRoute(path string) automationRoute {
	if path == DonationsPath {
		return automationRoute{kind: "donations", methods: []string{http.MethodGet, http.MethodPost}}
	}
	if path == BindingsPath {
		return automationRoute{kind: "charity_write", methods: []string{http.MethodPost}}
	}
	if path == FailurePolicyPath {
		return automationRoute{kind: "failure_policy", methods: []string{http.MethodGet, http.MethodPatch}}
	}
	personal := strings.HasPrefix(path, PersonalPrefix)
	prefix := StewardPrefix
	if personal {
		prefix = PersonalPrefix
	}
	if !strings.HasPrefix(path, prefix) {
		return automationRoute{}
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	for _, p := range parts {
		if p == "" {
			return automationRoute{}
		}
	}
	route := automationRoute{personal: personal, methods: []string{http.MethodGet}}
	if personal {
		switch {
		case len(parts) == 1 && (parts[0] == "endpoints" || parts[0] == "models"):
			route.kind = parts[0]
		case len(parts) == 2 && (parts[0] == "endpoints" || parts[0] == "models"):
			route.kind = parts[0] + "_one"
			route.ids = parts[1:]
		case len(parts) == 3 && parts[0] == "endpoints" && parts[2] == "keys":
			route.kind = "keys"
			route.ids = []string{parts[1]}
		case len(parts) == 4 && parts[0] == "endpoints" && parts[2] == "keys" && parts[3] == "batch-import":
			route.kind = "key_import"
			route.ids = []string{parts[1]}
			route.methods = []string{http.MethodPost}
		case len(parts) == 4 && parts[0] == "endpoints" && parts[2] == "keys":
			route.kind = "key_one"
			route.ids = []string{parts[1], parts[3]}
		case len(parts) == 3 && parts[0] == "models" && parts[2] == "bindings":
			route.kind = "bindings"
			route.ids = []string{parts[1]}
		case len(parts) == 4 && parts[0] == "models" && parts[2] == "bindings" && parts[3] == "batch":
			route.kind = "binding_append"
			route.ids = []string{parts[1]}
			route.methods = []string{http.MethodPost}
		default:
			return automationRoute{}
		}
	} else {
		switch {
		case len(parts) == 1 && parts[0] == "charity-models":
			route.kind = "charity_models"
		case len(parts) == 2 && (parts[0] == "donations" || parts[0] == "charity-models"):
			route.kind = parts[0] + "_one"
			route.ids = parts[1:]
		case len(parts) == 3 && parts[0] == "donations" && parts[2] == "keys":
			route.kind = "donation_keys"
			route.ids = []string{parts[1]}
		case len(parts) == 4 && parts[0] == "donations" && parts[2] == "keys":
			route.kind = "donation_key"
			route.ids = []string{parts[1], parts[3]}
		case len(parts) == 5 && parts[0] == "donations" && parts[2] == "keys" && parts[4] == "catalog":
			route.kind = "donation_catalog"
			route.ids = []string{parts[1], parts[3]}
		case len(parts) == 3 && parts[0] == "charity-models" && parts[2] == "bindings":
			route.kind = "charity_bindings"
			route.ids = []string{parts[1]}
		case len(parts) == 3 && parts[0] == "charity-models" && parts[2] == "binding-candidates":
			route.kind = "charity_candidates"
			route.ids = []string{parts[1]}
		default:
			return automationRoute{}
		}
	}
	return route
}

// RouteMethods is the finite CallerKey ingress allowlist used by root wiring.
func RouteMethods(path string) []string { return matchAutomationRoute(path).methods }
func IsPersonalPath(path string) bool   { return matchAutomationRoute(path).personal }

type importKey struct {
	Secret          *string `json:"secret"`
	Note            string  `json:"note"`
	Enabled         *bool   `json:"enabled"`
	ForceStoreFalse bool    `json:"force_store_false"`
	MaxConcurrency  int64   `json:"max_concurrency"`
	MaxRPM          int64   `json:"max_rpm"`
}
type importInput struct {
	OwnershipConfirmed bool        `json:"ownership_confirmed"`
	Keys               []importKey `json:"keys"`
}
type appendInput struct {
	EndpointKeyIDs  []string `json:"endpoint_key_ids"`
	UpstreamModelID string   `json:"upstream_model_id"`
	CatalogMode     string   `json:"catalog_mode"`
}

type itemResult struct {
	Index         int    `json:"index"`
	EndpointKeyID string `json:"endpoint_key_id,omitempty"`
	Status        string `json:"status"`
	Outcome       string `json:"outcome,omitempty"`
	BindingID     string `json:"binding_id,omitempty"`
	Code          string `json:"code,omitempty"`
	Message       string `json:"message,omitempty"`
}
type batchResult struct {
	EndpointID string       `json:"endpoint_id,omitempty"`
	ModelID    string       `json:"model_id,omitempty"`
	Results    []itemResult `json:"results"`
}

func canonicalImport(input importInput) ([]byte, error) {
	type key struct {
		Secret          string `json:"secret"`
		Note            string `json:"note"`
		Enabled         bool   `json:"enabled"`
		ForceStoreFalse bool   `json:"force_store_false"`
		MaxConcurrency  int64  `json:"max_concurrency"`
		MaxRPM          int64  `json:"max_rpm"`
	}
	out := struct {
		OwnershipConfirmed bool  `json:"ownership_confirmed"`
		Keys               []key `json:"keys"`
	}{OwnershipConfirmed: input.OwnershipConfirmed, Keys: make([]key, len(input.Keys))}
	for i, v := range input.Keys {
		if v.Secret == nil {
			return nil, errInvalid
		}
		out.Keys[i] = key{*v.Secret, v.Note, enabled(v.Enabled), v.ForceStoreFalse, v.MaxConcurrency, v.MaxRPM}
	}
	return json.Marshal(out)
}
