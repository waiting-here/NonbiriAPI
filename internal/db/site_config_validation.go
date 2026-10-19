package db

import (
	"context"
	"maps"
	"sync"
)

// Only deterministic validation is reused. Each read obtains the full current
// database snapshot; no revision, expiry or domain health result is cached.
type siteConfigValidation struct {
	mu     sync.RWMutex
	values map[string]string
}

func (s *Store) readValidatedSiteConfig(ctx context.Context) (map[string]string, error) {
	values, err := readGenerationTwoSiteConfigSnapshot(ctx, s.readDB)
	if err != nil {
		return nil, err
	}
	s.configValidation.mu.RLock()
	valid := s.configValidation.values != nil && maps.Equal(s.configValidation.values, values)
	s.configValidation.mu.RUnlock()
	if !valid {
		if err := validateGenerationTwoSiteConfigSnapshot(values); err != nil {
			return nil, err
		}
		s.configValidation.mu.Lock()
		// The caller receives its own map and may change it freely.
		s.configValidation.values = maps.Clone(values)
		s.configValidation.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return values, nil
}
