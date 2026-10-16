package v1

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func stableSourceFixture(t *testing.T) (string, *secret.Vault, string) {
	t.Helper()
	path, vault := sourceFixtureFromDDL(t, schema17SQLForTest(t), 17, stableSchema17Manifest)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	user := hostileInsertUser(t, database, "stable-source", 0, 1)
	hostileInsertAccount(t, database, "user", user, nil, -1, hostileBlob16(123), 1)
	hostileMustExec(t, database, "UPDATE site_config SET value='9500' WHERE key='game_fishing_blue_fish_chance_bps'")
	hostileMustExec(t, database, "UPDATE site_config SET value='custom title' WHERE key='site_name'")
	keyContext, err := secret.NewGenerationTwoEndpointKeyContext(bytes.Repeat([]byte{0x41}, 16))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := vault.SealForGenerationTwoContext([]byte("synthetic saved credential"), keyContext)
	if err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, database, `INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,'https://synthetic.example/v1','openai-compatible',?,1)`, bytes.Repeat([]byte{0x41}, 16), sealed)
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, vault, sealed
}

func TestStableContentUpgradePreservesSavedStateAndReopens(t *testing.T) {
	path, vault, sealed := stableSourceFixture(t)
	for range 2 {
		result, err := Run(t.Context(), path, vault, Options{Apply: true})
		if err != nil {
			t.Fatal(result, err)
		}
		store, err := db.Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		d := store.DB()
		var version, sign int
		var saved, title, blue, milk string
		var magnitude []byte
		if err = d.QueryRow("SELECT version FROM schema_state").Scan(&version); err != nil || version != db.CurrentSchemaVersion {
			t.Fatal(version, err)
		}
		if err = d.QueryRow("SELECT encrypted_secret FROM endpoint_key_secrets").Scan(&saved); err != nil || saved != sealed {
			t.Fatal("credential changed", err)
		}
		if err = d.QueryRow("SELECT balance_sign,balance_mag FROM credit_accounts WHERE kind='user'").Scan(&sign, &magnitude); err != nil || sign != -1 || !bytes.Equal(magnitude, hostileBlob16(123)) {
			t.Fatal("balance changed", err)
		}
		if err = d.QueryRow("SELECT value FROM site_config WHERE key='site_name'").Scan(&title); err != nil || title != "custom title" {
			t.Fatal(title, err)
		}
		if err = d.QueryRow("SELECT value FROM site_config WHERE key='game_fishing_blue_fish_chance_bps'").Scan(&blue); err != nil || blue != "9500" {
			t.Fatal(blue, err)
		}
		if err = d.QueryRow("SELECT value FROM site_config WHERE key='game_fishing_milk_crab_chance_bps'").Scan(&milk); err != nil || milk != "500" {
			t.Fatal(milk, err)
		}
		if err = validateGenerationTwoManifest(t.Context(), d); err != nil {
			t.Fatal(err)
		}
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.VerifyContext(t.Context(), path, vault, nil); err != nil {
		t.Fatal(err)
	}
}

func TestStableContentUpgradeCancellationRollsBackBothChanges(t *testing.T) {
	d := openSchema17DDLForTest(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := runGenerationTwoExtension(ctx, d, func(ctx context.Context, tx *sql.Tx) error {
		if err := extendContentAndProtocols(ctx, tx); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	plan, err := storageSchema.plan(t.Context(), d)
	if err != nil || len(plan.steps) != 2 || plan.steps[0].from != 17 {
		t.Fatal("source not restored", err)
	}
	var count int
	if err = d.QueryRow("SELECT count(*) FROM site_config WHERE key='game_fishing_milk_crab_chance_bps'").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial config committed", err)
	}
	if err = extendKnownGenerationTwoSchema(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if err = validateGenerationTwoManifest(t.Context(), d); err != nil {
		t.Fatal(err)
	}
}

func TestStableContentUpgradeRejectsDriftBeforeWriting(t *testing.T) {
	path, vault, _ := stableSourceFixture(t)
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, d, "CREATE INDEX unexpected_stable_index ON users(username)")
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(path, vault)
	if store != nil {
		store.Close()
		t.Fatal("drifted source accepted")
	}
	var startup *db.StartupError
	if !errors.As(err, &startup) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejection changed source", err)
	}
}
