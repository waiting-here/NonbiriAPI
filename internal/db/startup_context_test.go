package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenContextCanceledBeforeSourceIO(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-created")
	path := filepath.Join(parent, "source.sqlite")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store, err := OpenContext(ctx, path, bootstrapTestVault(t))
	if store != nil {
		defer func() { _ = store.Close() }()
	}
	if store != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenContext with pre-canceled context = (%v, %v), want nil and context.Canceled", store, err)
	}
	if _, err := os.Lstat(parent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-canceled startup touched the source parent: %v", err)
	}
}

func TestOpenContextRejectsOwnedPathAndHardlink(t *testing.T) {
	path := bootstrapTestPath(t, "owned.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatalf("create current database: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if duplicate, err := OpenContext(ctx, path, vault); duplicate != nil || !errors.Is(err, ErrDatabaseInUse) {
		if duplicate != nil {
			_ = duplicate.Close()
		}
		t.Fatalf("same-path OpenContext = (%v, %v), want nil and ErrDatabaseInUse", duplicate, err)
	}

	alias := filepath.Join(filepath.Dir(path), "owned-hardlink.sqlite")
	if err := os.Link(path, alias); err != nil {
		t.Logf("hardlink alias unavailable on this filesystem: %v", err)
		return
	}
	if duplicate, err := OpenContext(ctx, alias, vault); duplicate != nil || !errors.Is(err, ErrDatabaseInUse) {
		if duplicate != nil {
			_ = duplicate.Close()
		}
		t.Fatalf("hardlink alias OpenContext = (%v, %v), want nil and ErrDatabaseInUse", duplicate, err)
	}
}

func TestCloseContextDeadlineRetainsOwnershipUntilConnectionRelease(t *testing.T) {
	path := bootstrapTestPath(t, "close-deadline.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatalf("create current database: %v", err)
	}
	defer func() { _ = store.Close() }()

	connection, err := store.DB().Conn(context.Background())
	if err != nil {
		t.Fatalf("hold SQLite connection: %v", err)
	}
	defer func() { _ = connection.Close() }()
	if err := connection.PingContext(context.Background()); err != nil {
		t.Fatalf("open held SQLite connection: %v", err)
	}

	short, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = store.CloseContext(short)
	var deadline *CloseDeadlineError
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &deadline) || deadline.OpenConnections < 1 {
		t.Fatalf("CloseContext with held connection = %T %v, want CloseDeadlineError with live connection", err, err)
	}
	if reopened, err := OpenContext(context.Background(), path, vault); reopened != nil || !errors.Is(err, ErrDatabaseInUse) {
		if reopened != nil {
			_ = reopened.Close()
		}
		t.Fatalf("OpenContext before connection release = (%v, %v), want nil and ErrDatabaseInUse", reopened, err)
	}

	if err := connection.Close(); err != nil {
		t.Fatalf("release held connection: %v", err)
	}
	wait, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer waitCancel()
	if err := store.CloseContext(wait); err != nil {
		t.Fatalf("second CloseContext did not await actual close: %v", err)
	}
	// This is a new startup; the prior close budget has already served its purpose.
	reopened, err := Open(path, vault)
	if err != nil {
		t.Fatalf("reopen after actual close: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened database: %v", err)
	}
}

func TestOpenContextCanceledPreflightPreservesSource(t *testing.T) {
	path := bootstrapTestPath(t, "cancel-preflight.sqlite")
	vault := bootstrapTestVault(t)
	first, err := Open(path, vault)
	if err != nil {
		t.Fatalf("create current database: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close current database: %v", err)
	}
	before := snapshotBootstrapSources(t, path)
	preserveBootstrapHooks(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	beforeWritableOpenHook = cancel

	store, err := OpenContext(ctx, path, vault)
	if store != nil {
		defer func() { _ = store.Close() }()
	}
	if store != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenContext canceled during preflight = (%v, %v), want nil and context.Canceled", store, err)
	}
	assertBootstrapSourcesUnchanged(t, path, before)
	beforeWritableOpenHook = nil
	if reopened, err := Open(path, vault); err != nil {
		t.Fatalf("reopen preserved source: %v", err)
	} else if err := reopened.Close(); err != nil {
		t.Fatalf("close preserved source: %v", err)
	}
}

func TestStartupSecondaryErrorsRemainBoundedAndSafe(t *testing.T) {
	primary := startupError(StartupSourceChanged)
	err := primary
	secretDetail := errors.New("secret=/private/fixture.sqlite")
	for i := 0; i < 20; i++ {
		err = appendStartupError(err, secretDetail)
	}
	var categories interface{ SecondaryCategories() []string }
	if !errors.As(err, &categories) {
		t.Fatalf("startup error %T has no secondary categories", err)
	}
	got := categories.SecondaryCategories()
	if len(got) != 8 {
		t.Fatalf("secondary category count = %d, want bounded count 8", len(got))
	}
	for _, category := range got {
		if category != "cleanup_failed" {
			t.Fatalf("unclassified secondary category = %q, want cleanup_failed", category)
		}
	}
	got[0] = "modified"
	if categories.SecondaryCategories()[0] != "cleanup_failed" {
		t.Fatal("secondary category accessor exposed mutable internal storage")
	}
	var startup *StartupError
	if !errors.As(err, &startup) || startup.Kind != StartupSourceChanged || !errors.Is(err, ErrDatabaseStartup) {
		t.Fatalf("primary startup error was lost: %T %v", err, err)
	}
	if strings.Contains(err.Error(), "secret=") || err.Error() != primary.Error() {
		t.Fatalf("secondary detail leaked or replaced primary: %q", err.Error())
	}

	canceled := appendStartupError(context.Canceled, startupError(StartupCleanupFailure))
	if !errors.Is(canceled, context.Canceled) || errors.Is(canceled, ErrDatabaseStartup) {
		t.Fatalf("secondary cleanup replaced canceled primary: %T %v", canceled, canceled)
	}
}
