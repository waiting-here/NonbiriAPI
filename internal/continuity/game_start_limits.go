package continuity

import (
	"context"
	"database/sql"
	"encoding/hex"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

type StartWindows struct{ Service *Service }

var _ game.StartContinuity = StartWindows{}

func (adapter StartWindows) Identity(ctx context.Context, userID int64) (string, error) {
	key, err := adapter.Service.UserKey(ctx, userID)
	return hex.EncodeToString(key[:]), err
}

func (adapter StartWindows) IdentityTx(ctx context.Context, tx *sql.Tx, userID int64) (string, error) {
	key, err := adapter.Service.UserKeyTx(ctx, tx, userID)
	return hex.EncodeToString(key[:]), err
}

func (adapter StartWindows) LoadTx(ctx context.Context, tx *sql.Tx, userID, nowMillis int64) (string, []game.StartWindowFact, error) {
	key, err := adapter.Service.UserKeyTx(ctx, tx, userID)
	if err != nil {
		return "", nil, err
	}
	events, err := LoadWindowTx(ctx, tx, key, GameStart, "v1", nowMillis, game.FishingStartsPerMinute)
	if err != nil {
		return "", nil, err
	}
	facts := make([]game.StartWindowFact, 0, len(events))
	for _, event := range events {
		if event.Count != 1 || event.Value != nil {
			return "", nil, ErrInvariant
		}
		facts = append(facts, game.StartWindowFact{ID: event.ID, AtMillis: event.AtMillis, ExpiresMillis: event.ExpiresMillis})
	}
	return hex.EncodeToString(key[:]), facts, nil
}

func (adapter StartWindows) SaveTx(ctx context.Context, tx *sql.Tx, userID, nowMillis int64, facts []game.StartWindowFact) error {
	key, err := adapter.Service.UserKeyTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	events := make([]WindowEvent, 0, len(facts))
	for _, fact := range facts {
		events = append(events, WindowEvent{ID: fact.ID, AtMillis: fact.AtMillis, ExpiresMillis: fact.ExpiresMillis, Count: 1})
	}
	return SaveWindowTx(ctx, tx, key, GameStart, "v1", nowMillis, events)
}
