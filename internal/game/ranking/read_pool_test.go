package ranking

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func newRankingReadFixture(t *testing.T) (*fixture, *db.Store) {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vault.Close() })
	path := filepath.Join(t.TempDir(), "ranking-reader.db")
	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f := &fixture{t: t, db: store.DB()}
	if err := f.db.QueryRow("SELECT started_at FROM game_statistics_epoch").Scan(&f.epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("UPDATE maintenance_state SET enabled=0 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	return f, store
}

type rankingReadAuthority struct {
	now           int64
	calls         int
	requireReader bool
	afterRead     func(int)
}

func (a *rankingReadAuthority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	var admin, banned bool
	var until sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT is_admin,is_banned,banned_until FROM users WHERE id=?", user).Scan(&admin, &banned, &until); err != nil {
		return err
	}
	if admin || banned && (!until.Valid || until.Int64 > a.now) {
		return resources.ErrForbidden
	}
	if a.requireReader {
		var readOnly int
		if err := tx.QueryRowContext(ctx, "PRAGMA query_only").Scan(&readOnly); err != nil {
			return err
		}
		if readOnly != 1 {
			return errors.New("ranking authority did not use reader")
		}
	}
	a.calls++
	if a.afterRead != nil {
		a.afterRead(a.calls)
	}
	return nil
}

func TestRankingReadSnapshotAllowsWriterAndRechecksPrivacy(t *testing.T) {
	f, store := newRankingReadFixture(t)
	viewer, other := f.user(true), f.user(true)
	amount := db.U128{}
	amount[15] = 1
	if _, err := f.db.Exec("UPDATE users SET donation_credit_mag=?,donation_credit_achieved_at=?,donation_credit_achieved_seq=? WHERE id IN (?,?)", db.EncodeU128(amount), f.epoch, db.EncodeU128(amount), viewer, other); err != nil {
		t.Fatal(err)
	}
	authority := &rankingReadAuthority{now: f.epoch + 100, requireReader: true}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	authority.afterRead = func(call int) {
		if call == 2 {
			close(entered)
			<-release
		}
	}
	service, err := NewWithReader(f.db, store.ReadDB(), authority, func() time.Time { return time.Unix(authority.now, 0) })
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		board Board
		err   error
	}
	done := make(chan result, 1)
	go func() {
		defer close(finished)
		board, err := service.Read(t.Context(), viewer, "charity", "history", pagination.Default())
		done <- result{board, err}
	}()
	defer func() { resume(); <-finished }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("final snapshot not reached")
	}
	if store.DB().Stats().InUse != 0 || store.ReadDB().Stats().InUse != 1 {
		t.Fatal("ranking held writer")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	_, err = f.db.ExecContext(ctx, "UPDATE users SET charity_profile_public=0 WHERE id=?", other)
	cancel()
	if err != nil {
		t.Fatal("writer blocked", err)
	}
	resume()
	got := <-done
	<-finished
	authority.afterRead = nil
	if got.err != nil || len(got.board.Rows) != 2 || got.board.Pagination.TotalItems != "2" {
		t.Fatal(got)
	}
	for _, row := range got.board.Rows {
		if row.Identity.Kind != "public" {
			t.Fatal("snapshot privacy changed", got.board)
		}
	}
	next, err := service.Read(t.Context(), viewer, "charity", "history", pagination.Default())
	if err != nil {
		t.Fatal(err)
	}
	private := 0
	for _, row := range next.Rows {
		if row.Identity.Kind == "anonymous" {
			private++
		}
	}
	if private != 1 {
		t.Fatal("new snapshot ignored privacy change", next)
	}
	if _, err = f.db.Exec("UPDATE users SET is_banned=1 WHERE id=?", viewer); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Read(t.Context(), viewer, "charity", "history", pagination.Default()); !errors.Is(err, resources.ErrForbidden) {
		t.Fatal("new snapshot ignored actor ban", err)
	}
}

func TestRankingReaderKeepsFinalAuthorizationAndCatchUp(t *testing.T) {
	t.Run("final authorization", func(t *testing.T) {
		f, store := newRankingReadFixture(t)
		viewer := f.user(true)
		authority := &rankingReadAuthority{now: f.epoch + 100, requireReader: true}
		authority.afterRead = func(call int) {
			if call == 1 {
				if _, err := f.db.Exec("UPDATE users SET is_banned=1 WHERE id=?", viewer); err != nil {
					t.Fatal(err)
				}
			}
		}
		service, err := NewWithReader(f.db, store.ReadDB(), authority, func() time.Time { return time.Unix(authority.now, 0) })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Read(t.Context(), viewer, "charity", "history", pagination.Default()); !errors.Is(err, resources.ErrForbidden) {
			t.Fatal("final authorization ignored ban", err)
		}
	})
	t.Run("catch-up writer", func(t *testing.T) {
		f, store := newRankingReadFixture(t)
		viewer := f.user(true)
		f.add(viewer, f.epoch, 100, 0, "fishing")
		authority := &rankingReadAuthority{now: f.epoch + week + 1, requireReader: true}
		service, err := NewWithReader(f.db, store.ReadDB(), authority, func() time.Time { return time.Unix(authority.now, 0) })
		if err != nil {
			t.Fatal(err)
		}
		var before, after int
		if err := f.db.QueryRow("SELECT total_changes()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		board, err := service.Read(t.Context(), viewer, "game_charity", "7d", pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRow("SELECT total_changes()").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after <= before || len(board.Rows) != 0 || authority.calls != 2 {
			t.Fatal("catch-up or gates changed", before, after, authority.calls, board)
		}
		legacy, err := New(f.db, authority, nil)
		if err != nil || legacy.readDB != f.db {
			t.Fatal("legacy constructor changed", err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := service.Read(cancelled, viewer, "game_charity", "7d", pagination.Default()); err == nil {
			t.Fatal("cancelled read succeeded")
		}
	})
}

func TestRankingReaderPerformanceSample(t *testing.T) {
	if os.Getenv("NONBIRI_PERFORMANCE_SAMPLE") != "1" {
		t.Skip("opt-in bounded performance sample")
	}
	for _, count := range []int{1000, 20000} {
		t.Run(fmt.Sprintf("users-%d", count), func(t *testing.T) {
			f, store := newRankingReadFixture(t)
			amount := db.U128{}
			amount[15] = 1
			_, err := f.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
INSERT INTO users(username,charity_profile_public,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at,donation_credit_achieved_at,donation_credit_achieved_seq)
SELECT 'ranking-sample-'||i,1,?,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,?,?+i,? FROM n`, count, db.EncodeU128(amount), f.epoch, f.epoch, f.epoch, db.EncodeU128(amount))
			if err != nil {
				t.Fatal(err)
			}
			var viewer int64
			if err := f.db.QueryRow("SELECT max(id) FROM users WHERE is_admin=0").Scan(&viewer); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"writer", "reader"} {
				reader := f.db
				if mode == "reader" {
					reader = store.ReadDB()
				}
				authority := &rankingReadAuthority{now: f.epoch + int64(count) + 1}
				service, err := NewWithReader(f.db, reader, authority, func() time.Time { return time.Unix(authority.now, 0) })
				if err != nil {
					t.Fatal(err)
				}
				result := testing.Benchmark(func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						board, err := service.Read(context.Background(), viewer, "charity", "history", pagination.Default())
						if err != nil || len(board.Rows) != 20 || board.Pagination.TotalItems != fmt.Sprint(count) {
							b.Fatalf("board=%+v err=%v", board, err)
						}
					}
				})
				t.Logf("users=%d pool=%s %s %s", count, mode, result.String(), result.MemString())
			}
		})
	}
}
