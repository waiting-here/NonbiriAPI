package db

// Generation 2 configuration accessors used by the public bootstrap
// projection and the site-timezone guard. User-management, session, and
// caller-key repositories deliberately do not live in the atomic baseline:
// their authenticated mutation/authorization owners register them after the
// corresponding Generation 2 contracts are implemented.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxGenerationTwoConfigKeyBytes = 128

func generationTwoSiteConfigKeyOwnedByDomain(key string) bool {
	return key == "maintenance_mode" || key == generationTwoAnnouncementEpochKey ||
		key == "activities_enabled" || strings.HasPrefix(key, "activity_") ||
		key == "games_enabled" || strings.HasPrefix(key, "game_")
}

func validateGenerationTwoConfigKey(key string) error {
	if key == "" || len(key) > maxGenerationTwoConfigKeyBytes || !utf8.ValidString(key) {
		return errors.New("invalid configuration key")
	}
	for _, r := range key {
		if unicode.IsControl(r) || r == 0x7f {
			return errors.New("invalid configuration key")
		}
	}
	return nil
}

// SetSiteConfigValue validates the resulting full Generation 2 configuration
// snapshot and persists the value with the matching site revision in one
// transaction. The timezone key has an additional immutable-offset guard.
func (s *Store) SetSiteConfigValue(key, value string) error {
	if s == nil || s.db == nil {
		return ErrInvalidSiteConfig
	}
	if key == SiteTimezoneKey {
		minutes, err := parseSiteTimezoneOffset(value)
		if err != nil {
			return ErrConflict
		}
		return s.SetSiteTimezoneOffsetMinutes(int(minutes))
	}
	if key == timezoneLockKey {
		return ErrConflict
	}
	if generationTwoSiteConfigKeyOwnedByDomain(key) {
		return ErrConflict
	}
	if err := validateGenerationTwoConfigValue(key, value); err != nil {
		return ErrConflict
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("set site configuration: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	at := time.Now().Unix()
	if err := validateAndWriteGenerationTwoSiteConfigTx(context.Background(), tx, key, value, at); err != nil {
		return fmt.Errorf("set site configuration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set site configuration: commit: %w", err)
	}
	committed = true
	return nil
}

// GetAllSiteConfigValues returns a complete, validated public configuration
// snapshot. Corrupt or unknown rows fail closed instead of being silently
// omitted. The permanent timezone-freeze marker is internal metadata.
func (s *Store) GetAllSiteConfigValues() (map[string]string, error) {
	if s == nil || s.db == nil {
		return nil, ErrInvalidSiteConfig
	}
	values, err := validateCurrentGenerationTwoSiteConfig(context.Background(), s.db)
	if err != nil {
		return nil, fmt.Errorf("list site configuration: %w", err)
	}
	delete(values, timezoneLockKey)
	return values, nil
}

// GetSiteConfigValue returns one validated Generation 2 configuration value.
// Missing optional rows are represented by an empty string. Unknown keys are
// never exposed through this helper.
func (s *Store) GetSiteConfigValue(key string) (string, error) {
	return s.GetSiteConfigValueContext(context.Background(), key)
}

func (s *Store) GetSiteConfigValueContext(ctx context.Context, key string) (string, error) {
	if s == nil || s.db == nil || ctx == nil {
		return "", ErrInvalidSiteConfig
	}
	if err := validateGenerationTwoConfigKey(key); err != nil {
		return "", ErrNotFound
	}
	values, err := validateCurrentGenerationTwoSiteConfig(ctx, s.db)
	if err != nil {
		return "", err
	}
	if key == timezoneLockKey {
		return "", nil
	}
	value, ok := values[key]
	if !ok {
		return "", nil
	}
	return value, nil
}
