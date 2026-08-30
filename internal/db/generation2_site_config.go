package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const generationTwoMaxConfigRevision = int64(9223372036854775807)

// readGenerationTwoSiteConfigSnapshot reads the complete site configuration
// without dropping rows. The timezone freeze marker is storage metadata, not
// a public catalog key, so it is retained in the returned map and removed
// only by generationTwoConfigSnapshotForValidation.
func readGenerationTwoSiteConfigSnapshot(ctx context.Context, q generationTwoConfigQueryer) (map[string]string, error) {
	if ctx == nil {
		return nil, errors.New("site configuration: nil context")
	}
	rows, err := q.QueryContext(ctx, `SELECT key,value FROM site_config ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("read site configuration: %w", err)
	}
	defer rows.Close()
	values := make(map[string]string, len(generationTwoConfigCatalog)+2)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("read site configuration: scan: %w", err)
		}
		if _, duplicate := values[key]; duplicate {
			return nil, fmt.Errorf("site configuration: duplicate key %q", key)
		}
		if key == timezoneLockKey {
			if value != "1" {
				return nil, fmt.Errorf("site configuration: invalid timezone lock marker")
			}
		} else if err := validateGenerationTwoConfigValue(key, value); err != nil {
			return nil, fmt.Errorf("site configuration %q: %w", key, err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read site configuration: iterate: %w", err)
	}
	return values, nil
}

func generationTwoConfigSnapshotForValidation(values map[string]string) map[string]string {
	clean := make(map[string]string, len(values))
	for key, value := range values {
		if key == timezoneLockKey {
			continue
		}
		clean[key] = value
	}
	return clean
}

func validateGenerationTwoSiteConfigSnapshot(ctx context.Context, q generationTwoConfigQueryer, values map[string]string) error {
	if err := ValidateGenerationTwoConfigSnapshot(generationTwoConfigSnapshotForValidation(values)); err != nil {
		return err
	}
	clean := generationTwoConfigSnapshotForValidation(values)
	if generationTwoConfigBoolValue(clean, "activity_thursday_enabled") {
		var ready int
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM thursday_periods WHERE state IN ('configured','open','settling')
		)`).Scan(&ready); err != nil {
			return fmt.Errorf("validate Thursday activity health: %w", err)
		}
		if err := ValidateGenerationTwoThursdayConfigHealth(clean, ready == 1); err != nil {
			return err
		}
	}
	return nil
}

func bumpGenerationTwoConfigRevisionTx(ctx context.Context, tx *sql.Tx, domain string, at int64) error {
	if domain != "site" && domain != "activities" && domain != "games" {
		return errors.New("invalid configuration revision domain")
	}
	result, err := tx.ExecContext(ctx, `UPDATE config_revisions
		SET revision=revision+1, updated_at=?
		WHERE domain=? AND revision < ?`, at, domain, generationTwoMaxConfigRevision)
	if err != nil {
		return fmt.Errorf("advance %s configuration revision: %w", domain, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("advance %s configuration revision: %w", domain, err)
	}
	if changed != 1 {
		return fmt.Errorf("advance %s configuration revision: missing or exhausted revision", domain)
	}
	return nil
}

func validateAndWriteGenerationTwoSiteConfigTx(ctx context.Context, tx *sql.Tx, key, value string, at int64) error {
	if key == timezoneLockKey {
		return ErrConflict
	}
	if err := validateGenerationTwoConfigValue(key, value); err != nil {
		return ErrConflict
	}
	values, err := readGenerationTwoSiteConfigSnapshot(ctx, tx)
	if err != nil {
		return err
	}
	values[key] = value
	if err := validateGenerationTwoSiteConfigSnapshot(ctx, tx, values); err != nil {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, at); err != nil {
		return fmt.Errorf("write site configuration: %w", err)
	}
	return bumpGenerationTwoConfigRevisionTx(ctx, tx, "site", at)
}

func validateCurrentGenerationTwoSiteConfig(ctx context.Context, q generationTwoConfigQueryer) (map[string]string, error) {
	values, err := readGenerationTwoSiteConfigSnapshot(ctx, q)
	if err != nil {
		return nil, err
	}
	if err := validateGenerationTwoSiteConfigSnapshot(ctx, q, values); err != nil {
		return nil, err
	}
	return values, nil
}

func configRevisionTimestamp(at time.Time) (int64, error) {
	if at.IsZero() {
		return 0, errors.New("configuration write time is required")
	}
	return at.Unix(), nil
}
