package flowcontrol

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

func (c *Controller) loadRPMWindow(ctx context.Context, userID int64) (string, error) {
	if c.continuity == nil {
		return userKey(userID), nil
	}
	key, events, err := c.continuity.LoadWindow(ctx, userID, continuity.UserRPM, "v1", c.now().UnixMilli(), continuity.MaxWindowEvents)
	if errors.Is(err, continuity.ErrNotFound) {
		return "", ErrInvalidUser
	}
	if err != nil {
		return "", err
	}
	facts := make([]ratelimit.WindowFact, 0, len(events))
	for _, event := range events {
		if event.Count != 1 || event.Value != nil {
			return "", continuity.ErrInvariant
		}
		facts = append(facts, ratelimit.WindowFact{ID: event.ID, At: time.UnixMilli(event.AtMillis), Expires: time.UnixMilli(event.ExpiresMillis)})
	}
	rpmKey := hex.EncodeToString(key[:])
	if err := c.limiter.MergeUser(rpmKey, facts); err != nil {
		return "", err
	}
	return rpmKey, nil
}

// PreserveWindowTx runs after the lifecycle gate drains admitted requests,
// while the deletion transaction still has its original identity binding.
func (c *Controller) PreserveWindowTx(ctx context.Context, tx *sql.Tx, userID, _ int64) error {
	if c == nil || c.continuity == nil {
		return ErrClosed
	}
	key, err := c.continuity.UserKeyTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	now, facts, err := c.limiter.SnapshotUser(hex.EncodeToString(key[:]))
	if err != nil {
		return err
	}
	events := make([]continuity.WindowEvent, 0, len(facts))
	for _, fact := range facts {
		events = append(events, continuity.WindowEvent{ID: fact.ID, AtMillis: fact.At.UnixMilli(), ExpiresMillis: fact.Expires.Add(time.Millisecond - 1).UnixMilli(), Count: 1})
	}
	return continuity.SaveWindowTx(ctx, tx, key, continuity.UserRPM, "v1", now.UnixMilli(), events)
}
