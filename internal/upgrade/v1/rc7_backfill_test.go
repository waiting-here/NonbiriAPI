package v1

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestConversionBackfillBatchesShareOneTransaction(t *testing.T) {
	path, vault := sourceFixture(t)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 1001; i++ {
		identity := fmt.Sprintf("synthetic-%d", i)
		if i == 1001 {
			identity = "invalid identity"
		}
		mustExec(t, tx, `INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,'synthetic',zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),0,0)`, identity)
		id := make([]byte, 16)
		binary.BigEndian.PutUint64(id[8:], uint64(i))
		credential, err := secret.NewGenerationTwoEndpointKeyContext(id)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := vault.SealForGenerationTwoContext([]byte("synthetic"), credential)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, tx, `INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,'https://synthetic.example/v1','openai-compatible',?,0)`, id, envelope)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
		t.Fatal("invalid identity accepted")
	}
	database, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow("SELECT count(*) FROM user_continuity_identities").Scan(&count); err != nil || count != 0 {
		t.Fatal("earlier batch escaped rollback", count, err)
	}
	mustExec(t, database, "UPDATE users SET discord_id='synthetic-1001' WHERE discord_id='invalid identity'")
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err != nil {
		t.Fatal(err)
	}
	database, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var identities, reviewed int
	if err := database.QueryRow("SELECT (SELECT count(*) FROM user_continuity_identities),(SELECT count(*) FROM endpoint_key_secrets WHERE key_body_review_hmac IS NOT NULL)").Scan(&identities, &reviewed); err != nil || identities != 1001 || reviewed != 1001 {
		t.Fatal(identities, reviewed, err)
	}
}

func TestConversionImportsGatewayOnlyBeforeInitialization(t *testing.T) {
	legacy := `{"models":[{"base_url":"https://gateway.example/v1","model":"m","adapter":"openai_chat"}]}`
	for _, mode := range []string{"import", "preserve", "initialized", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			path, vault := sourceFixture(t)
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "initialized" {
				mustExec(t, database, "DELETE FROM gateway_model_capabilities_state")
			}
			if mode == "preserve" {
				config, err := gatewaypolicy.Parse(legacy)
				if err != nil {
					t.Fatal(err)
				}
				entry := config.Entries()[0]
				entry.MaxOutputTokens = 512
				policy, err := json.Marshal(entry.Policy)
				if err != nil {
					t.Fatal(err)
				}
				mustExec(t, database, "INSERT INTO gateway_model_capabilities(base_url,model,policy_json,revision,updated_at) VALUES(?,?,?,7,123)", entry.BaseURL, entry.Model, string(policy))
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			raw := legacy
			if mode == "initialized" || mode == "invalid" {
				raw = "invalid private configuration"
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Run(t.Context(), path, vault, Options{LegacyGatewayModels: raw})
			if mode == "invalid" {
				if err == nil {
					t.Fatal("invalid pending import accepted")
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("invalid check changed database", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Run(t.Context(), path, vault, Options{Apply: true, LegacyGatewayModels: raw}); err != nil {
				t.Fatal(err)
			}
			database, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			records, err := gatewaypolicy.ReadRecords(t.Context(), database)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "initialized" {
				if len(records) != 0 {
					t.Fatal("initialized configuration changed")
				}
				return
			}
			if len(records) != 1 {
				t.Fatal("missing imported policy")
			}
			if mode == "preserve" && (records[0].MaxOutputTokens != 512 || records[0].Revision != "7") {
				t.Fatal("database policy was overwritten")
			}
		})
	}
}

func TestConversionWaitsForExclusiveDatabaseAccess(t *testing.T) {
	path, vault := sourceFixture(t)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := tx.QueryRow("SELECT version FROM schema_state").Scan(&version); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := Run(ctx, path, vault, Options{Apply: true}); err == nil {
		t.Fatal("conversion bypassed a live reader")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err != nil {
		t.Fatal("retry after exclusive access failed", err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err != db.ErrDatabaseInUse {
		t.Fatal("owned database accepted", err)
	}
}
