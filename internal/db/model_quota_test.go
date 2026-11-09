package db

import (
	"database/sql"
	"testing"
)

func restoreSchema49LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	restoreSchema50LayoutForTest(t, database)
	hostileMustExec(t, database, `DROP TABLE donation_model_quotas;
DROP INDEX idx_lake_exchange_daily_source;
DROP INDEX idx_donation_usage_model_reserved;
ALTER TABLE donation_usage_reservations DROP COLUMN upstream_model_id;
DROP INDEX idx_donation_quota_rules_scope;
DROP INDEX idx_donation_quota_rules_key_order;
ALTER TABLE donation_quota_rules DROP COLUMN upstream_model_id;
CREATE UNIQUE INDEX idx_donation_quota_rules_key_order ON donation_quota_rules(donation_key_id, display_order) WHERE current_epoch IS NOT NULL;`)
	assertRetainedManifest(t, database, Schema49ManifestHash)
}
