package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type activeRecoveryKey struct{}
type verificationAuditKey struct{}

// ActiveRecoveryContext limits validation to current state and unfinished work.
// Historical validators retain their full scope when this context is absent.
func ActiveRecoveryContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, activeRecoveryKey{}, true)
}

func IsActiveRecovery(ctx context.Context) bool {
	active, _ := ctx.Value(activeRecoveryKey{}).(bool)
	return active
}

// InspectContext owns a stopped database or trusted consistent copy while its
// read-only callback inspects it. It validates path, storage identity and key.
// SQLite may create WAL/shared-memory files for read-lock coordination.
func InspectContext(ctx context.Context, path string, secrets secret.GenerationTwoContextCodec, inspect func(context.Context, *sql.DB) error) (result error) {
	if ctx == nil || nilSecretCodec(secrets) {
		return errors.New("verification dependencies are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, activeRecoveryKey{}, false)
	owner, err := acquireDatabaseOwner(path)
	if err != nil {
		return err
	}
	defer owner.releasePreflight()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return startupError(StartupInvalidHeader)
	}
	if err := inspectDBParentPathComponents(filepath.Dir(path)); err != nil {
		return startupError(StartupUnsafePath)
	}
	source, err := captureSourceSet(ActiveRecoveryContext(ctx), path)
	if err != nil {
		return err
	}
	defer func() { result = appendStartupError(result, source.close()) }()
	if source.journal != nil {
		return startupError(StartupRollbackJournal)
	}
	if err := validateHeader(source.main); err != nil {
		return err
	}
	if err := source.close(); err != nil {
		return err
	}
	d, err := openSQLiteContext(ctx, path, "ro")
	if err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	owned := sourceStore(context.WithValue(ctx, databaseOwnerKey{}, owner), d, secrets)
	// A cancelled audit must finish closing SQLite before a caller can retry.
	// Close has its own bounded cleanup budget independent of the audit deadline.
	defer func() { result = appendStartupError(result, owned.Close()) }()
	if _, err := d.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	if err := ValidateDatabaseIdentity(ctx, d); err != nil {
		return err
	}
	if err := validateDatabaseMaterial(ctx, d, secrets); err != nil {
		return startupSQLFailure(ctx, err, StartupCredentialReject)
	}
	if inspect != nil {
		return inspect(ctx, d)
	}
	return nil
}

func validateCurrentSource(ctx context.Context, path string, source *sourceSnapshotSet, secrets secret.GenerationTwoContextCodec) (result error) {
	if err := validateHeader(source.main); err != nil {
		return err
	}
	if err := source.close(); err != nil {
		return err
	}
	RecordStartupStage(ctx, StageRawHandlesClosed)
	if err := ctx.Err(); err != nil {
		return err
	}
	RecordStartupStage(ctx, StageIdentityValidation)
	if err := ctx.Err(); err != nil {
		return err
	}
	d, err := openSQLiteContext(ctx, path, "ro")
	if err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	owner, _ := ctx.Value(databaseOwnerKey{}).(*databaseOwner)
	owner.activate()
	readonly := &Store{db: d, afterClose: func() error {
		databaseOwners.Lock()
		owner.active = false
		databaseOwners.Unlock()
		if ctx.Err() != nil {
			owner.releasePreflight()
		}
		return nil
	}}
	defer func() { result = appendStartupError(result, readonly.CloseContext(ctx)) }()
	if _, err := d.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	var applicationID, userVersion uint32
	if err := d.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	if applicationID != DatabaseApplicationID {
		return startupError(StartupWrongIdentity)
	}
	if err := d.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	if userVersion != DatabaseUserVersion {
		return generationError(userVersion)
	}
	RecordStartupStage(ctx, StageSchemaValidation)
	if err := validateGenerationTwoManifestSource(ctx, d, true); err != nil {
		return startupSQLFailure(ctx, err, StartupSchemaMismatch)
	}
	if err := validateStartupSeed(ctx, d); err != nil {
		return startupSQLFailure(ctx, err, StartupSchemaMismatch)
	}
	if err := validateSourceConfig(ctx, d); err != nil {
		return startupSQLFailure(ctx, err, StartupSchemaMismatch)
	}
	RecordStartupStage(ctx, StageCredentialValidation)
	if err := validateDatabaseMaterial(ctx, d, secrets); err != nil {
		return startupSQLFailure(ctx, err, StartupCredentialReject)
	}
	return nil
}

// validateStartupSeed reads only fixed identities and singleton control rows.
// Closed pools and dynamic game accounts are audited by maintenance verify.
func validateStartupSeed(ctx context.Context, q queryer) error {
	expected := expectedGenerationTwoSeedManifest()
	for _, account := range expected.FixedAccounts {
		var count int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM credit_accounts WHERE code=? AND kind=? AND asset_type=? AND user_id IS NULL`, account.Code, account.Kind, account.Asset).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("fixed account identity mismatch")
		}
	}
	for _, table := range expected.SingletonRows {
		var count int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM `+quoteSQLiteIdentifier(table)+` WHERE id=1`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("required control row missing")
		}
	}
	for _, domain := range expected.ConfigRevisionDomains {
		var count int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM config_revisions WHERE domain=?`, domain).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("required configuration revision missing")
		}
	}
	var welfare, thursday bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM shared_pools p JOIN credit_accounts a ON a.id=p.account_id WHERE p.pool_type='welfare' AND p.period_id IS NULL AND a.kind='pool' AND a.code='pool:'||p.id),EXISTS(SELECT 1 FROM shared_pools WHERE pool_type='thursday')`).Scan(&welfare, &thursday); err != nil {
		return err
	}
	if !welfare || !thursday {
		return errors.New("required shared pool identity missing")
	}
	var initialized int
	if err := q.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM gateway_model_capabilities_state WHERE singleton=1) AND (SELECT revision<>zeroblob(16) FROM site_usage_totals WHERE id=1)").Scan(&initialized); err != nil || initialized != 1 {
		return errors.New("stable storage initialization marker missing")
	}
	return nil
}

// VerifyContext performs the complete current-schema audit without repairs.
func VerifyContext(ctx context.Context, path string, codec secret.GenerationTwoContextCodec, audit func(context.Context, *sql.DB) error) error {
	return InspectContext(ctx, path, codec, func(ctx context.Context, d *sql.DB) error {
		if audit != nil {
			ctx = context.WithValue(ctx, verificationAuditKey{}, audit)
		}
		return validateReadOnlyDatabase(ctx, d, codec)
	})
}

// ValidateDatabaseIdentity checks the SQLite application and generation markers.
func ValidateDatabaseIdentity(ctx context.Context, q queryer) error {
	var applicationID, userVersion uint32
	if err := q.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); err != nil {
		return startupSQLFailure(ctx, err, StartupReadIO)
	}
	if applicationID != DatabaseApplicationID {
		return startupError(StartupWrongIdentity)
	}
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		return startupSQLFailure(ctx, err, StartupReadIO)
	}
	if userVersion != DatabaseUserVersion {
		return generationError(userVersion)
	}
	return nil
}

// ValidateCurrentStorage checks final structure, configuration and key-bound
// control material. Full historical credential and data audits are separate.
func ValidateCurrentStorage(ctx context.Context, q queryer, codec secret.GenerationTwoContextCodec) error {
	if err := validateGenerationTwoManifest(ctx, q); err != nil {
		return err
	}
	if err := validateStartupSeed(ctx, q); err != nil {
		return err
	}
	if err := validateSourceConfig(ctx, q); err != nil {
		return err
	}
	return validateDatabaseMaterial(ctx, q, codec)
}
