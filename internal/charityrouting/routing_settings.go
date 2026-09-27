package charityrouting

import (
	"context"
	"database/sql"
	"fmt"
)

const (
	DefaultAffinityTTLSeconds = 300
	maxAffinityTTLSeconds     = 86_400
)

func validAffinityTTL(seconds int) bool {
	return seconds >= 1 && seconds <= maxAffinityTTLSeconds
}

func insertRoutingSettings(ctx context.Context, tx *sql.Tx, modelID int64, requested *int) error {
	ttl := DefaultAffinityTTLSeconds
	if requested != nil {
		ttl = *requested
	}
	if modelID <= 0 || !validAffinityTTL(ttl) {
		return ErrInvalidRequest
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO charity_routing_settings(model_id,revision,affinity_ttl_seconds) VALUES(?,1,?)`, modelID, ttl); err != nil {
		return fmt.Errorf("charity routing: initialize routing settings: %w", err)
	}
	return nil
}

func updateRoutingSettings(ctx context.Context, tx *sql.Tx, modelID int64, strategyChanged bool, requested *int) error {
	if modelID <= 0 || requested != nil && !validAffinityTTL(*requested) {
		return ErrInvalidRequest
	}
	var currentTTL int
	if err := tx.QueryRowContext(ctx, `SELECT affinity_ttl_seconds FROM charity_routing_settings WHERE model_id=?`, modelID).Scan(&currentTTL); err != nil {
		return fmt.Errorf("charity routing: read routing settings: %w", err)
	}
	if !validAffinityTTL(currentTTL) {
		return ErrInvariant
	}
	ttl := currentTTL
	if requested != nil {
		ttl = *requested
	}
	if !strategyChanged && ttl == currentTTL {
		return nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE charity_routing_settings
SET affinity_ttl_seconds=?,revision=revision+1
WHERE model_id=? AND revision<9223372036854775807`, ttl, modelID)
	if err != nil {
		return fmt.Errorf("charity routing: update routing settings: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return ErrInvariant
	}
	return nil
}
