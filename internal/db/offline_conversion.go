package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// OfflineSchema identifies one exact source accepted by an external converter.
// It does not add that source to the normal startup migration registry.
type OfflineSchema struct {
	Version      int
	ManifestHash string
}

// ConvertOffline checks a stopped database, or converts it in one exclusive
// transaction. A false result means it already has the current stable schema.
// The caller owns the recovery snapshot; this function never creates a backup.
func ConvertOffline(ctx context.Context, path string, codec secret.GenerationTwoContextCodec, source OfflineSchema, apply bool, check func(context.Context, *sql.DB) error, convert func(context.Context, *sql.DB, *sql.Tx) error) (changed bool, result error) {
	if ctx == nil || nilSecretCodec(codec) || source.Version < 1 || source.ManifestHash == "" || convert == nil {
		return false, errors.New("offline conversion dependencies are required")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	owner, err := acquireDatabaseOwner(path)
	if err != nil {
		return false, err
	}
	defer owner.release()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false, startupError(StartupInvalidHeader)
	}
	if err := inspectDBParentPathComponents(filepath.Dir(path)); err != nil {
		return false, startupError(StartupUnsafePath)
	}
	files, err := captureSourceSet(ctx, path)
	if err != nil {
		return false, err
	}
	defer func() { result = errors.Join(result, files.close()) }()
	if files.journal != nil {
		return false, startupError(StartupRollbackJournal)
	}
	if err := validateHeader(files.main); err != nil {
		return false, err
	}
	if err := files.close(); err != nil {
		return false, err
	}
	inspect := func(q queryer) (bool, error) {
		var appID, userVersion uint32
		if err := q.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appID); err != nil {
			return false, startupSQLFailure(ctx, err, StartupReadIO)
		}
		if appID != DatabaseApplicationID {
			return false, startupError(StartupWrongIdentity)
		}
		if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
			return false, startupSQLFailure(ctx, err, StartupReadIO)
		}
		if userVersion != DatabaseUserVersion {
			return false, generationError(userVersion)
		}
		version, err := readSchemaVersion(ctx, q)
		if err != nil {
			return false, startupSQLFailure(ctx, err, StartupSchemaMismatch)
		}
		hash, err := SchemaManifestHash(ctx, q)
		if err != nil {
			return false, startupSQLFailure(ctx, err, StartupSchemaMismatch)
		}
		current := version == CurrentSchemaVersion && hash == PinnedGenerationTwoManifestHash
		if !current && (version != source.Version || hash != source.ManifestHash) {
			return false, startupError(StartupSchemaMismatch)
		}
		if err := validateDatabaseMaterial(ctx, q, codec); err != nil {
			return false, startupSQLFailure(ctx, err, StartupCredentialReject)
		}
		return !current, nil
	}
	read, err := openSQLiteContext(ctx, path, "ro")
	if err != nil {
		return false, startupSQLFailure(ctx, err, StartupReadIO)
	}
	if _, err = read.ExecContext(ctx, "PRAGMA query_only=ON"); err == nil {
		changed, err = inspect(read)
	}
	if err == nil && changed {
		err = validateEndpointKeyEnvelopes(ctx, read, codec)
	}
	if err == nil && changed && check != nil {
		err = check(ctx, read)
	}
	if closeErr := read.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	if err != nil {
		return false, preserveStartupCategory(err, StartupCredentialReject)
	}
	if !apply || !changed {
		return changed, nil
	}
	dsn, err := sqliteFileURI(path, "rw")
	if err != nil {
		return false, err
	}
	database, err := sql.Open("sqlite", dsn+"&_pragma=locking_mode(EXCLUSIVE)&_txlock=exclusive")
	if err != nil {
		return false, err
	}
	database.SetMaxOpenConns(1)
	defer func() { result = errors.Join(result, database.Close()) }()
	err = runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
		needed, err := inspect(tx)
		changed = needed
		if err != nil || !needed {
			return err
		}
		if err := convert(ctx, database, tx); err != nil {
			return err
		}
		plan, err := storageSchema.plan(ctx, tx)
		if err != nil {
			return err
		}
		if err := plan.apply(ctx, tx); err != nil {
			return err
		}
		if err := validateGenerationTwoManifest(ctx, tx); err != nil {
			return err
		}
		if err := validateStartupSeed(ctx, tx); err != nil {
			return err
		}
		if err := validateSourceConfig(ctx, tx); err != nil {
			return err
		}
		return validateDatabaseMaterial(ctx, tx, codec)
	})
	if err != nil {
		return false, preserveStartupCategory(err, StartupSchemaMismatch)
	}
	return changed, nil
}
