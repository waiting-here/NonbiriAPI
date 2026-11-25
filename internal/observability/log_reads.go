package observability

import (
	"context"
	"errors"
	"sync"
	"time"
)

const slowLogPhase = 500 * time.Millisecond

// Only the presence of each filter is retained. Values and combinations
// never become keys in the performance series.
type LogFilterShape struct {
	Operation     bool `json:"operation,omitempty"`
	Outcome       bool `json:"outcome,omitempty"`
	Phase         bool `json:"phase,omitempty"`
	User          bool `json:"user,omitempty"`
	Discord       bool `json:"discord,omitempty"`
	EndpointKey   bool `json:"endpoint_key,omitempty"`
	EndpointURL   bool `json:"endpoint_url,omitempty"`
	UpstreamModel bool `json:"upstream_model,omitempty"`
	Model         bool `json:"model,omitempty"`
	CharityModel  bool `json:"charity_model,omitempty"`
	Status        bool `json:"status,omitempty"`
	ErrorCode     bool `json:"error_code,omitempty"`
	ErrorCategory bool `json:"error_category,omitempty"`
	ErrorPhase    bool `json:"error_phase,omitempty"`
	UsageMismatch bool `json:"usage_mismatch,omitempty"`
}

type LogReadShape struct {
	LogFilterShape
	Page      string `json:"page"`
	TimeRange string `json:"time_range"`
}

type logShapeKey struct{}

func WithLogReadShape(ctx context.Context, filters LogFilterShape, page int64, from, to *int64) context.Context {
	if p, _ := ctx.Value(performanceContextKey{}).(*Performance); p == nil {
		return ctx
	}
	s := &LogReadShape{LogFilterShape: filters}
	switch {
	case page == 0:
		s.Page = "cursor"
	case page == 1:
		s.Page = "first"
	case page <= 10:
		s.Page = "2-10"
	case page <= 100:
		s.Page = "11-100"
	default:
		s.Page = "101+"
	}
	switch {
	case from == nil && to == nil:
		s.TimeRange = "all"
	case from == nil:
		s.TimeRange = "until"
	case to == nil:
		s.TimeRange = "since"
	case *to-*from <= 3600:
		s.TimeRange = "up_to_hour"
	case *to-*from <= 86400:
		s.TimeRange = "up_to_day"
	case *to-*from <= 7*86400:
		s.TimeRange = "up_to_week"
	default:
		s.TimeRange = "over_week"
	}
	return context.WithValue(ctx, logShapeKey{}, s)
}

func logPhaseTimer(ctx context.Context, label string) func(error) {
	if ctx == nil {
		return func(error) {}
	}
	p, _ := ctx.Value(performanceContextKey{}).(*Performance)
	if p == nil {
		return func(error) {}
	}
	shape, _ := ctx.Value(logShapeKey{}).(*LogReadShape)
	start := time.Now()
	finish := p.beginSample(performanceKey{label, ""})
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			deadline := errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded)
			canceled := !deadline && (errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled))
			finish(time.Since(start), 0, performanceResult{failed: err != nil && !deadline && !canceled, canceled: canceled, deadline: deadline, shape: shape})
		})
	}
}
