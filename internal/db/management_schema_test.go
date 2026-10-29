package db

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestManagementCaptureSchemaFreshOpen(t *testing.T) {
	vault, err := secret.New([]byte(strings.Repeat("t", 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	trace := NewStartupTrace(time.Now(), nil)
	path := filepath.Join(privateDBDir(t), "capture.sqlite")
	store, err := OpenContext(trace.Context(t.Context()), path, vault)
	if err != nil {
		t.Fatalf("stage=%+v error=%v", trace.Snapshot(), err)
	}
	defer store.Close()
}

func TestManagementCaptureSchemaAllowsFreshSeeds(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	if _, err := database.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	epoch, err := GenerateOpaqueID("b1e_")
	if err != nil {
		t.Fatal(err)
	}
	if err = seedGenerationTwo(t.Context(), tx, epoch); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = tx.QueryRow("SELECT (SELECT COUNT(*) FROM management_capture_rows)+(SELECT COUNT(*) FROM management_capture_totals)").Scan(&count); err != nil || count != 0 {
		t.Fatalf("fresh seeds activated management capture: %d %v", count, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = reconcileEndpointKeySecretOrphans(t.Context(), database); err != nil {
		t.Fatal(err)
	}
}
