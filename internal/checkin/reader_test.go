package checkin

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type statusReadAuthority func(context.Context, *sql.Tx, int64) error

func (f statusReadAuthority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	return f(ctx, tx, user)
}

func TestStatusReaderContentionSnapshotAndRevocation(t *testing.T) {
	f := newCheckinFixture(t)
	f.configure(db.CheckinModeEnabled, "0", 1000, 1000, 9000)
	user := f.seedUser("reader")
	f.fundUser(user, 3500)
	writer, err := f.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	status, err := f.service.Status(ctx, user)
	if err != nil || !status.Enabled || status.Balance != "3.5" {
		t.Fatalf("writer-held status: %+v %v", status, err)
	}
	if n := f.store.ReadDB().Stats().InUse; n != 0 {
		t.Fatalf("reader retained after return: %d", n)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}

	f.service.finalAuth = statusReadAuthority(func(ctx context.Context, tx *sql.Tx, id int64) error {
		if err := f.authorizer.AuthorizeUserMutation(ctx, tx, id); err != nil {
			return err
		}
		// Authorization has fixed this read snapshot; the next read must see the change.
		if _, err := f.database.ExecContext(ctx, "UPDATE users SET is_banned=1 WHERE id=?", id); err != nil {
			return err
		}
		_, err := f.database.ExecContext(ctx, "UPDATE site_config SET value='disabled' WHERE key='checkin_mode'")
		return err
	})
	status, err = f.service.Status(ctx, user)
	if err != nil || !status.Enabled || status.Balance != "3.5" {
		t.Fatalf("mixed read snapshot: %+v %v", status, err)
	}
	if _, err = f.service.Status(ctx, user); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked user read: %v", err)
	}
	f.service.finalAuth = f.authorizer
	if _, err = f.service.Checkin(ctx, user); !errors.Is(err, ErrForbidden) && !errors.Is(err, resources.ErrForbidden) {
		t.Fatalf("revoked user mutation: %v", err)
	}
}
