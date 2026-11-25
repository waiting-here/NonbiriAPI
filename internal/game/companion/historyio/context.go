// Package historyio provides decision-scoped, read-only access to game records.
package historyio

import (
	"context"
	"encoding/json"
	"errors"
)

type Reader func(context.Context, string, json.RawMessage) (json.RawMessage, error)
type key struct{}

func WithReader(ctx context.Context, reader Reader) context.Context {
	return context.WithValue(ctx, key{}, reader)
}
func Call(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reader, ok := ctx.Value(key{}).(Reader)
	if !ok {
		return nil, errors.New("expired")
	}
	return reader(ctx, op, args)
}
