package dbtest

import (
	"database/sql"
	"testing"
)

// RestoreSchema54Fixture reconstructs the released layout around synthetic
// in-flight work, so upgrade and settlement tests exercise the same source.
func RestoreSchema54Fixture(t *testing.T, source *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		"DROP TABLE request_attempt_backoffs",
		"ALTER TABLE request_attempts DROP COLUMN streak_after",
		"ALTER TABLE request_attempts DROP COLUMN streak_before",
		"ALTER TABLE request_attempts DROP COLUMN streak_effect",
		"DROP TRIGGER google_model_target_update",
		"DROP TRIGGER google_model_target_delete",
		"DROP TABLE donation_service_model_cooldowns",
		"DROP INDEX idx_retry_pending_health",
		"ALTER TABLE dispatch_retry_after_observations DROP COLUMN pending_health_id",
		"DROP INDEX idx_donation_keys_due_expiry",
	} {
		if _, err := source.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, column := range []string{"historical_costs_partial", "price_reserved_mag", "calls_reserved", "price_used_mag", "calls_used", "price_limit_mag", "call_limit_mag"} {
		if _, err := source.ExecContext(t.Context(), "ALTER TABLE donation_model_quotas DROP COLUMN "+column); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := source.ExecContext(t.Context(), "UPDATE schema_state SET version=54"); err != nil {
		t.Fatal(err)
	}
}
