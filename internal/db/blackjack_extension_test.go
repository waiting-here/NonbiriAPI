package db

import (
	"strings"
	"testing"
)

func TestBlackjackPartialSourceRejectedWithoutWriting(t *testing.T) {
	path, vault := bootstrapTestPath(t, "partial.sqlite"), bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, store.DB(), `DROP TABLE game_blackjack_entries`)
	hostileMustExec(t, store.DB(), `CREATE TABLE game_blackjack_entries(incomplete TEXT)`)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before := snapshotBootstrapSources(t, path)
	store, err = Open(path, vault)
	if store != nil {
		store.Close()
		t.Fatal("partial schema accepted")
	}
	if err == nil {
		t.Fatal("missing startup rejection", err)
	}
	assertBootstrapSourcesUnchanged(t, path, before)
}

func TestBlackjackNineSeatFreshConstraintAndRepeatedOpenNoOp(t *testing.T) {
	path, vault := bootstrapTestPath(t, "nine-seat.sqlite"), bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	var ddl string
	if err := store.DB().QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='game_blackjack_entries'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "seat_no INTEGER CHECK(seat_no BETWEEN 0 AND 8)") {
		t.Fatalf("fresh schema is not nine-seat: %s", ddl)
	}
	assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM game_blackjack_entries`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reopen changed entries: %d %v", count, err)
	}
}
