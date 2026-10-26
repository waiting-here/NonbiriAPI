package v1

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type OfflineSchema struct {
	Version      int
	ManifestHash string
}

// ConvertOffline checks or converts one exact source in an exclusive transaction.
// The caller owns the stopped service and its recovery snapshot.
func ConvertOffline(ctx context.Context, path string, codec secret.GenerationTwoContextCodec, source OfflineSchema, apply bool, check func(context.Context, *sql.DB) error, convert func(context.Context, *sql.DB, *sql.Tx) error) (bool, error) {
	// Arbitrary caller transforms have no compiled impact classification.
	return convertOffline(ctx, path, codec, []OfflineSchema{source}, apply, false, check, convert)
}

func convertOffline(ctx context.Context, path string, codec secret.GenerationTwoContextCodec, sources []OfflineSchema, apply, classified bool, check func(context.Context, *sql.DB) error, convert func(context.Context, *sql.DB, *sql.Tx) error) (changed bool, result error) {
	if ctx == nil || codec == nil || len(sources) == 0 || convert == nil {
		return false, errors.New("offline conversion dependencies are required")
	}
	for _, source := range sources {
		if source.Version < 1 || source.ManifestHash == "" {
			return false, errors.New("offline source identity is required")
		}
	}
	inspect := func(q queryer) (bool, error) {
		version, err := readSchemaVersion(ctx, q)
		if err != nil {
			return false, err
		}
		if version > db.CurrentSchemaVersion {
			return false, &db.StartupError{Kind: db.StartupSchemaMismatch, ExpectedSchemaVersion: db.CurrentSchemaVersion, ActualSchemaVersion: &version}
		}
		hash, err := db.SchemaManifestHash(ctx, q)
		if err != nil {
			return false, err
		}
		if version == db.CurrentSchemaVersion && hash == db.PinnedGenerationTwoManifestHash {
			return false, nil
		}
		for _, source := range sources {
			if version == source.Version && hash == source.ManifestHash {
				return true, nil
			}
		}
		return false, &db.StartupError{Kind: db.StartupSchemaMismatch, ExpectedSchemaVersion: db.CurrentSchemaVersion, ActualSchemaVersion: &version}
	}
	finishIdentity := beginProgress(ctx, "identity", 0, "")
	identityFinished := false
	defer func() {
		if !identityFinished {
			finishIdentity(result)
		}
	}()
	err := db.InspectContext(ctx, path, codec, func(ctx context.Context, read *sql.DB) error {
		needed, err := inspect(read)
		if err != nil {
			return err
		}
		changed = needed
		sourceVersion, err := readSchemaVersion(ctx, read)
		if err != nil {
			return err
		}
		setProgressSource(ctx, sourceVersion)
		planVersion := sourceVersion
		if sourceVersion == 16 {
			planVersion = 17
		}
		plan, err := storageSchema.planFromValidatedVersion(planVersion)
		if err != nil {
			return err
		}
		if needed && check != nil {
			if err := check(ctx, read); err != nil {
				return err
			}
		}
		finishIdentity(nil)
		identityFinished = true
		fullAudit := !apply || sourceVersion == 16 || !classified
		if fullAudit || plan.auditsCredentials() {
			if err := progressStep(ctx, "audit", 0, "endpoint_key_secrets", func() error {
				if sourceVersion <= 26 {
					return db.ValidateLegacyStoredEndpointCredentials(ctx, read, codec)
				}
				return db.ValidateStoredEndpointCredentials(ctx, read, codec)
			}); err != nil {
				return err
			}
		}
		if fullAudit {
			if err := progressStep(ctx, "audit", 0, "", func() error { return checkIntegrity(ctx, read) }); err != nil {
				return err
			}
		}
		if !needed {
			return progressStep(ctx, "target", 0, "", func() error { return db.ValidateCurrentStorage(ctx, read, codec) })
		}
		if !apply {
			return nil
		}
		finishIdentity = beginProgress(ctx, "identity", 0, "")
		identityFinished = false
		// A WAL reader retains shared-memory locks even after its queries finish.
		// Keep the process ownership lease, but close this SQLite connection
		// before acquiring exclusive access on the conversion connection.
		if err := read.Close(); err != nil {
			return err
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(absolute)
		if runtime.GOOS == "windows" && !strings.HasPrefix(slash, "/") {
			slash = "/" + slash
		}
		databaseURL := url.URL{Scheme: "file", Path: slash}
		dsn := databaseURL.String() + "?mode=rw&_pragma=busy_timeout(5000)&_pragma=locking_mode(EXCLUSIVE)&_txlock=exclusive"
		database, err := sql.Open("sqlite", dsn)
		if err != nil {
			return err
		}
		database.SetMaxOpenConns(1)
		defer database.Close()
		var sourceRelationships map[string]bool
		return runConversionTransaction(ctx, database, fullAudit || plan.rebuildsTables(), func(ctx context.Context, tx *sql.Tx) error {
			if err := db.ValidateDatabaseIdentity(ctx, tx); err != nil {
				return err
			}
			needed, err := inspect(tx)
			if err != nil {
				return err
			}
			version, err := readSchemaVersion(ctx, tx)
			if err != nil {
				return err
			}
			if !needed || version != sourceVersion {
				return errors.New("conversion source changed before exclusive access")
			}
			if err := validateMaterial(ctx, tx, codec); err != nil {
				return err
			}
			finishIdentity(nil)
			identityFinished = true
			if !fullAudit {
				sourceRelationships, err = plan.relationshipTables(ctx, tx)
				if err != nil {
					return err
				}
			}
			if sourceVersion == 16 {
				if err := progressStep(ctx, "migration", 17, "", func() error { return convert(ctx, database, tx) }); err != nil {
					return err
				}
			} else if err := convert(ctx, database, tx); err != nil {
				return err
			}
			version, err = readSchemaVersion(ctx, tx)
			if err != nil {
				return err
			}
			// The exact source was checked under this exclusive transaction.
			// The prerelease transform, when needed, establishes version 17;
			// the final complete manifest remains the commit boundary.
			plan, err := storageSchema.planFromValidatedVersion(version)
			if err != nil {
				return err
			}
			if err := plan.applySteps(ctx, tx); err != nil {
				return err
			}
			return nil
		}, func(ctx context.Context, tx *sql.Tx) error {
			if fullAudit {
				if err := progressStep(ctx, "relationships", 0, "", func() error {
					return checkForeignKeys(ctx, tx, "PRAGMA foreign_key_check")
				}); err != nil {
					return err
				}
			} else {
				targetRelationships, err := plan.relationshipTables(ctx, tx)
				if err != nil {
					return err
				}
				if err := checkRelationships(ctx, tx, sourceRelationships, targetRelationships); err != nil {
					return err
				}
			}
			return progressStep(ctx, "target", 0, "", func() error { return db.ValidateCurrentStorage(ctx, tx, codec) })
		})
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, db.ErrDatabaseInUse) {
			return false, err
		}
		var startup *db.StartupError
		if errors.As(err, &startup) {
			return false, err
		}
		return false, &db.StartupError{Kind: db.StartupSchemaMismatch}
	}
	return changed, nil
}

func validateMaterial(ctx context.Context, q queryer, codec secret.GenerationTwoContextCodec) error {
	var version int
	var envelope []byte
	if err := q.QueryRowContext(ctx, "SELECT version,envelope FROM donation_review_material WHERE id=1").Scan(&version, &envelope); err != nil || version != 1 {
		return secret.ErrReviewMaterial
	}
	defer clear(envelope)
	return codec.ValidateDatabaseMaterial(envelope)
}

func checkIntegrity(ctx context.Context, q queryer) error {
	rows, err := q.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return err
		}
		if value != "ok" {
			rows.Close()
			return errors.New("source integrity check failed")
		}
		count++
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("source integrity result is missing")
	}
	return checkForeignKeys(ctx, q, "PRAGMA foreign_key_check")
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
