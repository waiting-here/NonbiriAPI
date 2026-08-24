package db

// Atomic Fishing configuration persistence. The game registry owns the
// closed key set and complete economy compiler; this repository owns the
// transaction which reads the current snapshot, merges a partial admin
// update, validates the resulting whole snapshot, and writes all touched rows
// together. No caller can save a half-valid economy.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

// GetGameConfigSnapshot reads and compiles one coherent game configuration
// snapshot. Missing economy rows use the registry defaults; malformed
// economy rows fail closed. Toggle corruption projects disabled only in the
// registry's read semantics and never enables a game unexpectedly.
func (s *Store) GetGameConfigSnapshot(ctx context.Context) (game.ConfigSnapshot, error) {
	if s == nil || s.db == nil || ctx == nil {
		return game.ConfigSnapshot{}, ErrInvalidSiteConfig
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return game.ConfigSnapshot{}, fmt.Errorf("read game config: begin: %w", err)
	}
	defer tx.Rollback()
	snapshot, err := readGameConfigTx(ctx, tx)
	if err != nil {
		return game.ConfigSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return game.ConfigSnapshot{}, fmt.Errorf("read game config: commit: %w", err)
	}
	return snapshot, nil
}

// PatchGameConfig atomically merges touched game keys into the current raw
// snapshot. Values must already be strict canonical strings; the complete
// candidate is compiled before any row is written. The administrator's
// console write is recorded once in the same transaction when activity is
// enabled. An empty changes map is rejected.
func (s *Store) PatchGameConfig(ctx context.Context, actorUserID int64, changes map[string]string, at time.Time) error {
	if s == nil || s.db == nil || ctx == nil || actorUserID <= 0 || at.IsZero() || len(changes) == 0 {
		return ErrConflict
	}
	allowed := make(map[string]struct{}, len(game.SiteConfigKeys()))
	for _, key := range game.SiteConfigKeys() {
		allowed[key] = struct{}{}
	}
	for key, value := range changes {
		if _, ok := allowed[key]; !ok || !validGameConfigStoredValue(key, value) {
			return ErrConflict
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("patch game config: begin: %w", err)
	}
	defer tx.Rollback()
	raw := make(map[string]string, len(allowed))
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM site_config`)
	if err != nil {
		return fmt.Errorf("patch game config: read: %w", err)
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return fmt.Errorf("patch game config: read: %w", err)
		}
		if _, ok := allowed[key]; ok {
			raw[key] = value
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("patch game config: read: %w", err)
	}
	rows.Close()
	for key, value := range changes {
		raw[key] = value
	}
	if _, err := game.CompileConfig(raw); err != nil {
		return ErrInvalidSiteConfig
	}
	keys := make([]string, 0, len(changes))
	for key := range changes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, changes[key], at.Unix()); err != nil {
			return fmt.Errorf("patch game config: write: %w", err)
		}
	}
	dayKey, dayErr := siteDayKeyAtTx(tx, at.Unix())
	if errors.Is(dayErr, ErrTimezoneUnavailable) {
		// Configuration remains valid while activity is disabled.
	} else if dayErr != nil {
		return fmt.Errorf("patch game config: activity day: %w", dayErr)
	} else if _, err := recordActivityTx(ctx, tx, actorUserID, dayKey, ActivityDelta{ConsoleWrites: 1}, at.Unix()); err != nil {
		return fmt.Errorf("patch game config: activity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("patch game config: commit: %w", err)
	}
	return nil
}

func validGameConfigStoredValue(key, value string) bool {
	switch key {
	case game.GamesEnabledKey, game.FishingEnabledKey:
		return value == "0" || value == "1"
	case game.FishingWormPriceMilliKey, game.FishingLurePriceMilliKey, game.FishingPremiumPriceMilliKey,
		game.FishingStandardRTPKey, game.FishingPremiumRTPKey,
		game.FishingTreasureBottleMultiplierKey, game.FishingTreasureCloverMultiplierKey,
		game.FishingTreasureShellMultiplierKey:
		return value != "" && value[0] != '+' && value[0] != '-' // complete compiler enforces range/canonicality
	default:
		return false
	}
}
