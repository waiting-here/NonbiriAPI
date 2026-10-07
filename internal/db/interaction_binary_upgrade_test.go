package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	lakeRules "github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// The release gate supplies a populated database created by the supported
// released binary. An isolated consistent copy can use the same verifier.
func TestUpgradeFromReleasedBinary(t *testing.T) {
	source := os.Getenv("NONBIRI_UPGRADE_FIXTURE")
	if source == "" {
		t.Skip("released-source gate supplies a consistent fixture")
	}
	verifyReleasedStorageUpgrade(t, source, preLedgerRetentionManifestHash)
}

// A consistent versioned source exercises the populated upgrade directly.
func TestUpgradeFromVersionedSource(t *testing.T) {
	source := os.Getenv("NONBIRI_VERSIONED_FIXTURE")
	if source == "" {
		t.Skip("consistent versioned fixture not supplied")
	}
	expected := terminalReservationIndexesManifestHash
	switch os.Getenv("NONBIRI_FIXTURE_SCHEMA_VERSION") {
	case "1":
		expected = baselineManifestHash
	case "2":
	case "3":
		expected = aiPlayersManifestHash
	case "4":
		expected = managementAndGamesManifestHash
	default:
		t.Fatal("exact source schema version must be supplied")
	}
	verifyReleasedStorageUpgrade(t, source, expected)
}

func verifyReleasedStorageUpgrade(t *testing.T, source, expectedSourceManifest string) {
	t.Helper()
	startupBudget := DefaultStartupTimeout
	if configured := os.Getenv("NONBIRI_UPGRADE_STARTUP_TIMEOUT"); configured != "" {
		var err error
		startupBudget, err = time.ParseDuration(configured)
		if err != nil || startupBudget <= 0 {
			t.Fatal("invalid isolated upgrade startup timeout")
		}
	}
	key := bytes.Repeat([]byte{0x42}, secret.MasterKeyBytes)
	if file := os.Getenv("NONBIRI_UPGRADE_MASTER_KEY_FILE"); file != "" {
		encoded, err := os.ReadFile(file)
		if err != nil {
			t.Fatal("cannot read isolated master key")
		}
		key, err = hex.DecodeString(strings.TrimSpace(string(encoded)))
		clear(encoded)
		if err != nil || len(key) != secret.MasterKeyBytes {
			t.Fatal("invalid isolated master key encoding")
		}
	}
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal("cannot initialize isolated vault")
	}
	defer vault.Close()
	path := bootstrapTestPath(t, "released-interaction.sqlite")
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		input.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	inputErr, outputErr := input.Close(), output.Close()
	if copyErr != nil || inputErr != nil || outputErr != nil {
		t.Fatal("isolated copy failed", copyErr, inputErr, outputErr)
	}
	ctx := context.Background()
	prior, err := openSQLite(path, "ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prior.Close() })
	sourceManifest, err := readGenerationManifest(ctx, prior)
	if err != nil {
		prior.Close()
		t.Fatal(err)
	}
	assertRetainedManifest(t, prior, expectedSourceManifest)
	before := interactionTableDigests(t, prior, sourceManifest, false)
	wantConfig := upgradeSiteConfig(t, prior)
	preserveManagementConfig := expectedSourceManifest == managementAndGamesManifestHash
	var biddingSettings *[2]int64
	for _, table := range sourceManifest.Tables {
		if table.Name == "game_ai_settings" {
			biddingSettings = &[2]int64{}
			if err := prior.QueryRow("SELECT enabled,revision FROM game_ai_settings WHERE game_key='bidding'").Scan(&biddingSettings[0], &biddingSettings[1]); err != nil {
				t.Fatal("source Bidding AI settings", err)
			}
		}
	}
	var wantSequences map[string]int64
	if preserveManagementConfig {
		wantSequences = upgradeDuelSequences(t, prior)
	}
	var lakeOpen bool
	if err := prior.QueryRow(`SELECT EXISTS(SELECT 1 FROM limited_activity_configs WHERE activity_key='lake-notes' AND visible=1 AND paused=0)`).Scan(&lakeOpen); err != nil {
		t.Fatal(err)
	}
	if !preserveManagementConfig {
		for key, value := range map[string]string{
			"global_rpm_per_user":      "60",
			"game_steadycatch_enabled": "0", "game_steadycatch_price_milli": "0", "game_steadycatch_first_reward_milli": "0",
			"game_gwent_enabled": "0", "game_gwent_standard_enabled": "0", "game_gwent_standard_ticket_milli": "5000000",
			"game_gwent_standard_rake_platform_bp": "100", "game_gwent_standard_rake_welfare_bp": "100", "game_gwent_standard_rake_thursday_bp": "100",
			"game_lakenotes_enabled":   "0",
			"game_lakenotes_exchanges": `{"coins_to_game":{"enabled":false,"source_amount":"","target_amount":""},"coins_to_general":{"enabled":false,"source_amount":"","target_amount":""},"game_to_coins":{"enabled":false,"source_amount":"","target_amount":""},"general_to_coins":{"enabled":false,"source_amount":"","target_amount":""}}`,
		} {
			wantConfig[key] = upgradeSetting{Value: value}
		}
		if lakeOpen {
			wantConfig["game_lakenotes_enabled"] = upgradeSetting{Value: "1"}
			master := wantConfig["games_enabled"]
			master.Value = "1"
			wantConfig["games_enabled"] = master
		}
	}
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := Open(bootstrapTestPath(t, "fresh-interaction.sqlite"), vault)
	if err != nil {
		t.Fatal(err)
	}
	freshManifest, err := readGenerationManifest(ctx, fresh.DB())
	if err != nil {
		fresh.Close()
		t.Fatal(err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		trace := NewStartupTrace(time.Now(), func(progress StartupProgress) {
			t.Logf("open=%d stage=%s elapsed_ms=%d", attempt+1, progress.Stage, progress.ElapsedMS)
		})
		startup, cancel := context.WithTimeout(ctx, startupBudget)
		store, err := OpenContext(trace.Context(startup), path, vault)
		cancel()
		if err != nil {
			t.Fatalf("released upgrade/open failed (budget=%s stage=%s): %v", startupBudget, trace.Snapshot().Stage, err)
		}
		t.Cleanup(func() { _ = store.Close() })
		database := store.DB()
		manifest, err := readGenerationManifest(ctx, database)
		if err != nil || !reflect.DeepEqual(manifest, freshManifest) {
			store.Close()
			t.Fatal("fresh and upgraded schema differ", err)
		}
		if attempt == 0 {
			after := interactionTableDigests(t, database, sourceManifest, true)
			for table, want := range before {
				if table == "schema_state" || table == "site_config" {
					continue
				} // Configuration has explicit migration expectations below.
				if after[table] != want {
					t.Errorf("retained source columns changed in %s (rows %d -> %d)", table, want.Rows, after[table].Rows)
				}
			}
			if got := upgradeSiteConfig(t, database); !reflect.DeepEqual(got, wantConfig) {
				t.Errorf("configuration differs from preserved source plus declared game defaults (rows %d, want %d)", len(got), len(wantConfig))
			}
		}
		if biddingSettings != nil {
			var got [2]int64
			if err := database.QueryRow("SELECT enabled,revision FROM game_ai_settings WHERE game_key='bidding'").Scan(&got[0], &got[1]); err != nil || got != *biddingSettings {
				t.Fatal("retained Bidding AI settings changed", got, biddingSettings, err)
			}
		}
		var gwentSettings [2]int64
		if err := database.QueryRow("SELECT enabled,revision FROM game_ai_settings WHERE game_key='gwent'").Scan(&gwentSettings[0], &gwentSettings[1]); err != nil || gwentSettings != [2]int64{0, 1} {
			t.Fatal("new Gwent AI setting differs from its disabled default", gwentSettings, err)
		}
		if wantSequences != nil {
			if got := upgradeDuelSequences(t, database); !reflect.DeepEqual(got, wantSequences) {
				t.Fatal("duel/AI queue sequence counters changed", got, wantSequences)
			}
		}
		if !preserveManagementConfig {
			for _, table := range []string{"lake_notes_profiles", "lake_notes_casts"} {
				var unknown int
				if err := database.QueryRow("SELECT count(*) FROM " + table + " WHERE storage_version<>1").Scan(&unknown); err != nil || unknown != 0 {
					t.Fatal("saved Lake Notes format was not initialized", table, unknown, err)
				}
			}
		}
		rows, err := database.Query(`SELECT context_id,encrypted_secret FROM endpoint_key_secrets`)
		if err != nil {
			store.Close()
			t.Fatal(err)
		}
		credentials := 0
		for rows.Next() {
			var contextID []byte
			var envelope string
			if err := rows.Scan(&contextID, &envelope); err != nil {
				t.Fatal("credential projection failed")
			}
			keyContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
			if err != nil {
				t.Fatal("credential context invalid")
			}
			plain, err := vault.OpenForGenerationTwoContext(envelope, keyContext)
			valid := err == nil && len(plain) > 0
			clear(plain)
			if !valid {
				t.Fatal("retained credential did not decrypt")
			}
			credentials++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if credentials == 0 {
			t.Fatal("source contains no credential preservation evidence")
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("Preserved %d source table projections; fresh schema, credential decryption and reopen verified", len(before))
}

type upgradeSetting struct {
	Value     string
	UpdatedAt int64
}

func upgradeSiteConfig(t *testing.T, database *sql.DB) map[string]upgradeSetting {
	t.Helper()
	rows, err := database.Query(`SELECT key,value,updated_at FROM site_config`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	values := make(map[string]upgradeSetting)
	for rows.Next() {
		var key string
		var value upgradeSetting
		if err := rows.Scan(&key, &value.Value, &value.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

type interactionTableDigest struct {
	Rows int64
	Sum  [sha256.Size]byte
	XOR  [sha256.Size]byte
}

// Order-independent digests keep the real-copy check bounded in memory. Column
// types participate in each row hash; row counts, sum and XOR preserve duplicates.
func interactionTableDigests(t *testing.T, database *sql.DB, manifest generationManifest, excludeNewGwentSetting ...bool) map[string]interactionTableDigest {
	t.Helper()
	out := make(map[string]interactionTableDigest)
	for _, table := range manifest.Tables {
		if strings.HasPrefix(table.Name, "sqlite_") {
			continue
		}
		columns := make([]string, len(table.Columns))
		for i, column := range table.Columns {
			columns[i] = hostileQuoteIdent(column.Name)
		}
		query := "SELECT " + strings.Join(columns, ",") + " FROM " + hostileQuoteIdent(table.Name)
		if table.Name == "game_ai_settings" && len(excludeNewGwentSetting) > 0 && excludeNewGwentSetting[0] {
			query += " WHERE game_key<>'gwent'"
		}
		rows, err := database.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		var digest interactionTableDigest
		for rows.Next() {
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			types := make([]string, len(values))
			for i, value := range values {
				if value != nil {
					types[i] = reflect.TypeOf(value).String()
				}
			}
			encoded, err := json.Marshal([]any{types, values})
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(encoded)
			carry := uint16(0)
			for i := sha256.Size - 1; i >= 0; i-- {
				carry += uint16(digest.Sum[i]) + uint16(hash[i])
				digest.Sum[i] = byte(carry)
				carry >>= 8
				digest.XOR[i] ^= hash[i]
			}
			digest.Rows++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		out[table.Name] = digest
	}
	return out
}

func upgradeDuelSequences(t *testing.T, database *sql.DB) map[string]int64 {
	t.Helper()
	rows, err := database.Query("SELECT name,seq FROM sqlite_sequence WHERE name IN ('game_duel_sessions','game_duel_anonymous','game_ai_queue')")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	values := map[string]int64{}
	for rows.Next() {
		var name string
		var sequence int64
		if err := rows.Scan(&name, &sequence); err != nil {
			t.Fatal(err)
		}
		values[name] = sequence
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestManagementSourceUpgradePreservesConfiguredGamesAndLakeV2(t *testing.T) {
	t.Setenv("NONBIRI_UPGRADE_MASTER_KEY_FILE", "")
	vault, err := secret.New(bytes.Repeat([]byte{0x42}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := bootstrapTestPath(t, "management-source.sqlite")
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	database := store.DB()
	user := hostileInsertUser(t, database, "saved-game", 0, testNow)
	profile := lakeRules.InitialProfile()
	profile, cast, err := lakeRules.Start(profile, lakeRules.CryptoRandom{Reader: bytes.NewReader(make([]byte, 1024))}, 123)
	if err != nil {
		t.Fatal(err)
	}
	cast.Paused = true
	profileRaw, err := lakeRules.EncodeProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	castRaw, err := lakeRules.EncodeCast(cast)
	if err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, database, "INSERT INTO lake_notes_profiles(user_id,revision,rules_id,storage_version,coin_mag,profile,updated_at) VALUES(?,7,?,1,zeroblob(16),?,?)", user, lakeRules.RulesID, profileRaw, testNow)
	hostileMustExec(t, database, "INSERT INTO lake_notes_casts(id,user_id,rules_id,storage_version,phase,paused,generation,revision,last_tick,snapshot,reward_plan,held,active_elapsed_ns,created_at,updated_at) VALUES(?,?,?,2,'waiting',1,3,8,0,?,X'7b7d',0,0,?,?)", hostileOID("lnc_"), user, lakeRules.RulesID, castRaw, testNow, testNow)
	credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(hostileBlob16(101))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := vault.SealForGenerationTwoContext([]byte("retained-fixture-credential"), credentialContext)
	if err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, database, "INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at,orphaned_at) VALUES(?,?,?,?,?,?)", credentialContext.ContextID(), bootstrapTestCredentialOrigin, bootstrapTestConnector, envelope, testNow, time.Now().Unix())
	for key, value := range map[string]string{
		"global_rpm_per_user": "123", "games_enabled": "1", "game_lakenotes_enabled": "0",
		"game_gwent_enabled": "1", "game_gwent_standard_enabled": "1", "game_gwent_standard_ticket_milli": "7000",
		"game_steadycatch_enabled": "1", "game_steadycatch_price_milli": "5000", "game_steadycatch_first_reward_milli": "9000",
	} {
		hostileMustExec(t, database, "UPDATE site_config SET value=?,updated_at=? WHERE key=?", value, testNow, key)
	}
	hostileMustExec(t, database, "UPDATE limited_activity_configs SET visible=1,paused=0 WHERE activity_key='lake-notes'")
	hostileMustExec(t, database, "UPDATE game_ai_settings SET enabled=1,revision=9 WHERE game_key='bidding'")
	if err := validateSourceConfig(context.Background(), database); err != nil {
		t.Fatalf("invalid configured fixture: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	source, err := openSQLite(path, "rw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	hostileMustExec(t, source, "PRAGMA foreign_keys=OFF;"+preGwentAIFixture+"PRAGMA foreign_keys=ON;")
	hostileMustExec(t, source, "INSERT INTO sqlite_sequence(name,seq) VALUES('game_duel_anonymous',77),('game_ai_queue',41)")
	assertRetainedManifest(t, source, managementAndGamesManifestHash)
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	verifyReleasedStorageUpgrade(t, path, managementAndGamesManifestHash)
}
