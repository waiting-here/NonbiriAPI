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

func verifyReleasedStorageUpgrade(t *testing.T, source, expectedSourceManifest string) {
	t.Helper()
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
	before := interactionTableDigests(t, prior, sourceManifest)
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
		store, err := Open(path, vault)
		if err != nil {
			t.Fatal("released upgrade/open failed", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		database := store.DB()
		manifest, err := readGenerationManifest(ctx, database)
		if err != nil || !reflect.DeepEqual(manifest, freshManifest) {
			store.Close()
			t.Fatal("fresh and upgraded schema differ", err)
		}
		if attempt == 0 {
			after := interactionTableDigests(t, database, sourceManifest)
			for table, want := range before {
				if after[table] != want {
					t.Errorf("retained source columns changed in %s (rows %d -> %d)", table, want.Rows, after[table].Rows)
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

type interactionTableDigest struct {
	Rows int64
	Sum  [sha256.Size]byte
	XOR  [sha256.Size]byte
}

// Order-independent digests keep the real-copy check bounded in memory. Column
// types participate in each row hash; row counts, sum and XOR preserve duplicates.
func interactionTableDigests(t *testing.T, database *sql.DB, manifest generationManifest) map[string]interactionTableDigest {
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
