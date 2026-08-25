package db

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestGetUserRPMLimit(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "rpm.db"))
	defer store.Close()
	user, err := store.CreateDiscordUser("discord-rpm", "rpm-user", "")
	if err != nil {
		t.Fatal(err)
	}

	// NULL cap: no custom limit, administrator default applies.
	limit, has, err := store.GetUserRPMLimit(user.ID)
	if err != nil || has || limit != 0 {
		t.Fatalf("NULL cap = limit=%d has=%v err=%v", limit, has, err)
	}

	// Stored cap is returned as the authoritative explicit user override.
	if _, err := store.DB().Exec(`UPDATE users SET rpm_limit=? WHERE id=?`, 25, user.ID); err != nil {
		t.Fatal(err)
	}
	limit, has, err = store.GetUserRPMLimit(user.ID)
	if err != nil || !has || limit != 25 {
		t.Fatalf("stored cap = limit=%d has=%v err=%v", limit, has, err)
	}

	// Generation one rejects zero rather than assigning it a hidden meaning.
	if _, err := store.DB().Exec(`UPDATE users SET rpm_limit=? WHERE id=?`, 0, user.ID); err != nil {
		// Expected constraint failure.
	} else {
		t.Fatal("zero RPM override passed the schema CHECK")
	}

	// Missing user: not found, not an error.
	limit, has, err = store.GetUserRPMLimit(user.ID + 10_000)
	if err != nil || has || limit != 0 {
		t.Fatalf("missing user = limit=%d has=%v err=%v", limit, has, err)
	}

	// Invalid ids fail closed.
	if _, _, err := store.GetUserRPMLimit(0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("zero id err=%v", err)
	}
	if _, _, err := store.GetUserRPMLimit(-1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("negative id err=%v", err)
	}
}
