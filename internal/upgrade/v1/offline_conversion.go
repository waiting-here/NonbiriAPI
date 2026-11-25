package v1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// operatorMessage contains only application-owned descriptions. SQLite and
// codec errors can contain stored values or paths and must not reach the CLI.
type operatorMessage string

func (m operatorMessage) Error() string { return string(m) }

func operationFailure(ctx context.Context, topic string, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, db.ErrDatabaseInUse} {
		if errors.Is(err, known) {
			return known
		}
	}
	var startup *db.StartupError
	if errors.As(err, &startup) {
		return startup
	}
	var safe operatorMessage
	if errors.As(err, &safe) {
		return safe
	}
	return operatorMessage("offline upgrade: " + topic + " failed; preserve the database and check a matching recovery copy")
}

func offlineStep(ctx context.Context, phase string, version int, table, topic string, work func() error) error {
	return operationFailure(ctx, topic, progressStep(ctx, phase, version, table, work))
}

// The version marker and the full manifest must agree before any write.
func inspectVersion(ctx context.Context, q queryer) (int, error) {
	var version int
	if err := q.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1").Scan(&version); err != nil {
		return 0, err
	}
	want := db.PinnedGenerationTwoManifestHash
	if version != db.CurrentSchemaVersion {
		want = ""
		for _, source := range supportedSources {
			if source.SourceSchemaVersion == version {
				want = source.SourceManifestHash
				break
			}
		}
		if want == "" {
			return 0, &db.StartupError{Kind: db.StartupSchemaMismatch, ExpectedSchemaVersion: db.CurrentSchemaVersion, ActualSchemaVersion: &version}
		}
	}
	hash, err := db.SchemaManifestHash(ctx, q)
	if err != nil {
		return 0, err
	}
	if hash != want {
		return 0, operatorMessage(fmt.Sprintf("schema %d: database structure differs from the released schema; preserve the database and use a matching recovery set", version))
	}
	return version, nil
}

func convertOffline(ctx context.Context, path string, codec secret.GenerationTwoContextCodec, apply bool) (int, error) {
	var source int
	err := db.InspectContext(ctx, path, codec, func(ctx context.Context, read *sql.DB) error {
		if err := offlineStep(ctx, "identity", 0, "", "source identity and configuration check", func() error {
			var err error
			source, err = inspectVersion(ctx, read)
			if err == nil {
				setProgressSource(ctx, source)
			}
			return err
		}); err != nil {
			return err
		}
		if !apply {
			if err := offlineStep(ctx, "audit", 0, "endpoint_key_secrets", "stored credential audit", func() error { return db.ValidateStoredEndpointCredentials(ctx, read, codec) }); err != nil {
				return err
			}
			if err := offlineStep(ctx, "audit", 0, "", "SQLite integrity and foreign key audit", func() error { return checkIntegrity(ctx, read) }); err != nil {
				return err
			}
			if err := offlineStep(ctx, "audit", 0, "credit_accounts", "ledger and reservation audit", func() error {
				tx, err := read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					return err
				}
				defer tx.Rollback()
				return ledger.ValidateRecovery(ctx, tx)
			}); err != nil {
				return err
			}
		}
		if source == db.CurrentSchemaVersion {
			return offlineStep(ctx, "target", 0, "", "target schema and configuration validation", func() error { return db.ValidateCurrentStorage(ctx, read, codec) })
		}
		if !apply {
			return operationFailure(ctx, "source configuration validation", db.ValidateStorageControls(ctx, read, codec))
		}
		// Release this SQLite reader while retaining the stopped-database lease.
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
		u := url.URL{Scheme: "file", Path: slash}
		database, err := sql.Open("sqlite", u.String()+"?mode=rw&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=locking_mode(EXCLUSIVE)&_txlock=exclusive")
		if err != nil {
			return err
		}
		database.SetMaxOpenConns(1)
		defer database.Close()
		return applyUpgrade(ctx, database, codec, source)
	})
	if err != nil {
		return 0, operationFailure(ctx, "database access", err)
	}
	return source, nil
}

func applyUpgrade(ctx context.Context, database *sql.DB, codec secret.GenerationTwoContextCodec, source int) (result error) {
	// Roll back synchronously before releasing the ownership lease on cancellation.
	tx, err := database.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return operationFailure(ctx, "exclusive database access", err)
	}
	committed := false
	defer func() {
		if !committed {
			finish := beginProgress(ctx, "rollback", 0, "")
			rollback := tx.Rollback()
			if errors.Is(rollback, sql.ErrTxDone) {
				rollback = nil
			}
			finish(rollback)
			if rollback != nil {
				result = operationFailure(ctx, "transaction rollback", rollback)
			}
		}
	}()
	if err := offlineStep(ctx, "identity", 0, "", "source identity and configuration check", func() error {
		if err := db.ValidateDatabaseIdentity(ctx, tx); err != nil {
			return err
		}
		version, err := inspectVersion(ctx, tx)
		if err != nil {
			return err
		}
		if version != source {
			return errors.New("conversion source changed before exclusive access")
		}
		return db.ValidateStorageControls(ctx, tx, codec)
	}); err != nil {
		return err
	}
	if source == SourceSchemaVersion {
		if err := migrateSchema54(ctx, tx); err != nil {
			return err
		}
	}
	if source < 56 {
		if err := offlineStep(ctx, "migration", 56, "attendance_periods", "attendance presentation", func() error {
			_, err := tx.ExecContext(ctx, db.AttendancePresentationSQL)
			return err
		}); err != nil {
			return err
		}
	}
	if source < 57 {
		if err := offlineStep(ctx, "migration", 57, "dispatch_caller_parameter_errors", "Google caller error receipts", func() error {
			_, err := tx.ExecContext(ctx, db.GoogleCallerParameterSQL)
			return err
		}); err != nil {
			return err
		}
		if err := offlineStep(ctx, "migration", 57, "charity_reservations", "fractional discount snapshots", func() error {
			_, err := tx.ExecContext(ctx, db.CharityDiscountPrecisionSQL)
			return err
		}); err != nil {
			return err
		}
		if err := offlineStep(ctx, "migration", 57, "whale_haven_settings", "Whale Haven visit reward rate", func() error {
			return updateVisitRewardRate(ctx, tx, time.Now().UnixMilli())
		}); err != nil {
			return err
		}
	}
	if err := offlineStep(ctx, "migration", 58, "request_logs", "caller error log index", func() error {
		_, err := tx.ExecContext(ctx, db.RequestLogCallerErrorIndexSQL)
		return err
	}); err != nil {
		return err
	}
	if err := offlineStep(ctx, "migration", db.CurrentSchemaVersion, "schema_state", "schema version update", func() error {
		updated, err := tx.ExecContext(ctx, "UPDATE schema_state SET version=? WHERE id=1 AND version=?", db.CurrentSchemaVersion, source)
		if err != nil {
			return err
		}
		if count, err := updated.RowsAffected(); err != nil || count != 1 {
			return errors.New("schema version changed during conversion")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := offlineStep(ctx, "target", 0, "", "target schema and configuration validation", func() error { return db.ValidateCurrentStorage(ctx, tx, codec) }); err != nil {
		return err
	}
	return offlineStep(ctx, "commit", 0, "", "transaction commit", func() error {
		err := tx.Commit()
		committed = err == nil
		return err
	})
}

// Append a prospective correction to the old defaults without rewriting
// historical meals or changing a site's customized reward or meal size.
func updateVisitRewardRate(ctx context.Context, tx *sql.Tx, at int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO whale_haven_settings(revision,effective_at,document)
		SELECT revision+1,MAX(effective_at,?),json_set(document,'$.values.visit_grams_per_pearl',28)
		FROM (SELECT revision,effective_at,document FROM whale_haven_settings ORDER BY revision DESC LIMIT 1)
		WHERE json_extract(document,'$.values.guest_full_grams')=28
		AND json_extract(document,'$.values.visit_grams_per_pearl')=35`, at)
	return err
}

func migrateSchema54(ctx context.Context, tx *sql.Tx) error {
	if err := offlineStep(ctx, "migration", 55, "whale_haven_profiles", "create Whale Haven wallets and houses", func() error {
		_, err := tx.ExecContext(ctx, db.WhaleHavenSQL)
		return err
	}); err != nil {
		return err
	}
	if err := offlineStep(ctx, "migration", 55, "donation_model_quotas", "model quota conversion", func() error {
		if _, err := tx.ExecContext(ctx, db.ModelConsumptionSQL+db.DonationExpiryIndexSQL+db.GoogleModelCooldownSQL); err != nil {
			return err
		}
		return backfillModelConsumption(ctx, tx)
	}); err != nil {
		return err
	}
	if err := offlineStep(ctx, "migration", 55, "request_attempts", "attempt health conversion", func() error {
		if _, err := tx.ExecContext(ctx, db.AttemptHealthSQL); err != nil {
			return err
		}
		if err := backfillAttemptStreaks(ctx, tx); err != nil {
			return err
		}
		return backfillAttemptBackoffs(ctx, tx)
	}); err != nil {
		return err
	}
	return nil
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

func checkForeignKeys(ctx context.Context, q queryer, query string) error {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	invalid := rows.Next()
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if invalid {
		return errors.New("schema migration left invalid references")
	}
	return nil
}
