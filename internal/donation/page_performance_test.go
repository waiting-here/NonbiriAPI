package donation

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"modernc.org/sqlite"
)

func pagePerformanceFixture(t *testing.T) (*donationTestEnv, int64) {
	t.Helper()
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "page-performance-owner", nil, false)
	endpoints, keys := [3]int64{}, [3]int64{}
	for i := range endpoints {
		endpoints[i], keys[i] = e.seedEndpointKeyAt(t, owner, byte('a'+i), fmt.Sprintf("https://source-%d.example.test/v1", i))
	}
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := range 100 {
		input := CreateInput{Description: "page performance sample", OwnershipAuthorized: true}
		for source := range endpoints {
			key := keys[source]
			if i > 0 {
				fingerprint := make([]byte, 32)
				binary.BigEndian.PutUint64(fingerprint[24:], uint64(1000+i*3+source))
				contextID := make([]byte, 16)
				binary.BigEndian.PutUint64(contextID[8:], uint64(1000+i*3+source))
				secretContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
				if err != nil {
					t.Fatal(err)
				}
				encrypted, err := e.vault.SealForGenerationTwoContext([]byte(fmt.Sprintf("sample-key-%d-%d", i, source)), secretContext)
				if err != nil {
					t.Fatal(err)
				}
				stored, err := tx.ExecContext(t.Context(), "INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,?,'openai-compatible',?,?)", contextID, fmt.Sprintf("https://source-%d.example.test/v1", source), encrypted, e.clock.Load())
				if err != nil {
					t.Fatal(err)
				}
				secretID, err := stored.LastInsertId()
				if err != nil {
					t.Fatal(err)
				}
				identity, err := testseed.InsertEndpointKeyTx(t.Context(), tx, testseed.KeyInput{EndpointID: endpoints[source], SecretRefID: secretID, Fingerprint: fingerprint, DisplayHead: "head", DisplayTail: "tail", At: e.clock.Load()})
				if err != nil {
					t.Fatal(err)
				}
				key = identity.KeyID
			}
			input.Keys = append(input.Keys, CreateKeyInput{EndpointKeyID: key})
		}
		if _, err := e.service.CreateInTransaction(t.Context(), tx, owner, input); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return e, owner
}

func addUnrelatedPageHistory(t *testing.T, e *donationTestEnv, count int) {
	t.Helper()
	owner := e.seedUser(t, "unrelated-page-history", nil, false)
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := range count {
		result, err := tx.ExecContext(t.Context(), `INSERT INTO donations(user_id,status,revision,description,created_at,updated_at,terminal_at) VALUES(?,'deleted',1,'unrelated history',?,?,?)`, owner, e.clock.Load(), e.clock.Load(), e.clock.Load())
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO donation_handling(donation_id,state,revision,closed_at,closed_reason,created_at,updated_at) VALUES(?,'closed',1,?,'deleted',?,?)`, id, e.clock.Load(), e.clock.Load(), e.clock.Load()); err != nil {
			t.Fatal(err)
		}
		for source := range 3 {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO donation_keys(donation_id,canonical_base_url,source_endpoint_key_id,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,failure_streak,streak_generation,next_claim_seq,next_fold_seq,enabled,ended_at,ended_reason,report_match_until,created_at,updated_at,report_fingerprint) VALUES(?,?,?,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),nbi_u128(1),nbi_u128(1),nbi_u128(1),0,?,'terminated',?,?,?,zeroblob(32))`, id, fmt.Sprintf("https://historical-%d.example.test/v1", source), 100000+i*3+source, e.clock.Load(), e.clock.Load()+reportMatchRetention, e.clock.Load(), e.clock.Load()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

type pageCountingConnector struct {
	driver.Connector
	queries *atomic.Int64
}
type pageCountingConnection struct {
	driver.Conn
	queries *atomic.Int64
}

func (c pageCountingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &pageCountingConnection{Conn: conn, queries: c.queries}, nil
}
func (c *pageCountingConnection) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
}
func (c *pageCountingConnection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.queries.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

// The separate counting pass uses the actual SQLite driver without inserting
// instrumentation into the production pool or the measured timing loop.
func countPageQueries(t *testing.T, e *donationTestEnv, owner int64) int64 {
	t.Helper()
	var sequence int
	var name, path string
	if err := e.store.DB().QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path}
	query := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)"}}
	uri.RawQuery = query.Encode()
	connector, err := sqlite.NewConnector(uri.String())
	if err != nil {
		t.Fatal(err)
	}
	var queries atomic.Int64
	counted := sql.OpenDB(pageCountingConnector{Connector: connector, queries: &queries})
	counted.SetMaxOpenConns(1)
	defer counted.Close()
	service := *e.service
	service.db, service.readDB = counted, counted
	out, err := service.DonationsAdminPage(t.Context(), ManagementFilter{UserID: strconv.FormatInt(owner, 10)}, pagination.Request{Page: 1, Size: 100})
	if err != nil || len(out.Data) != 100 {
		t.Fatalf("counted page: rows=%d error=%v", len(out.Data), err)
	}
	return queries.Load()
}

func TestDonationPagePerformanceSample(t *testing.T) {
	if os.Getenv("NONBIRI_PERFORMANCE_SAMPLE") != "1" {
		t.Skip("explicit bounded performance sample")
	}
	e, owner := pagePerformanceFixture(t)
	for _, unrelated := range []int{0, 2000} {
		if unrelated > 0 {
			addUnrelatedPageHistory(t, e, unrelated)
		}
		filter := ManagementFilter{UserID: strconv.FormatInt(owner, 10)}
		page := pagination.Request{Page: 1, Size: 100}
		sample, err := e.service.DonationsAdminPage(t.Context(), filter, page)
		if err != nil || len(sample.Data) != 100 || sample.Pagination.TotalItems != "100" {
			t.Fatalf("page: rows=%d error=%v", len(sample.Data), err)
		}
		for _, row := range sample.Data {
			if row.KeyCount != "3" || row.SourceCount != "3" || len(row.Sources) != 3 {
				t.Fatalf("invalid sample: %+v", row)
			}
		}
		result := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				out, err := e.service.DonationsAdminPage(context.Background(), filter, page)
				if err != nil || len(out.Data) != 100 {
					b.Fatalf("page: rows=%d error=%v", len(out.Data), err)
				}
			}
		})
		t.Logf("page=100 sources=3 unrelated_donations=%d unrelated_keys=%d sql=%d %s %s", unrelated, unrelated*3, countPageQueries(t, e, owner), result.String(), result.MemString())
	}
}
