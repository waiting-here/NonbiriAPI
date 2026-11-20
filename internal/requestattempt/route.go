package requestattempt

import (
	"context"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// RouteObservation records a platform decision before a wire attempt. It
// contains only internal binding numbers and closed protocol and reason names.
type RouteObservation struct {
	Stage         string                 `json:"stage,omitempty"`
	Reason        string                 `json:"reason"`
	BindingID     string                 `json:"binding_id,omitempty"`
	NativeService contract.NativeService `json:"native_service,omitempty"`
	Match         string                 `json:"match,omitempty"`
	Field         string                 `json:"field,omitempty"`
}

type RouteDecision struct {
	Observations []RouteObservation `json:"observations"`
	Truncated    bool               `json:"truncated,omitempty"`
}

// Reserve room for protocol checks even when many earlier bindings were skipped.
const maxRouteObservations = 16

func (o RouteObservation) valid() bool {
	switch o.Reason {
	case "no_bindings", "failure_disabled", "cooldown", "unavailable", "expired", "capacity", "quota",
		"google_daily_requests", "google_minute_requests", "google_minute_input_tokens", "google_daily_input_tokens", "google_model_quota",
		"key_rpm", "key_concurrency", "key_concurrency_rpm", "key_queue_full", "key_queue_timeout", "mixed_admission", "ledger_busy", "ledger_full",
		"key_rate_limit", "quota_storage_full", "routing_busy", "caller_level", "credential_unavailable", "dependency_unavailable",
		"operation_unsupported", "features_unsupported", "preparation_rejected", "continuation_mismatch", "protocol_rejected", "eligible":
	default:
		return false
	}
	if o.Stage != "" && o.Stage != "claim" && o.Stage != "dispatch" && o.Stage != "queue" && o.Stage != "accept" {
		return false
	}
	if o.BindingID != "" {
		id, err := strconv.ParseInt(o.BindingID, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != o.BindingID {
			return false
		}
	}
	if o.NativeService != "" {
		if _, err := contract.ParseNativeServices([]contract.NativeService{o.NativeService}); err != nil {
			return false
		}
	}
	if o.Match != "" && o.Match != "native" && o.Match != "conversion" {
		return false
	}
	return o.Field == "" || validRejectionField(o.Field)
}

func ObserveRoute(ctx context.Context, bindingID int64, service contract.NativeService, reason, match, field string) {
	recordRoute(ctx, bindingID, RouteObservation{Reason: reason, NativeService: service, Match: match, Field: field})
}

func ObserveRecheck(ctx context.Context, bindingID int64, service contract.NativeService, reason, stage string) {
	recordRoute(ctx, bindingID, RouteObservation{Reason: reason, NativeService: service, Stage: stage})
}

func recordRoute(ctx context.Context, bindingID int64, o RouteObservation) {
	a := get(ctx)
	if a == nil {
		return
	}
	if bindingID > 0 {
		o.BindingID = strconv.FormatInt(bindingID, 10)
	}
	if !o.valid() {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.route
	if d == nil {
		d = &RouteDecision{}
		a.route = d
	}
	for _, seen := range d.Observations {
		if seen == o {
			return
		}
	}
	limit := maxRouteObservations / 2
	if o.Match != "" {
		limit = maxRouteObservations - 1
	}
	if o.Stage != "" {
		limit = maxRouteObservations
	}
	if len(d.Observations) >= limit {
		d.Truncated = true
		return
	}
	d.Observations = append(d.Observations, o)
}

func TruncateRoute(ctx context.Context) {
	if a := get(ctx); a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.route == nil {
			a.route = &RouteDecision{}
		}
		a.route.Truncated = true
	}
}

func RouteSnapshot(ctx context.Context) *RouteDecision {
	a := get(ctx)
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.route
	if d == nil {
		return nil
	}
	return &RouteDecision{Observations: append([]RouteObservation(nil), d.Observations...), Truncated: d.Truncated}
}

// Essential retains the most specific local decision when delivery facts have
// used most of the storage budget. Other observations remain explicitly omitted.
func (d *RouteDecision) Essential() *RouteDecision {
	if d == nil || len(d.Observations) == 0 {
		return nil
	}
	chosen := d.Observations[0]
	for _, o := range d.Observations {
		if o.Stage != "" {
			chosen = o
			break
		}
		if o.Field != "" && chosen.Field == "" {
			chosen = o
		}
	}
	return &RouteDecision{Observations: []RouteObservation{chosen}, Truncated: d.Truncated || len(d.Observations) > 1}
}

func (d *RouteDecision) Valid() bool {
	if d == nil || len(d.Observations) == 0 || len(d.Observations) > maxRouteObservations {
		return false
	}
	for _, o := range d.Observations {
		if !o.valid() {
			return false
		}
	}
	return true
}
