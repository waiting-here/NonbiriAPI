package requestadaptation

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"modernc.org/sqlite"
)

type queryCounterDriver struct{ count *atomic.Int64 }
type queryCounterConn struct {
	driver.Conn
	count *atomic.Int64
}

func (d queryCounterDriver) Open(name string) (driver.Conn, error) {
	conn, err := (&sqlite.Driver{}).Open(name)
	return &queryCounterConn{conn, d.count}, err
}
func (c *queryCounterConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c *queryCounterConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}
func (c *queryCounterConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "FROM request_adaptations") {
		c.count.Add(1)
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

type openHookCodec struct {
	secret.GenerationTwoContextCodec
	afterFirst func()
}

func (c *openHookCodec) OpenForGenerationTwoContext(value string, key secret.GenerationTwoEndpointKeyContext) ([]byte, error) {
	valueBytes, err := c.GenerationTwoContextCodec.OpenForGenerationTwoContext(value, key)
	if c.afterFirst != nil {
		hook := c.afterFirst
		c.afterFirst = nil
		hook()
	}
	return valueBytes, err
}

var queryCounterNames atomic.Int64

func TestLoadManyBatchesScopedIDsAtOneSnapshot(t *testing.T) {
	var count atomic.Int64
	driverName := fmt.Sprintf("adaptation-query-counter-%d", queryCounterNames.Add(1))
	sql.Register(driverName, queryCounterDriver{&count})
	store, database := testStoreDriver(t, driverName, filepath.Join(t.TempDir(), "adaptation.sqlite"))
	if _, err := database.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	refs := []Ref{{ScopeEndpoint, 1}, {ScopePersonalModel, 1}, {ScopeCharityModel, 1}, {ScopeBinding, 1}}
	for _, ref := range refs {
		doc := Empty(ref.Scope)
		doc.FixedHeaders.Mode = ModeReplace
		doc.FixedHeaders.Values["X-Value"] = string(ref.Scope)
		tx, err := database.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.SaveTx(t.Context(), tx, ref, 0, doc, 1, 1); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	// A writer commits while the reader is decrypting its first row. Every
	// subsequent row must still belong to the original SQL snapshot.
	store.codec = &openHookCodec{GenerationTwoContextCodec: store.codec, afterFirst: func() {
		if _, err := database.Exec("UPDATE request_adaptations SET revision=2 WHERE scope='binding'"); err != nil {
			t.Fatal(err)
		}
	}}
	refs = append(refs, refs[0], Ref{ScopeEndpoint, 2})
	count.Store(0)
	got, err := store.LoadMany(t.Context(), refs)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, snapshot := range got {
			snapshot.Clear()
		}
	}()
	if count.Load() != 1 || len(got) != 5 {
		t.Fatalf("batch used %d queries for %d unique references", count.Load(), len(got))
	}
	for _, ref := range refs[:4] {
		if got[ref].Revision != "1" || got[ref].Document.FixedHeaders.Values["X-Value"] != string(ref.Scope) {
			t.Fatalf("scope or snapshot changed for %v", ref)
		}
	}
	if got[Ref{ScopeEndpoint, 2}].Revision != "0" {
		t.Fatal("missing configuration did not retain the empty default")
	}
	if _, err := store.LoadMany(t.Context(), []Ref{{ScopeEndpoint, 1}, {Scope: "unknown", ID: 1}}); !errors.Is(err, ErrInvalid) || count.Load() != 1 {
		t.Fatal("invalid references reached the database", err)
	}
	if _, err := database.Exec("UPDATE request_adaptations SET endpoint_id=3 WHERE scope='endpoint'"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.LoadMany(t.Context(), []Ref{{ScopeEndpoint, 3}}); !errors.Is(err, ErrUnavailable) || got != nil {
		t.Fatal("transplanted envelope accepted in batch", err)
	}
}

func BenchmarkLoadManyEmpty(b *testing.B) {
	store, _ := testStore(b)
	for _, count := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("candidates=%d", count), func(b *testing.B) {
			refs := make([]Ref, count)
			for i := range refs {
				refs[i] = Ref{ScopeEndpoint, int64(i + 1)}
			}
			b.ReportAllocs()
			for b.Loop() {
				snapshots, err := store.LoadMany(b.Context(), refs)
				if err != nil {
					b.Fatal(err)
				}
				for _, snapshot := range snapshots {
					snapshot.Clear()
				}
			}
		})
	}
}
