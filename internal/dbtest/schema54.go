package dbtest

import (
	"database/sql"
	_ "embed"
	"testing"
)

// RestoreSchema54Fixture reconstructs the released layout around synthetic
// in-flight work, so upgrade and settlement tests exercise the same source.
func RestoreSchema54Fixture(t *testing.T, source *sql.DB) {
	t.Helper()
	RestoreSchema55Fixture(t, source)
	for _, statement := range []string{
		"DROP TRIGGER whale_master_insert",
		"DROP TRIGGER whale_master_update",
		"DROP TRIGGER whale_record_footprint",
		"DROP TABLE whale_haven_footprints",
		"DROP TABLE whale_haven_events",
		"DROP TABLE whale_haven_movements",
		"DROP TABLE whale_haven_assets",
		"DROP TABLE whale_haven_receipts",
		"DROP TABLE whale_haven_pins",
		"DROP TABLE whale_haven_daily",
		"DROP TABLE whale_haven_wage_fractions",
		"DROP TABLE whale_haven_visits",
		"DROP TABLE whale_haven_profiles",
		"DROP TABLE whale_haven_master_history",
		"DROP TABLE whale_haven_settings",
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

// RestoreSchema55Fixture removes only the optional attendance presentation.
func RestoreSchema55Fixture(t *testing.T, source *sql.DB) {
	t.Helper()
	RestoreSchema56Fixture(t, source)
	if _, err := source.ExecContext(t.Context(), `ALTER TABLE attendance_periods DROP COLUMN cover;
ALTER TABLE attendance_tiers DROP COLUMN featured;
UPDATE schema_state SET version=55`); err != nil {
		t.Fatal(err)
	}
}

//go:embed testdata/schema56_caller_errors.sql.txt
var schema56CallerErrors string

// RestoreSchema56Fixture reinstates the caller-error receipt DDL from v1.4.2.
func RestoreSchema56Fixture(t *testing.T, source *sql.DB) {
	t.Helper()
	if _, err := source.ExecContext(t.Context(), schema56CallerErrors+"ALTER TABLE charity_reservations DROP COLUMN discount_basis_points; UPDATE schema_state SET version=56"); err != nil {
		t.Fatal(err)
	}
}
