package v1

import (
	"context"
	"sync"
	"time"
)

// Progress describes a conversion stage. Heartbeats report elapsed time only;
// they do not imply that a running SQLite statement has processed more rows.
// Callbacks are serialized and must return promptly.
type Progress struct {
	Type                string `json:"type"`
	Phase               string `json:"phase"`
	State               string `json:"state"`
	ElapsedMS           int64  `json:"elapsed_ms"`
	PhaseElapsedMS      int64  `json:"phase_elapsed_ms"`
	SourceSchemaVersion int    `json:"source_schema_version"`
	TargetSchemaVersion int    `json:"target_schema_version"`
	MigrationVersion    int    `json:"migration_version"`
	Table               string `json:"table"`
}

type progressKey struct{}
type progressStage struct {
	phase, table string
	version      int
	started      time.Time
}

type progressReporter struct {
	mu       sync.Mutex
	callback func(Progress)
	started  time.Time
	source   int
	target   int
	active   *progressStage
}

func withProgress(ctx context.Context, target int, callback func(Progress), interval time.Duration) (context.Context, func()) {
	if callback == nil {
		return ctx, func() {}
	}
	reporter := &progressReporter{callback: callback, started: time.Now(), target: target}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				reporter.mu.Lock()
				if reporter.active != nil {
					reporter.emit(reporter.active, "heartbeat")
				}
				reporter.mu.Unlock()
			}
		}
	}()
	return context.WithValue(ctx, progressKey{}, reporter), func() { close(stop); <-done }
}

func setProgressSource(ctx context.Context, version int) {
	if reporter, ok := ctx.Value(progressKey{}).(*progressReporter); ok {
		reporter.mu.Lock()
		reporter.source = version
		reporter.mu.Unlock()
	}
}

func beginProgress(ctx context.Context, phase string, version int, table string) func(error) {
	reporter, ok := ctx.Value(progressKey{}).(*progressReporter)
	if !ok {
		return func(error) {}
	}
	reporter.mu.Lock()
	parent := reporter.active
	if version == 0 && parent != nil {
		version = parent.version
	}
	stage := &progressStage{phase: phase, table: table, version: version, started: time.Now()}
	reporter.active = stage
	reporter.emit(stage, "started")
	reporter.mu.Unlock()
	return func(err error) {
		reporter.mu.Lock()
		defer reporter.mu.Unlock()
		state := "completed"
		if err != nil {
			state = "failed"
		}
		reporter.emit(stage, state)
		reporter.active = parent
	}
}

func (reporter *progressReporter) emit(stage *progressStage, state string) {
	now := time.Now()
	reporter.callback(Progress{Type: "upgrade_progress", Phase: stage.phase, State: state,
		ElapsedMS: now.Sub(reporter.started).Milliseconds(), PhaseElapsedMS: now.Sub(stage.started).Milliseconds(),
		SourceSchemaVersion: reporter.source, TargetSchemaVersion: reporter.target,
		MigrationVersion: stage.version, Table: stage.table})
}

func progressStep(ctx context.Context, phase string, version int, table string, work func() error) error {
	finish := beginProgress(ctx, phase, version, table)
	err := ctx.Err()
	if err == nil {
		err = work()
	}
	finish(err)
	return err
}
