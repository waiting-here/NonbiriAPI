package db

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func makePreGatewayPolicyFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	makePreProgressionFixture(t, database)
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM pragma_table_info('donation_keys') WHERE name='failure_disable_threshold'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		return
	}
	if _, err := database.Exec(`ALTER TABLE donation_keys DROP COLUMN failure_disable_threshold; PRAGMA writable_schema=ON;`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mainstream_channels", "endpoints", "endpoint_key_secrets", "donation_keys", "request_attempts", "report_cases", "report_targets", "donation_reviews"} {
		var value string
		if err := database.QueryRow(`SELECT sql FROM sqlite_schema WHERE name=? AND type='table'`, name).Scan(&value); err != nil {
			t.Fatal(err)
		}
		value = strings.ReplaceAll(strings.ReplaceAll(value, gatewayConnectorConstraint, oldConnectorConstraint), failurePolicyReviewConstraint, oldFailureReviewConstraint)
		if _, err := database.Exec(`UPDATE sqlite_schema SET sql=? WHERE name=? AND type='table'`, value, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`PRAGMA writable_schema=RESET; DELETE FROM site_config WHERE key='gateway_user_attribution_enabled'`); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayPolicySchemaUpgradeAndDefaults(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(generationTwoWithoutGatewaySchema); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, preGatewayPolicyManifestHash)
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedGenerationTwo(ctx, tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM site_config WHERE key='gateway_user_attribution_enabled'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	user := hostileInsertUser(t, database, "policy-migration", 0, 100)
	endpoint := hostileInsertEndpoint(t, database, user, "https://upstream.example/v1")
	key := hostileInsertEndpointKey(t, database, endpoint, hostileInsertSecret(t, database, "https://upstream.example/v1", 100))
	donation := hostileInsertDonation(t, database, user)
	donated := hostileInsertDonationKey(t, database, donation, key)
	if _, err := database.Exec(`UPDATE donation_keys SET failure_streak=?,streak_generation=?,failure_disabled=1,enabled=0 WHERE id=?`, hostileBlob16(19), hostileBlob16(7), donated); err != nil {
		t.Fatal(err)
	}
	if err := extendKnownGenerationTwoSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	var streak, generation []byte
	var threshold string
	var disabled, enabled int
	if err := database.QueryRow(`SELECT failure_streak,streak_generation,failure_disable_threshold,failure_disabled,enabled FROM donation_keys WHERE id=?`, donated).Scan(&streak, &generation, &threshold, &disabled, &enabled); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(streak, hostileBlob16(19)) || !reflect.DeepEqual(generation, hostileBlob16(7)) || threshold != "10" || disabled != 1 || enabled != 0 {
		t.Fatal("migration changed existing policy state")
	}
	var value string
	if err := database.QueryRow(`SELECT value FROM site_config WHERE key='gateway_user_attribution_enabled'`).Scan(&value); err != nil || value != "0" {
		t.Fatal("attribution default", value, err)
	}
	if err := extendKnownGenerationTwoSchema(ctx, database); err != nil {
		t.Fatal("second startup", err)
	}
	for _, value := range []any{"", "01", "+1", "-1", "1.0", "1e1", " 1", "1 ", "1\x00", "１２", "340282366920938463463374607431768211456", strings.Repeat("1", 40), []byte("10"), nil} {
		if _, err := database.Exec(`UPDATE donation_keys SET failure_disable_threshold=?`, value); err == nil {
			t.Fatalf("accepted invalid threshold %q", value)
		}
	}
	for _, value := range []string{"0", "10", "340282366920938463463374607431768211455"} {
		if _, err := database.Exec(`UPDATE donation_keys SET failure_disable_threshold=?`, value); err != nil {
			t.Fatal(err)
		}
		var kind, retained string
		if err := database.QueryRow(`SELECT typeof(failure_disable_threshold),failure_disable_threshold FROM donation_keys WHERE id=?`, donated).Scan(&kind, &retained); err != nil || kind != "text" || retained != value {
			t.Fatal("threshold is not canonical decimal text", kind, retained, err)
		}
	}
}
