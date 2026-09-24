package db

import (
	"context"
	"database/sql"
)

const limitedActivitySchema = `
CREATE TABLE limited_activity_configs (
 activity_key TEXT NOT NULL PRIMARY KEY CHECK(length(activity_key) BETWEEN 1 AND 64 AND activity_key NOT GLOB '*[^a-z0-9-]*'),
 visible INTEGER NOT NULL CHECK(visible IN (0,1)),
 starts_at INTEGER CHECK(starts_at IS NULL OR starts_at BETWEEN 0 AND 253402300799),
 ends_at INTEGER CHECK(ends_at IS NULL OR ends_at BETWEEN 0 AND 253402300799),
 paused INTEGER NOT NULL CHECK(paused IN (0,1)),
 module_config TEXT NOT NULL CHECK(typeof(module_config)='text' AND length(CAST(module_config AS BLOB)) BETWEEN 2 AND 65536 AND json_valid(module_config)),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 CHECK((starts_at IS NULL AND ends_at IS NULL) OR (starts_at IS NOT NULL AND ends_at IS NOT NULL AND starts_at<ends_at))
) STRICT;
CREATE TABLE limited_activity_revisions (
 activity_key TEXT NOT NULL REFERENCES limited_activity_configs(activity_key),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 visible INTEGER NOT NULL CHECK(visible IN (0,1)),
 starts_at INTEGER CHECK(starts_at IS NULL OR starts_at BETWEEN 0 AND 253402300799),
 ends_at INTEGER CHECK(ends_at IS NULL OR ends_at BETWEEN 0 AND 253402300799),
 paused INTEGER NOT NULL CHECK(paused IN (0,1)),
 module_config TEXT NOT NULL CHECK(typeof(module_config)='text' AND length(CAST(module_config AS BLOB)) BETWEEN 2 AND 65536 AND json_valid(module_config)),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(activity_key,revision),
 CHECK((starts_at IS NULL AND ends_at IS NULL) OR (starts_at IS NOT NULL AND ends_at IS NOT NULL AND starts_at<ends_at))
) STRICT;
CREATE TRIGGER limited_activity_revisions_immutable BEFORE UPDATE ON limited_activity_revisions
WHEN NEW.activity_key<>OLD.activity_key OR NEW.revision<>OLD.revision OR NEW.visible<>OLD.visible
 OR NEW.starts_at IS NOT OLD.starts_at OR NEW.ends_at IS NOT OLD.ends_at OR NEW.paused<>OLD.paused
 OR NEW.module_config<>OLD.module_config OR NEW.created_at<>OLD.created_at
 OR (NEW.actor_user_id IS NOT OLD.actor_user_id AND NOT (OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL))
BEGIN SELECT RAISE(ABORT,'limited activity revisions are immutable'); END;
CREATE TRIGGER limited_activity_revisions_no_delete BEFORE DELETE ON limited_activity_revisions
BEGIN SELECT RAISE(ABORT,'limited activity revisions are immutable'); END;
CREATE TABLE activity_exchange_state (
 activity_key TEXT NOT NULL REFERENCES limited_activity_configs(activity_key),
 asset_type TEXT NOT NULL CHECK(asset_type IN ('sketch_paper','sketch_brush')),
 total_exchanged_mag BLOB NOT NULL CHECK(typeof(total_exchanged_mag)='blob' AND length(total_exchanged_mag)=16),
 cap_mag BLOB CHECK(cap_mag IS NULL OR (typeof(cap_mag)='blob' AND length(cap_mag)=16)),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 PRIMARY KEY(activity_key,asset_type),
 CHECK((asset_type='sketch_paper' AND cap_mag IS NULL) OR (asset_type='sketch_brush' AND cap_mag IS NOT NULL))
) STRICT;
CREATE TRIGGER activity_exchange_state_monotone BEFORE UPDATE ON activity_exchange_state
WHEN NEW.activity_key<>OLD.activity_key OR NEW.asset_type<>OLD.asset_type OR NEW.total_exchanged_mag<OLD.total_exchanged_mag OR NEW.revision<=OLD.revision
BEGIN SELECT RAISE(ABORT,'invalid activity exchange state transition'); END;
CREATE TRIGGER activity_exchange_state_no_delete BEFORE DELETE ON activity_exchange_state
BEGIN SELECT RAISE(ABORT,'activity exchange totals are permanent'); END;
CREATE TABLE activity_exchange_receipts (
 operation_id TEXT NOT NULL PRIMARY KEY REFERENCES credit_operations(id),
 activity_key TEXT NOT NULL,
 config_revision INTEGER NOT NULL,
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 asset_type TEXT NOT NULL CHECK(asset_type IN ('sketch_paper','sketch_brush')),
 quantity_mag BLOB NOT NULL CHECK(typeof(quantity_mag)='blob' AND length(quantity_mag)=16 AND quantity_mag>X'00000000000000000000000000000000'),
 unit_price_milli INTEGER NOT NULL CHECK(unit_price_milli BETWEEN 1 AND 9000000000000000),
 cost_mag BLOB NOT NULL CHECK(typeof(cost_mag)='blob' AND length(cost_mag)=16 AND cost_mag>X'00000000000000000000000000000000' AND cost_mag<=X'7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF'),
 ledger_seq INTEGER NOT NULL CHECK(ledger_seq BETWEEN 1 AND 9223372036854775807),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 FOREIGN KEY(activity_key,config_revision) REFERENCES limited_activity_revisions(activity_key,revision)
) STRICT;
CREATE INDEX idx_activity_exchange_receipts_user_time ON activity_exchange_receipts(user_id,created_at,operation_id);
CREATE TRIGGER activity_exchange_receipts_immutable BEFORE UPDATE ON activity_exchange_receipts
WHEN NEW.operation_id<>OLD.operation_id OR NEW.activity_key<>OLD.activity_key OR NEW.config_revision<>OLD.config_revision
 OR NEW.asset_type<>OLD.asset_type OR NEW.quantity_mag<>OLD.quantity_mag OR NEW.unit_price_milli<>OLD.unit_price_milli
 OR NEW.cost_mag<>OLD.cost_mag OR NEW.ledger_seq<>OLD.ledger_seq OR NEW.created_at<>OLD.created_at
 OR (NEW.user_id IS NOT OLD.user_id AND NOT (OLD.user_id IS NOT NULL AND NEW.user_id IS NULL))
BEGIN SELECT RAISE(ABORT,'activity exchange receipts are immutable'); END;
CREATE TRIGGER activity_exchange_receipts_no_delete BEFORE DELETE ON activity_exchange_receipts
BEGIN SELECT RAISE(ABORT,'activity exchange receipts are permanent'); END;
`

func seedLimitedActivities(ctx context.Context, tx *sql.Tx, at int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO limited_activity_configs VALUES('picture-book',0,NULL,NULL,0,?,1,?);
 INSERT INTO limited_activity_revisions VALUES('picture-book',1,0,NULL,NULL,0,?,NULL,?);
 INSERT INTO activity_exchange_state VALUES('picture-book','sketch_paper',X'00000000000000000000000000000000',NULL,1);
 INSERT INTO activity_exchange_state VALUES('picture-book','sketch_brush',X'00000000000000000000000000000000',X'0000000000000000000000000000000A',1);`,
		`{"paper_price":"1000","brush_price":"10000","brush_cap":"10"}`, at,
		`{"paper_price":"1000","brush_price":"10000","brush_cap":"10"}`, at)
	return err
}
