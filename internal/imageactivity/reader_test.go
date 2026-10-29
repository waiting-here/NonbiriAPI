package imageactivity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestImageReadersWhileWriterHeldAndSessionRevoked(t *testing.T) {
	f := newFixture(t)
	// Seed no external work: empty catalog and queue still exercise real authority and SQL.
	tx, err := f.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(f.ctx(f.user), 2*time.Second)
	defer cancel()
	if _, err = f.service.ListTasks(ctx, f.user, 20, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.UserModels(ctx, f.user, 20, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.GetQueue(ctx, f.user); err != nil {
		t.Fatal(err)
	}
	adminCtx, adminCancel := context.WithTimeout(f.ctx(f.admin), 2*time.Second)
	defer adminCancel()
	if _, err = f.service.Controls(adminCtx, f.admin, 20, ""); err != nil {
		t.Fatal(err)
	}
	if err = f.service.adminRead(adminCtx, f.admin); err != nil {
		t.Fatal(err)
	}
	if n := f.service.config.ReadDatabase.Stats().InUse; n != 0 {
		t.Fatalf("reader retained: %d", n)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec("DELETE FROM sessions WHERE user_id=?", f.user); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.GetQueue(ctx, f.user); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("revoked queue: %v", err)
	}
}
