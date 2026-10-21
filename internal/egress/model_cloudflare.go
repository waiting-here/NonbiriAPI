package egress

import "context"

type modelCloudflareKey struct{}

// WithCloudflareProxy carries a frozen model choice through every connector.
// Calls without a model choice retain the configured endpoint routing.
func WithCloudflareProxy(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, modelCloudflareKey{}, enabled)
}

func cloudflareProxyEnabled(ctx context.Context) bool {
	enabled, present := ctx.Value(modelCloudflareKey{}).(bool)
	return !present || enabled
}

// CloudflareProxyMode reports the immutable deployment mode without secrets.
func (s *Stack) CloudflareProxyMode() string {
	if s != nil {
		if s.worker != nil {
			return "worker"
		}
		if len(s.routes) > 0 {
			return "legacy"
		}
	}
	return "direct"
}
