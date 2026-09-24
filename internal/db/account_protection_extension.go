package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Only the complete published governance schema can enter this extension.
const preAccountProtectionManifestHash = "06d17af44c7d0e44270d1169a3aa9da110c05f84f889b203ad37c5d2cb8ebd6c"

const accountProtectionSchema = `
CREATE TABLE discord_blacklist (
 discord_id TEXT NOT NULL PRIMARY KEY CHECK(length(discord_id) BETWEEN 1 AND 20 AND discord_id NOT GLOB '*[^0-9]*' AND substr(discord_id,1,1)<>'0' AND (length(discord_id)<20 OR discord_id<='18446744073709551615')),
 reason TEXT NOT NULL CHECK(length(reason) BETWEEN 1 AND 1024),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE admin_account_deletions (
 alert_id INTEGER NOT NULL PRIMARY KEY REFERENCES admin_alerts(id) ON DELETE CASCADE,
 snapshot_json TEXT NOT NULL CHECK(length(snapshot_json) BETWEEN 2 AND 8192 AND json_valid(snapshot_json) AND json_type(snapshot_json)='object')
) STRICT;
CREATE INDEX idx_admin_alerts_kind_resolved ON admin_alerts(kind,resolved,id DESC);
CREATE INDEX idx_admin_alerts_kind ON admin_alerts(kind,id DESC);
CREATE TRIGGER discord_blacklist_registration_guard BEFORE INSERT ON users
WHEN NEW.discord_id IS NOT NULL AND EXISTS(SELECT 1 FROM discord_blacklist WHERE discord_id=NEW.discord_id)
BEGIN SELECT RAISE(ABORT,'discord identity is blocked'); END;
CREATE TRIGGER discord_blacklist_user_guard BEFORE UPDATE OF discord_id,is_banned,banned_until ON users
WHEN EXISTS(SELECT 1 FROM discord_blacklist WHERE discord_id=NEW.discord_id) AND (NEW.is_banned<>1 OR NEW.banned_until IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'blacklisted account must remain permanently banned'); END;
CREATE TRIGGER discord_blacklist_insert_guard BEFORE INSERT ON discord_blacklist
WHEN EXISTS(SELECT 1 FROM users WHERE discord_id=NEW.discord_id AND (is_admin=1 OR is_banned<>1 OR banned_until IS NOT NULL))
BEGIN SELECT RAISE(ABORT,'existing account must be permanently banned first'); END;
CREATE TRIGGER discord_blacklist_identity_guard BEFORE UPDATE OF discord_id ON discord_blacklist
WHEN NEW.discord_id<>OLD.discord_id
BEGIN SELECT RAISE(ABORT,'blacklist identity is immutable'); END;
`

func accountProtectionBootstrapSchema(previous string) string {
	const before = "'worker_checkpoint_failed','invariant_violation'"
	if strings.Count(previous, before) != 1 {
		panic("unrecognized administrator alert constraint")
	}
	return strings.Replace(previous, before, before+",'account_deleted'", 1) + accountProtectionSchema
}

func applyAccountProtectionExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preAccountProtectionManifestHash {
		return errors.New("unrecognized account protection source manifest")
	}
	var before string
	if err = tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name='admin_alerts'`).Scan(&before); err != nil {
		return err
	}
	const match = "'worker_checkpoint_failed','invariant_violation'"
	if strings.Count(before, match) != 1 {
		return errors.New("unrecognized administrator alert constraint")
	}
	after := strings.Replace(before, match, match+",'account_deleted'", 1)
	var version int64
	if err = tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version >= 2147483647 {
		return errors.New("schema version cannot advance")
	}
	if _, err = tx.ExecContext(ctx, `PRAGMA writable_schema=ON`); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name='admin_alerts' AND sql=?`, after, before)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return errors.New("administrator alert schema update count mismatch")
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA schema_version=%d; PRAGMA writable_schema=RESET;", version+1)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, accountProtectionSchema)
	return err
}
