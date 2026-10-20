package v1

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Data-only changes also advance version, even when manifest stays unchanged.
type schemaMigration struct {
	from     int
	version  int
	manifest string
	sql      string
	apply    func(context.Context, *sql.Tx) error
	impact   *migrationImpact
}

type schemaRegistry struct {
	baseline int
	target   int
	versions []schemaMigration
}

func (registry schemaRegistry) validate() error {
	if len(registry.versions) == 0 || registry.baseline < 1 || registry.versions[0].version != registry.baseline || registry.versions[len(registry.versions)-1].version != registry.target {
		return errors.New("schema baseline is missing")
	}
	validHash := func(value string) bool {
		raw, err := hex.DecodeString(value)
		return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == value
	}
	for i, step := range registry.versions {
		if !validHash(step.manifest) ||
			i == 0 && (step.from != 0 || step.sql != "" || step.apply != nil) ||
			i > 0 && (step.from != registry.versions[i-1].version || step.version <= step.from || step.sql == "" && step.apply == nil) {
			return errors.New("schema migration chain is incomplete")
		}
		if i > 0 {
			if err := step.impact.validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

type schemaPlan struct {
	steps  []schemaMigration
	target schemaMigration
}

func (plan schemaPlan) needed() bool { return len(plan.steps) != 0 }

func (registry schemaRegistry) plan(ctx context.Context, q queryer) (schemaPlan, error) {
	var plan schemaPlan
	if err := registry.validate(); err != nil {
		return plan, err
	}
	hash, err := db.SchemaManifestHash(ctx, q)
	if err != nil {
		return plan, err
	}
	plan.target = registry.versions[len(registry.versions)-1]
	version, err := readSchemaVersion(ctx, q)
	if err != nil {
		return plan, err
	}
	for i, source := range registry.versions {
		if source.version == version && source.manifest == hash {
			plan.steps = registry.versions[i+1:]
			return plan, nil
		}
	}
	return plan, errors.New("unsupported schema version or manifest")
}

func readSchemaVersion(ctx context.Context, q queryer) (int, error) {
	var version int
	err := q.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1").Scan(&version)
	return version, err
}

func (plan schemaPlan) apply(ctx context.Context, tx *sql.Tx) error {
	if !plan.needed() {
		return nil
	}
	if err := plan.applySteps(ctx, tx); err != nil {
		return err
	}
	hash, err := db.SchemaManifestHash(ctx, tx)
	if err != nil {
		return err
	}
	version, err := readSchemaVersion(ctx, tx)
	if err != nil {
		return err
	}
	if version != plan.target.version || hash != plan.target.manifest {
		return errors.New("schema migration did not reach its target")
	}
	return nil
}

// applySteps lets the offline transaction combine its single final manifest
// check with current seed, configuration and key validation before commit.
func (plan schemaPlan) applySteps(ctx context.Context, tx *sql.Tx) error {
	for _, step := range plan.steps {
		if err := progressStep(ctx, "migration", step.version, "", func() error {
			return step.applyStep(ctx, tx)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (step schemaMigration) applyStep(ctx context.Context, tx *sql.Tx) error {
	if err := step.impact.validate(); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, migrationImpactKey{}, step.impact)
	if step.sql != "" {
		if _, err := tx.ExecContext(ctx, step.sql); err != nil {
			return fmt.Errorf("migrate storage to %d: %w", step.version, err)
		}
	}
	if step.apply != nil {
		if err := step.apply(ctx, tx); err != nil {
			return fmt.Errorf("migrate data to %d: %w", step.version, err)
		}
	}
	if err := validateNewTables(ctx, tx, step.impact.addedTables); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE schema_state SET version=? WHERE id=1 AND version=?", step.version, step.from)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return errors.New("schema version changed during migration")
	}
	return nil
}

func (registry schemaRegistry) planFromValidatedVersion(version int) (schemaPlan, error) {
	if err := registry.validate(); err != nil {
		return schemaPlan{}, err
	}
	for i, source := range registry.versions {
		if source.version == version {
			return schemaPlan{steps: registry.versions[i+1:], target: registry.versions[len(registry.versions)-1]}, nil
		}
	}
	return schemaPlan{}, errors.New("unsupported conversion source version")
}

// The unclassified callback entry point retains a complete relationship check.
// Registered offline conversions use runConversionTransaction with their plan.
func runGenerationTwoExtension(ctx context.Context, database *sql.DB, extend func(context.Context, *sql.Tx) error) (result error) {
	return runConversionTransaction(ctx, database, true, extend, func(ctx context.Context, tx *sql.Tx) error {
		return checkForeignKeys(ctx, tx, "PRAGMA foreign_key_check")
	})
}

// Rollback and connection cleanup finish before ownership is returned, including
// after cancellation. Foreign keys are disabled only for a rebuilding chain.
func runConversionTransaction(ctx context.Context, database *sql.DB, rebuild bool, extend, validate func(context.Context, *sql.Tx) error) (result error) {
	conn, err := database.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return err
	}
	if rebuild {
		// Rebuilding a referenced table must not run ON DELETE actions.
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
	} else if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, fmt.Sprintf("PRAGMA foreign_keys=%d", foreignKeys)); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			result = errors.Join(result, err)
		}
	}()
	// Statements retain ctx; the detached transaction lifetime prevents an
	// asynchronous database/sql rollback from outliving the startup result.
	tx, err := conn.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			finish := beginProgress(ctx, "rollback", 0, "")
			err := tx.Rollback()
			if errors.Is(err, sql.ErrTxDone) {
				err = nil
			}
			finish(err)
			result = errors.Join(result, err)
		}
	}()
	if err := extend(ctx, tx); err != nil {
		return err
	}
	if validate != nil {
		if err := validate(ctx, tx); err != nil {
			return err
		}
	}
	return progressStep(ctx, "commit", 0, "", func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := tx.Commit()
		committed = err == nil
		return err
	})
}
