package v1

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
)

// The source program creates this populated database. Its own executable
// also checks rejection of the upgraded database after this test returns.
func TestConvertReleasedDatabase(t *testing.T) {
	store := convertReleasedFixture(t, "NONBIRI_UPGRADE_FIXTURE", 0x42, 54, db.Schema54ManifestHash)
	ctx := t.Context()
	var valid bool
	err := store.DB().QueryRow(`SELECT EXISTS(SELECT 1 FROM legal_holds h JOIN donations d ON CAST(d.id AS TEXT)=h.object_ref WHERE h.state='active' AND h.object_kind='donation' AND h.basis='Synthetic preservation requirement') AND EXISTS(SELECT 1 FROM site_config WHERE key='legal_privacy_override_en' AND value=?) AND EXISTS(SELECT 1 FROM caller_keys WHERE generation=7) AND EXISTS(SELECT 1 FROM credential_versions) AND EXISTS(SELECT 1 FROM credit_accounts WHERE balance_sign=-1)`, "Synthetic privacy.\nPreserve punctuation, spaces, and tabs.\t").Scan(&valid)
	if err != nil || !valid {
		t.Fatal("preserved governance, credential and wallet state", valid, err)
	}
	service, err := donation.New(donation.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var owner int64
	if err = tx.QueryRow("SELECT id FROM users WHERE username='dual-positive'").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	exported, err := service.ExportUserTx(ctx, tx, owner, time.Now().Unix(), 100)
	if err != nil || len(exported) != 1 || len(exported[0].Keys) != 1 || len(exported[0].Keys[0].ModelLimits) != 1 {
		t.Fatal("owner export", exported, err)
	}
	model := exported[0].Keys[0].ModelLimits[0]
	if model.UpstreamModelID != "source-model" || !model.HistoricalCostsPartial || model.Used.Calls != "1" || model.Used.Credits != "1.125" || model.Used.Tokens != "19" || *model.Remaining.Tokens != "23" {
		t.Fatal(model)
	}
}

func TestConvertReleasedAttendanceDatabase(t *testing.T) {
	convertReleasedFixture(t, "NONBIRI_RELEASED_ATTENDANCE_FIXTURE", 0, 55, db.Schema55ManifestHash)
}

func TestConvertReleasedSchema56Database(t *testing.T) {
	convertReleasedFixture(t, "NONBIRI_RELEASED_SCHEMA56_FIXTURE", 0, 56, db.Schema56ManifestHash)
}

func convertReleasedFixture(t *testing.T, fixtureEnv string, keyByte byte, version int, manifest string) *db.Store {
	t.Helper()
	path := os.Getenv(fixtureEnv)
	if path == "" {
		t.Skip("the pinned release gate supplies the source")
	}
	cli := os.Getenv("NONBIRI_UPGRADE_CLI")
	if cli == "" {
		t.Fatal("missing upgrade executable")
	}
	t.Setenv("NONBIRI_MASTER_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{keyByte}, 32)))
	t.Setenv("NONBIRI_MASTER_KEY_FILE", "")
	vault := testVault(t, keyByte)
	ctx := t.Context()
	if err := db.InspectContext(ctx, path, vault, func(ctx context.Context, source *sql.DB) error {
		assertRetainedManifest(t, source, manifest)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode    string
		status  string
		version int
	}{{"--check", "ready", version}, {"--apply", "converted", version}, {"--check", "current", db.CurrentSchemaVersion}, {"--apply", "current", db.CurrentSchemaVersion}} {
		command := exec.CommandContext(ctx, cli, tc.mode, "--database", path)
		var progress bytes.Buffer
		command.Stderr = &progress
		out, err := command.Output()
		if err != nil {
			t.Fatalf("%s: %v (%s)", tc.mode, err, progress.Bytes())
		}
		var got Result
		if err = json.Unmarshal(out, &got); err != nil || got.Status != tc.status || got.SourceSchemaVersion != tc.version || got.TargetSchemaVersion != db.CurrentSchemaVersion || got.TargetManifestHash != db.PinnedGenerationTwoManifestHash {
			t.Fatal(got, err)
		}
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestUnsupportedUpgradeSourcesRemainUnchanged(t *testing.T) {
	vault := bootstrapTestVault(t)
	path := bootstrapTestPath(t, "source.sqlite")
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		marker  int
		message string
	}{{53, "v1.3.12"}, {28, "v1.3.12"}, {db.CurrentSchemaVersion + 1, "newer"}, {54, "differs from the released schema"}, {55, "differs from the released schema"}} {
		database, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, database, "UPDATE schema_state SET version=?", tc.marker)
		if err = database.Close(); err != nil {
			t.Fatal(err)
		}
		for _, apply := range []bool{false, true} {
			if _, err = Run(t.Context(), path, vault, Options{Apply: apply}); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatal(tc, apply, err)
			}
		}
		if err = db.InspectContext(t.Context(), path, vault, func(ctx context.Context, q *sql.DB) error {
			assertRetainedManifest(t, q, db.PinnedGenerationTwoManifestHash)
			var marker int
			if err := q.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1").Scan(&marker); err != nil || marker != tc.marker {
				t.Fatal(marker, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	restoreSourceLayout(t, database)
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	wrong := testVault(t, 0x31)
	if _, err = Run(t.Context(), path, wrong, Options{Apply: true}); err == nil {
		t.Fatal("incorrect key accepted")
	}
	var startup *db.StartupError
	if !errors.As(err, &startup) || startup.Kind != db.StartupCredentialReject {
		t.Fatal(err)
	}
	if status, err := Run(t.Context(), path, vault, Options{}); err != nil || status.Status != "ready" {
		t.Fatal(status, err)
	}
}
