package donation

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func sourceReadFixture(t *testing.T) (*donationTestEnv, int64, string) {
	t.Helper()
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "source-reader", &five, false)
	owner := e.seedUser(t, "source-donor", nil, false)
	_, tagged := e.seedEndpointKeyAt(t, owner, 'A', "https://tagged.example.test/v1")
	_, first := e.seedEndpointKeyAt(t, owner, 'B', "https://untagged.example.test/v1")
	_, second := e.seedEndpointKeyAt(t, owner, 'C', "https://untagged.example.test/v1")
	mixed := approvedResetDonation(t, e, owner, tagged, first, second)
	_, unrelated := e.seedEndpointKeyAt(t, owner, 'D', "https://other.example.test/v1")
	e.createDonationWithSeed(t, owner, 'u', unrelated)
	if _, err := e.store.DB().Exec(`INSERT INTO admin_endpoint_tags(base_url,tag) VALUES('https://tagged.example.test/v1','user_gateway')`); err != nil {
		t.Fatal(err)
	}
	p := authz.DefaultStewardPermissions(5)
	p.DonationScope = authz.DonationScopeEnhanced
	writeDonationPolicy(t, e, actor, p)
	var source string
	if err := e.store.DB().QueryRow(`SELECT nbi_donation_source(mainstream_channel_id,connector_type,canonical_base_url) FROM donation_keys WHERE id=?`, parseTestID(t, mixed.Keys[1].ID)).Scan(&source); err != nil {
		t.Fatal(err)
	}
	return e, actor, source
}

func TestSourceReadsReleaseWriterAndKeepAuthorizedSnapshot(t *testing.T) {
	for _, keys := range []bool{false, true} {
		name := "sources"
		if keys {
			name = "keys"
		}
		t.Run(name, func(t *testing.T) {
			e, actor, source := sourceReadFixture(t)
			ctx := authz.WithStewardPermission(t.Context(), "donations.manage_keys")
			type result struct {
				total string
				rows  int
				err   error
			}
			read := func(ctx context.Context) result {
				if keys {
					out, err := e.service.SourceKeysStewardPage(ctx, actor, source, SourceFilter{Scope: "all"}, pagination.Default())
					if err != nil {
						return result{err: err}
					}
					for _, key := range out.Data {
						if key.EndpointKeyID != nil {
							return result{err: errors.New("source projection exposed physical identity")}
						}
					}
					return result{total: out.Pagination.TotalItems, rows: len(out.Data)}
				}
				out, err := e.service.SourcesStewardPage(ctx, actor, SourceFilter{Scope: "all"}, pagination.Default())
				if err != nil {
					return result{err: err}
				}
				return result{total: out.Pagination.TotalItems, rows: len(out.Data)}
			}
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			resume := func() { releaseOnce.Do(func() { close(release) }) }
			originalNow := e.service.now
			// nowUnix runs after live authorization has established the snapshot,
			// before either count or page queries execute.
			e.service.now = func() time.Time {
				close(entered)
				<-release
				return originalNow()
			}
			done := make(chan result, 1)
			go func() {
				defer close(finished)
				done <- read(ctx)
			}()
			defer func() { resume(); <-finished }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("source reader did not reach its snapshot")
			}
			if e.store.ReadDB().Stats().InUse != 1 || e.store.DB().Stats().InUse != 0 {
				t.Fatal("source read occupied the writer instead of the reader pool")
			}
			writeCtx, cancel := context.WithTimeout(t.Context(), time.Second)
			_, err := e.store.DB().ExecContext(writeCtx, `DELETE FROM admin_endpoint_tags`)
			cancel()
			if err != nil {
				t.Fatalf("writer could not commit while source read was active: %v", err)
			}
			resume()
			got := <-done
			<-finished
			e.service.now = originalNow
			if got.err != nil || got.total != "2" || got.rows != 2 {
				t.Fatalf("count/page did not retain the authorized snapshot: %+v", got)
			}
			got = read(ctx)
			if got.err != nil || got.total != "0" || got.rows != 0 {
				t.Fatalf("new source read ignored tag revocation: %+v", got)
			}
			p := authz.DefaultStewardPermissions(5)
			p.DonationScope = authz.DonationScopeEnhanced
			p.Permissions["donations.manage_keys"] = false
			writeDonationPolicy(t, e, actor, p)
			if got = read(ctx); !errors.Is(got.err, authz.ErrPermissionDenied) {
				t.Fatalf("new source read ignored action revocation: %v", got.err)
			}
			cancelled, stop := context.WithCancel(ctx)
			stop()
			if got = read(cancelled); got.err == nil {
				t.Fatal("cancelled source read succeeded")
			}
		})
	}
}

func TestEnhancedSourceReadSelectionMatchesSharedSelector(t *testing.T) {
	e, actor, _ := sourceReadFixture(t)
	scope := charityscope.Scope{ActorID: actor, ActorLevel: 5, DonationScope: authz.DonationScopeEnhanced}
	readIDs := func(query string, args []any) []int64 {
		t.Helper()
		rows, err := e.store.ReadDB().Query(`SELECT id FROM (`+query+`) ORDER BY id`, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	modelScope := scope
	modelScope.ModelID = controlModel(t, e, "source-filter-model", true)
	for _, selectedScope := range []charityscope.Scope{scope, modelScope} {
		for _, source := range []*sourceIdentity{nil, {connector: "openai-compatible", url: "https://untagged.example.test/v1"}} {
			for _, filter := range []SourceFilter{
				{Scope: "all"}, {Scope: "active", Query: "untagged"}, {Scope: "all", Handling: "pending"},
				{Scope: "all", Idle: "yes", KeyState: "available"}, {Scope: "all", Idle: "no"}, {Scope: "all", Query: "' OR 1=1 --"},
			} {
				filter.maintenanceScope = selectedScope
				before, beforeArgs := sourceSelectionSQL(filter, source, e.clock.Load())
				after, afterArgs := sourceReadSelectionSQL(filter, source, e.clock.Load())
				want, got := readIDs(before, beforeArgs), readIDs(after, afterArgs)
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("source=%+v filter=%+v want=%v got=%v", source, filter, want, got)
				}
			}
		}
	}
	query, args := sourceReadSelectionSQL(SourceFilter{Scope: "all", maintenanceScope: scope}, nil, e.clock.Load())
	rows, err := e.store.ReadDB().Query(`EXPLAIN QUERY PLAN SELECT `+sourceGroupColumns+`,COUNT(*) FROM (`+query+`) GROUP BY `+sourceGroupColumns, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte(10)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"MATERIALIZE source_donations", "MATERIALIZE tagged_source_donations", "idx_donation_keys_donation", "idx_donation_keys_source_page"} {
		if !strings.Contains(plan.String(), part) {
			t.Fatalf("missing bounded donation lookup %q: %s", part, plan.String())
		}
	}
	t.Log(plan.String())
}

// A read-only source transaction must remain physically unable to mutate.
func TestSourceReadTransactionIsQueryOnly(t *testing.T) {
	e, actor, _ := sourceReadFixture(t)
	ctx := authz.WithStewardPermission(t.Context(), "donations.manage_keys")
	tx, _, err := e.service.beginSourceReadTx(ctx, reviewerSteward, actor)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var queryOnly int
	if err := tx.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil || queryOnly != 1 {
		t.Fatal(queryOnly, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_endpoint_tags`); err == nil || errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("source reader accepted a write: %v", err)
	}
}
