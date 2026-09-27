package limitedactivities

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
)

// The activity directory has no engine settings for this module. Its typed
// level, period and economy configuration belongs to the game service.
type emptyConfiguration struct{}

func (emptyConfiguration) Normalize(raw json.RawMessage) (json.RawMessage, error) {
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil || compact.String() != "{}" {
		return nil, ErrInvalid
	}
	return json.RawMessage(`{}`), nil
}

func (emptyConfiguration) PublicTx(_ context.Context, _ *sql.Tx, _ string, raw json.RawMessage) (json.RawMessage, error) {
	return emptyConfiguration{}.Normalize(raw)
}

func (emptyConfiguration) ApplyTx(context.Context, *sql.Tx, string, json.RawMessage) error {
	return nil
}
