package gacha

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/artwork"
)

type presetCatalog struct {
	Version int      `json:"version"`
	Presets []Preset `json:"presets"`
}

//go:embed presets.json
var presetsJSON []byte

var defaultPresets = func() presetCatalog {
	var out presetCatalog
	if err := json.Unmarshal(presetsJSON, &out); err != nil || out.Version < 1 {
		panic("invalid pool template catalog")
	}
	return out
}()

// SyncCatalog adds missing built-in templates. Content revisions are independent
// of database versions: saved templates, live pools and player facts stay intact.
func (s *Service) SyncCatalog(ctx context.Context) error {
	return syncPresets(ctx, s.config.Database, defaultPresets)
}

func builtInPreset(id string) bool {
	for _, preset := range defaultPresets.Presets {
		if preset.ID == id {
			return true
		}
	}
	return false
}

func syncPresets(ctx context.Context, database *sql.DB, content presetCatalog) error {
	if content.Version < 1 {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(content.Presets))
	for _, preset := range content.Presets {
		if preset.ID == "" || len(preset.ID) > 64 || preset.Name == "" || seen[preset.ID] || preset.Pool.ID != "" || preset.Pool.Published || preset.Pool.StartsAt != nil || preset.Pool.EndsAt != nil {
			return ErrInvalid
		}
		if _, err := validatePool(preset.Pool); err != nil {
			return fmt.Errorf("gacha: invalid template %q: %w", preset.ID, err)
		}
		seen[preset.ID] = true
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, preset := range content.Presets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO gacha_presets(id,name,config,revision) VALUES(?,?,?,1) ON CONFLICT(id) DO NOTHING`, preset.ID, preset.Name, jsonText(preset.Pool)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func poolCoverIDs() []string {
	out := []string{}
	for _, cover := range artwork.Covers() {
		out = append(out, cover.ID)
	}
	return out
}
