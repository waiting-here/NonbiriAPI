package logapi

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type logReadAuthFunc func(context.Context, *sql.Tx, string, int64) error

func (f logReadAuthFunc) AuthorizeLogRead(ctx context.Context, tx *sql.Tx, role string, id int64) error {
	return f(ctx, tx, role, id)
}

func newSeparateLogReaders(t *testing.T, authorize ReadAuthorizer) (*logFixture, *sql.DB) {
	t.Helper()
	f := newNumberedLogFixture(t)
	path := filepath.Join(t.TempDir(), "logs.db")
	if _, err := f.db.Exec(`VACUUM INTO ?`, path); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	writer.SetMaxOpenConns(1)
	if _, err := writer.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	reader.SetMaxOpenConns(1)
	f.db = writer
	f.repo, err = NewRepositoryWithReader(writer, reader, f.keys, authorize)
	if err != nil {
		t.Fatal(err)
	}
	f.repo.now = f.clock.Now
	return f, reader
}

func TestLogReadPoolDoesNotQueueBehindWriterAndKeepsHeldReadsWritable(t *testing.T) {
	authorizations := 0
	f, reader := newSeparateLogReaders(t, logReadAuthFunc(func(ctx context.Context, tx *sql.Tx, role string, id int64) error {
		authorizations++
		return authorizePageAccount(ctx, tx, role, id, 1_000_000)
	}))
	connection, err := f.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, filter := range []ListFilter{{Limit: 10}, {Page: &pagination.Request{Page: 1, Size: 10}}} {
		if _, err := f.repo.ListUser(ctx, logUserOne, filter); err != nil {
			t.Fatalf("user list queued behind writer: %v", err)
		}
		if _, err := f.repo.ListAdmin(ctx, filter); err != nil {
			t.Fatalf("admin list queued behind writer: %v", err)
		}
		if _, err := f.repo.ListSteward(ctx, 6, filter, mismatchStewardAuthorizer{}); err != nil {
			t.Fatalf("steward list queued behind writer: %v", err)
		}
	}
	if _, err := f.repo.GetUser(ctx, logUserOne, f.selfID, AttemptFilter{}); err != nil {
		t.Fatalf("user detail queued behind writer: %v", err)
	}
	if _, err := f.repo.ExportAdmin(ctx, ListFilter{}); err != nil {
		t.Fatalf("admin export queued behind writer: %v", err)
	}
	if _, err := f.repo.ExportUser(ctx, logUserOne, ListFilter{}); err != nil {
		t.Fatalf("user export queued behind writer: %v", err)
	}
	if _, err := f.repo.ExportSteward(ctx, 6, ListFilter{}, mismatchStewardAuthorizer{}); err != nil {
		t.Fatalf("steward export queued behind writer: %v", err)
	}
	if authorizations != 7 {
		t.Fatalf("final authority checks = %d, want 7", authorizations)
	}
	if _, err := reader.Exec(`UPDATE users SET username='forbidden'`); err == nil {
		t.Fatal("reader accepted a write")
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	f.mustExec(`CREATE TABLE held_read_audit(request_log_id INTEGER NOT NULL)`)
	if err := f.repo.AttachAdminHeldReadAuthorizer(logHeldAuditWriter{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.GetAdmin(ctx, f.selfID, AttemptFilter{}); err != nil {
		t.Fatalf("admin held-read audit lost writer: %v", err)
	}
	f.clock.Advance(40 * 24 * time.Hour)
	if _, err := f.repo.GetSteward(ctx, 6, f.selfID, AttemptFilter{}, mismatchStewardAuthorizer{}); err != nil {
		t.Fatalf("steward held-read audit lost writer: %v", err)
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM held_read_audit`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("held-read audit count = %d, %v", count, err)
	}
}

type logHeldAuditWriter struct{}

func (logHeldAuditWriter) AuthorizeHeldRequestLogRead(ctx context.Context, tx *sql.Tx, id, _ int64) (bool, error) {
	_, err := tx.ExecContext(ctx, `INSERT INTO held_read_audit(request_log_id) VALUES(?)`, id)
	return err == nil, err
}

func (w logHeldAuditWriter) AuthorizeStewardHeldRequestLogRead(ctx context.Context, tx *sql.Tx, _ int64, id, now int64) (bool, error) {
	return w.AuthorizeHeldRequestLogRead(ctx, tx, id, now)
}

func TestLogReaderRechecksAuthorityAfterConnectionWait(t *testing.T) {
	for _, denied := range []error{authz.ErrForbidden, authz.ErrMFARequired} {
		t.Run(denied.Error(), func(t *testing.T) {
			f, reader := newSeparateLogReaders(t, logReadAuthFunc(func(ctx context.Context, tx *sql.Tx, _ string, id int64) error {
				var banned int
				if err := tx.QueryRowContext(ctx, `SELECT is_banned FROM users WHERE id=?`, id).Scan(&banned); err != nil {
					return err
				}
				if banned != 0 {
					return denied
				}
				return nil
			}))
			connection, err := reader.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := f.repo.ListUser(ctx, logUserOne, ListFilter{Limit: 10})
				result <- err
			}()
			for reader.Stats().WaitCount == 0 {
				select {
				case err := <-result:
					t.Fatalf("reader never waited: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			f.mustExec(`UPDATE users SET is_banned=1 WHERE id=?`, logUserOne)
			if err := connection.Close(); err != nil {
				t.Fatal(err)
			}
			err = <-result
			want := denied
			if denied == authz.ErrForbidden {
				want = ErrForbidden
			}
			if !errors.Is(err, want) {
				t.Fatalf("read after revocation = %v, want %v", err, want)
			}
		})
	}
}
