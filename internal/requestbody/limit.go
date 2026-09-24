// Package requestbody owns the finite, per-request model input limit.
package requestbody

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

const (
	ConfigKey          = "model_request_body_limit_mib"
	MiB          int64 = 1 << 20
	DefaultMiB         = 10
	MaximumMiB         = 64
	DefaultBytes       = DefaultMiB * MiB
	MaximumBytes       = MaximumMiB * MiB
)

type Provider func(context.Context) (int64, error)

type snapshotKey struct{}
type snapshot struct {
	once     sync.Once
	provider Provider
	limit    int64
	err      error
}

// WithProvider defers the configuration read until admission or bounded denial
// classification needs it. A request keeps one snapshot across all parsing.
func WithProvider(next http.Handler, provider Provider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := &snapshot{provider: provider}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), snapshotKey{}, value)))
	})
}

func Limit(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	value, ok := ctx.Value(snapshotKey{}).(*snapshot)
	if !ok {
		return DefaultBytes, nil
	}
	value.once.Do(func() {
		value.limit = DefaultBytes
		if value.provider != nil {
			value.limit, value.err = value.provider(ctx)
		}
		if value.err == nil && (value.limit < MiB || value.limit > MaximumBytes || value.limit%MiB != 0) {
			value.err = errors.New("model request body limit unavailable")
		}
	})
	return value.limit, value.err
}

// DecoderLimit also permits smaller limits for bounded internal consumers.
func DecoderLimit(limit int64) int64 {
	if limit <= 0 {
		return DefaultBytes
	}
	return min(limit, MaximumBytes)
}
