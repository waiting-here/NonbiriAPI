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
	return convertOffline(ctx, path, codec, []OfflineSchema{source}, apply, check, convert)
}

func convertOffline(ctx context.Context, path string, codec secret.GenerationTwoContextCodec, sources []OfflineSchema, apply bool, check func(context.Context, *sql.DB) error, convert func(context.Context, *sql.DB, *sql.Tx) error) (changed bool, result error) {
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
		return false, errors.New("unsupported schema version or manifest")
	}
	err := db.InspectContext(ctx, path, codec, func(ctx context.Context, read *sql.DB) error {
		needed, err := inspect(read)
		if err != nil {
			return err
		}
		changed = needed
		if !needed {
			return db.ValidateCurrentStorage(ctx, read, codec)
		}
		if err := db.ValidateStoredEndpointCredentials(ctx, read, codec); err != nil {
			return err
		}
		if err := checkIntegrity(ctx, read); err != nil {
			return err
		}
		if check != nil {
			if err := check(ctx, read); err != nil {
				return err
			}
		}
		if !apply {
			return nil
		}
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
		return runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
			if err := db.ValidateDatabaseIdentity(ctx, tx); err != nil {
				return err
			}
			needed, err := inspect(tx)
			if err != nil {
				return err
			}
			if !needed {
				changed = false
				return nil
			}
			if err := validateMaterial(ctx, tx, codec); err != nil {
				return err
			}
			if err := convert(ctx, database, tx); err != nil {
				return err
			}
			version, err := readSchemaVersion(ctx, tx)
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
			return db.ValidateCurrentStorage(ctx, tx, codec)
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
	rows, err = q.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := rows.Next()
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if invalid {
		return errors.New("source reference check failed")
	}
	return nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
