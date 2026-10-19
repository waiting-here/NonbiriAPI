package activities

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestProjectionUsesCommittedReadSnapshotWhileWriterIsHeld(t *testing.T) {
	f := newActivityFixture(t, 1_800_100_000)
	user, _ := f.seedUser("snapshot-reader", false)
	f.setActivityConfig(true, true, false, 0, 0)
	before, err := f.repository.ProjectActivities(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err = writer.Exec("UPDATE site_config SET value='0' WHERE key=?", configActivitiesEnabled); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec("UPDATE config_revisions SET revision=revision+1 WHERE domain='activities'"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	during, err := f.repository.ProjectActivities(ctx, user)
	if err != nil || !during.Snapshot.Master.Enabled || during.Revision != before.Revision {
		t.Fatalf("projection blocked or observed uncommitted state: %+v %v", during, err)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
	after, err := f.repository.ProjectActivities(t.Context(), user)
	if err != nil || after.Snapshot.Master.Enabled || after.Revision == before.Revision {
		t.Fatalf("committed snapshot missing: %+v %v", after, err)
	}
}

func TestProjectionReadPoolCancellationAndCloseReturnErrors(t *testing.T) {
	f := newActivityFixture(t, 1_800_100_000)
	user, _ := f.seedUser("waiting-reader", false)
	var held []*sql.Conn
	for range 2 {
		conn, err := f.store.ReadDB().Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
		t.Cleanup(func() { _ = conn.Close() })
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := f.repository.ProjectActivities(ctx, user); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pool wait = %v", err)
	}
	for _, conn := range held {
		_ = conn.Close()
	}
	if _, err := f.repository.ProjectActivities(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	if stats := f.store.ReadDB().Stats(); stats.InUse != 0 || stats.WaitCount == 0 {
		t.Fatalf("reader leaked or bypassed wait: %+v", stats)
	}
	if err := f.store.ReadDB().Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repository.ProjectActivities(t.Context(), user); err == nil {
		t.Fatal("closed reader silently returned a snapshot")
	}
	if err := f.store.DB().PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}
