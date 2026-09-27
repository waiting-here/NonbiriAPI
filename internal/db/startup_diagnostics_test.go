package db

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func waitForStartupOwnershipRelease(t *testing.T, path string) {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		absolute = strings.ToLower(absolute)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		databaseOwners.Lock()
		_, owned := databaseOwners.entries[filepath.Clean(absolute)]
		databaseOwners.Unlock()
		if !owned {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("startup cleanup retained database ownership")
		}
	}
}

func TestStartupCancellationAtEveryDatabaseStage(t *testing.T) {
	for _, target := range []StartupStage{StagePathPreflight, StageSnapshotCopy, StageSnapshotValidation,
		StageSourceRecheck, StageRawHandlesClosed, StageSourceOpen, StageSchemaUpgrade, StageDomainRecovery} {
		t.Run(string(target), func(t *testing.T) {
			path := bootstrapTestPath(t, string(target)+".sqlite")
			vault := bootstrapTestVault(t)
			initial, err := Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			if err := initial.Close(); err != nil {
				t.Fatal(err)
			}
			before := snapshotBootstrapSources(t, path)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reached := false
			trace := NewStartupTrace(time.Now(), func(progress StartupProgress) {
				if progress.Stage == target {
					reached = true
					cancel()
				}
			})
			store, err := OpenContext(trace.Context(ctx), path, vault)
			if store != nil {
				_ = store.Close()
			}
			if !reached || store != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("stage=%s reached=%t store=%v err=%v", target, reached, store, err)
			}
			progress := trace.Snapshot()
			failedStage := progress.FailureStage
			if failedStage == "" {
				failedStage = progress.Stage
			}
			if failedStage != target {
				t.Fatalf("cleanup replaced failure stage: got %s want %s", failedStage, target)
			}
			waitForStartupOwnershipRelease(t, path)
			switch target {
			case StagePathPreflight, StageSnapshotCopy, StageSnapshotValidation, StageSourceRecheck,
				StageRawHandlesClosed, StageSourceOpen:
				assertBootstrapSourcesUnchanged(t, path, before)
				if trace.Snapshot().LastCommittedPhase != "" {
					t.Fatal("preflight failure claimed committed writes")
				}
			}
			// A second clean restart must preserve the same schema/configuration,
			// including when cancellation followed a committed migration.
			for restart := 0; restart < 2; restart++ {
				reopened, err := Open(path, vault)
				if err != nil {
					t.Fatalf("restart %d: %v", restart, err)
				}
				if err := reopened.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestStartupDiagnosticsKeepPrimaryCauseAndHideSecondaryDetails(t *testing.T) {
	primary := &StartupError{Kind: StartupSchemaMismatch}
	err := WithStartupCleanupError(primary, &CloseDeadlineError{Cause: context.DeadlineExceeded, OpenConnections: 1})
	category, retryable, secondary := StartupFailureDetails(err)
	if category != "schema_mismatch" || retryable || len(secondary) != 1 || secondary[0] != "cleanup_deadline" {
		t.Fatalf("primary replaced by cleanup: %s %t %v", category, retryable, secondary)
	}
	categories := []StartupErrorKind{StartupReadIO, StartupWriteIO, StartupSQLBusy}
	for _, kind := range categories {
		category, _, _ := StartupFailureDetails(startupError(kind))
		if category != string(kind) {
			t.Fatalf("I/O or busy mislabeled as a source change: %s", category)
		}
	}
}
