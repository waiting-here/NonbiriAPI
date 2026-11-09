package donation

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func (api *httpAPI) recurringOwner(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.recurringRole(w, r, p, recurringOwner)
}
func (api *httpAPI) recurringSteward(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.recurringRole(w, r, p, reviewerSteward)
}
func (api *httpAPI) recurringAdmin(w http.ResponseWriter, r *http.Request) {
	api.recurringRole(w, r, UserPrincipal{}, reviewerAdmin)
}

func (api *httpAPI) recurringRole(w http.ResponseWriter, r *http.Request, p UserPrincipal, role reviewerRole) {
	id, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	key, ok := parsePathID(w, r, "keyId")
	if !ok || !requireNoBody(w, r) {
		return
	}
	values, ok := requestQuery(w, r)
	if !ok {
		return
	}
	model := values.Get("upstream_model_id")
	if !exactQuery(values, "upstream_model_id") || (values.Has("upstream_model_id") && !donationquota.ValidModel(model)) {
		writeDonationError(w, ErrInvalidRequest)
		return
	}
	out, err := api.service.recurring(r.Context(), role, p.UserID, id, key, model)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	writeJSON(w, out)
}

type recurringRuleWire struct {
	ID           nullableField[string] `json:"id"`
	Mode         requiredField[string] `json:"mode"`
	Interval     requiredField[string] `json:"interval"`
	Alignment    nullableField[string] `json:"alignment"`
	AnchorLocal  nullableField[string] `json:"anchor_local"`
	TimeZone     requiredField[string] `json:"time_zone"`
	WeekStartsOn nullableField[int]    `json:"week_starts_on"`
	Metric       requiredField[string] `json:"metric"`
	Limit        requiredField[string] `json:"limit"`
}
type recurringWire struct {
	UpstreamModelID  requiredField[string]              `json:"upstream_model_id"`
	ExpectedRevision requiredField[string]              `json:"expected_revision"`
	Rules            requiredField[[]recurringRuleWire] `json:"rules"`
}

func (api *httpAPI) replaceRecurringSteward(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.replaceRecurringRole(w, r, p, reviewerSteward)
}
func (api *httpAPI) replaceRecurringAdmin(w http.ResponseWriter, r *http.Request) {
	api.replaceRecurringRole(w, r, UserPrincipal{}, reviewerAdmin)
}

func (api *httpAPI) replaceRecurringRole(w http.ResponseWriter, r *http.Request, p UserPrincipal, role reviewerRole) {
	id, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	key, ok := parsePathID(w, r, "keyId")
	if !ok || !requireEmptyQuery(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var wire recurringWire
	if !decodeStrictObject(w, r, &wire) {
		return
	}
	revision, err := requiredRevision(wire.ExpectedRevision)
	if err != nil || !wire.Rules.Set || len(wire.Rules.Value) > donationquota.MaxRules {
		writeDonationError(w, ErrInvalidRequest)
		return
	}
	rules := make([]donationquota.RuleInput, 0, len(wire.Rules.Value))
	for _, v := range wire.Rules.Value {
		if !v.ID.Set || !v.Mode.Set || !v.Interval.Set || !v.Alignment.Set || !v.TimeZone.Set || !v.WeekStartsOn.Set || !v.Metric.Set || !v.Limit.Set {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
		rule := donationquota.RuleInput{ID: v.ID.Value, Mode: v.Mode.Value, Interval: v.Interval.Value, Alignment: v.Alignment.Value, AnchorLocal: v.AnchorLocal.Value, TimeZone: v.TimeZone.Value, WeekStartsOn: v.WeekStartsOn.Value, Metric: v.Metric.Value, Limit: v.Limit.Value}
		if donationquota.Validate(rule) != nil {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
		rules = append(rules, rule)
	}
	route := routeAdminRecurring
	if role == reviewerSteward {
		route = routeStewardRecurring
	}
	canonical := map[string]any{"expected_revision": wire.ExpectedRevision.Value, "rules": rules}
	if wire.UpstreamModelID.Set {
		if !donationquota.ValidModel(wire.UpstreamModelID.Value) {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
		canonical["upstream_model_id"] = wire.UpstreamModelID.Value
	}
	mutation, ok := mutationFor(w, r, route, []int64{id, key}, canonical)
	if !ok {
		return
	}
	out, err := api.service.replaceRecurring(r.Context(), role, p.UserID, id, key, mutation, revision, rules, wire.UpstreamModelID.Value)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	writeMutation(w, out)
}
