package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"testing"
)

func TestAgentDeletionRollbackCommitAndFinalizers(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			f := newLifecycleTestFixture(t, 100)
			id := seedLifecycleUser(t, f.store.DB(), "agent-delete", false, 100)
			f.config.Ledger = &testLedgerDelete{deleteUser: true}
			retirement := &testRetirement{}
			f.config.Retirement = testRetirementBoundary{retirement: retirement}
			finalized := &testFinalizer{}
			f.config.Delete.AuthSessionCallerKey = testDeleteAdapter{name: "agent-auth", finalizer: finalized}
			c := mustNewLifecycleCoordinator(t, f.config)
			prepared, err := c.PrepareAgentDeletion(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			elevationCalls := 0
			authorize := func(_ context.Context, _ *sql.Tx, _ agentplan.Actor, kind string) error {
				if kind == "fresh_elevation" {
					elevationCalls++
				}
				return nil
			}
			tx, err := f.store.DB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = prepared.Apply(t.Context(), tx, agentplan.Actor{ID: 42, Role: "admin"}, "0", authorize); err != nil {
				t.Fatal(err)
			}
			if retirement.commits != 0 || finalized.commits != 0 {
				t.Fatal("finalized before commit")
			}
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			prepared.Finalize(commit)
			prepared.Finalize(commit)
			var count int
			if err = f.store.DB().QueryRow("SELECT count(*) FROM users WHERE id=?", id).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 1
			if commit {
				want = 0
			}
			if count != want || elevationCalls != 1 {
				t.Fatalf("users=%d elevation=%d", count, elevationCalls)
			}
			if commit {
				if retirement.commits != 1 || retirement.aborts != 0 || finalized.commits != 1 {
					t.Fatalf("retirement=%+v finalizer=%+v", retirement, finalized)
				}
			} else {
				if retirement.aborts != 1 || retirement.commits != 0 || finalized.aborts != 1 {
					t.Fatalf("retirement=%+v finalizer=%+v", retirement, finalized)
				}
			}
		})
	}
}
func TestAgentDeletionRechecksRevisionAfterPreparedDrain(t *testing.T) {
	f := newLifecycleTestFixture(t, 100)
	id := seedLifecycleUser(t, f.store.DB(), "agent-delete-stale", false, 100)
	c := mustNewLifecycleCoordinator(t, f.config)
	prepared, err := c.PrepareAgentDeletion(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := f.store.DB().BeginTx(t.Context(), nil)
	err = prepared.Apply(t.Context(), tx, agentplan.Actor{ID: 42, Role: "admin"}, "1", func(context.Context, *sql.Tx, agentplan.Actor, string) error { return nil })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version=%v", err)
	}
	tx.Rollback()
	prepared.Finalize(false)
}
