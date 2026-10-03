package db

import (
	"context"
	"errors"
	"sync"
	"time"

	sqlite "modernc.org/sqlite"
)

// StartupStage is a closed vocabulary shared with the process composition root.
type StartupStage string

const (
	StageConfiguration        StartupStage = "configuration"
	StageIdentityValidation   StartupStage = "identity_validation"
	StageSchemaValidation     StartupStage = "schema_validation"
	StageCredentialValidation StartupStage = "credential_validation"
	StageAccountRecovery      StartupStage = "account_recovery"
	StageGameValidation       StartupStage = "game_validation"
	StageQuotaValidation      StartupStage = "quota_validation"
	StageBusinessRecovery     StartupStage = "business_recovery"
	StagePathPreflight        StartupStage = "path_preflight"
	StageSnapshotCopy         StartupStage = "snapshot_copy"
	StageSnapshotValidation   StartupStage = "snapshot_validation"
	StageSourceRecheck        StartupStage = "source_recheck"
	StageRawHandlesClosed     StartupStage = "raw_handles_closed"
	StageSourceOpen           StartupStage = "source_open"
	StageSchemaUpgrade        StartupStage = "schema_upgrade"
	StageDomainRecovery       StartupStage = "domain_recovery"
	StageRoutesReady          StartupStage = "routes_ready"
	StageListenerBound        StartupStage = "listener_bound"
	StageReady                StartupStage = "ready"
)

type StartupProgress struct {
	Stage              StartupStage
	FailureStage       StartupStage
	ElapsedMS          int64
	StageElapsedMS     int64
	ProcessedCount     int64
	ProcessedBytes     int64
	LastCommittedPhase StartupStage
}

// StartupTrace contains counters and fixed labels only, never paths or row data.
// It remains safe to inspect while a non-interruptible OS call is pending.
type StartupTrace struct {
	mu           sync.Mutex
	started      time.Time
	stageStarted time.Time
	progress     StartupProgress
	observe      func(StartupProgress)
}

type startupTraceKey struct{}

func NewStartupTrace(started time.Time, observe func(StartupProgress)) *StartupTrace {
	return &StartupTrace{started: started, stageStarted: started, observe: observe,
		progress: StartupProgress{Stage: StageConfiguration}}
}

func (t *StartupTrace) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, startupTraceKey{}, t)
}

func (t *StartupTrace) Snapshot() StartupProgress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked()
}

func (t *StartupTrace) snapshotLocked() StartupProgress {
	out := t.progress
	out.ElapsedMS = time.Since(t.started).Milliseconds()
	out.StageElapsedMS = time.Since(t.stageStarted).Milliseconds()
	return out
}

func validStartupStage(stage StartupStage) bool {
	switch stage {
	case StageIdentityValidation, StageSchemaValidation, StageCredentialValidation, StageAccountRecovery, StageGameValidation, StageQuotaValidation, StageBusinessRecovery, StageConfiguration, StagePathPreflight, StageSnapshotCopy, StageSnapshotValidation,
		StageSourceRecheck, StageRawHandlesClosed, StageSourceOpen, StageSchemaUpgrade,
		StageDomainRecovery, StageRoutesReady, StageListenerBound, StageReady:
		return true
	}
	return false
}

func RecordStartupStage(ctx context.Context, stage StartupStage) {
	t, _ := ctx.Value(startupTraceKey{}).(*StartupTrace)
	if t == nil || !validStartupStage(stage) {
		return
	}
	t.mu.Lock()
	previous := t.snapshotLocked()
	t.progress.Stage = stage
	t.stageStarted = time.Now()
	progress := t.snapshotLocked()
	t.mu.Unlock()
	if t.observe != nil {
		t.observe(previous)
		t.observe(progress)
	}
}

func RecordStartupCheckpoint(ctx context.Context, stage StartupStage) {
	t, _ := ctx.Value(startupTraceKey{}).(*StartupTrace)
	if t == nil || !validStartupStage(stage) {
		return
	}
	t.mu.Lock()
	t.progress.LastCommittedPhase = stage
	t.mu.Unlock()
}

func recordStartupFailure(ctx context.Context, err error) {
	if err == nil {
		return
	}
	t, _ := ctx.Value(startupTraceKey{}).(*StartupTrace)
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.progress.FailureStage == "" {
		t.progress.FailureStage = t.progress.Stage
	}
	t.mu.Unlock()
}

func recordStartupProgress(ctx context.Context, count, bytes int64) {
	t, _ := ctx.Value(startupTraceKey{}).(*StartupTrace)
	if t == nil {
		return
	}
	t.mu.Lock()
	t.progress.ProcessedCount += count
	t.progress.ProcessedBytes += bytes
	t.mu.Unlock()
}

// StartupFailureDetails provides safe diagnostics without exposing an error's
// potentially private SQL, configuration values or filesystem path.
func StartupFailureDetails(err error) (category string, retryable bool, secondary []string) {
	category = "initialization_failed"
	var classified *StartupError
	var sqliteError *sqlite.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		category, retryable = "deadline", true
	case errors.Is(err, context.Canceled):
		category = "cancelled"
	case errors.Is(err, ErrDatabaseInUse):
		category, retryable = "database_in_use", true
	case errors.As(err, &classified):
		category = string(classified.Kind)
		if classified.Reason != "" {
			category += ":" + classified.Reason
		}
		retryable = classified.Kind == StartupSourceChanged || classified.Kind == StartupReadIO || classified.Kind == StartupSQLBusy
	case errors.As(err, &sqliteError) && sqliteError.Code()&0xff == 5:
		category, retryable = "sql_busy", true
	}
	var additional interface{ SecondaryCategories() []string }
	if errors.As(err, &additional) {
		secondary = additional.SecondaryCategories()
	}
	return
}
