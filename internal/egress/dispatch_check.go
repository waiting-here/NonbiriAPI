package egress

import "context"

type dispatchCheckKey struct{}

// WithDispatchCheck attaches a live authorization check run after the shared
// egress wait and immediately before preparing the outbound request. It must
// return promptly and must not perform network I/O.
func WithDispatchCheck(ctx context.Context, check func(context.Context) error) context.Context {
	return context.WithValue(ctx, dispatchCheckKey{}, check)
}
func checkDispatch(ctx context.Context) error {
	if check, ok := ctx.Value(dispatchCheckKey{}).(func(context.Context) error); ok && check != nil {
		return check(ctx)
	}
	return nil
}
