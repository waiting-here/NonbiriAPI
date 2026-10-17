package charityrouting

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// The driver probe counts SQL calls and can pause after the authorization
// row establishes the SQLite snapshot. It is confined to temporary test DBs.
type browseProbe struct {
	driver.Driver
	queries    atomic.Int64
	afterLevel func()
}

type browseProbeConn struct {
	driver.Conn
	probe *browseProbe
}
type browseProbeRows struct {
	driver.Rows
	after func()
}

func (p *browseProbe) Open(name string) (driver.Conn, error) {
	c, err := p.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &browseProbeConn{Conn: c, probe: p}, nil
}

func (c *browseProbeConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
}

func (c *browseProbeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.probe.queries.Add(1)
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err == nil && c.probe.afterLevel != nil && strings.Contains(query, "COALESCE(level,auto_level),auto_level,is_admin") {
		return &browseProbeRows{Rows: rows, after: c.probe.afterLevel}, nil
	}
	return rows, err
}

func (r *browseProbeRows) Next(values []driver.Value) error {
	err := r.Rows.Next(values)
	if err == nil {
		r.after()
	}
	return err
}

var browseProbeSequence atomic.Int64

func attachBrowseProbe(t *testing.T, service *Service, afterLevel func()) *browseProbe {
	t.Helper()
	var sequence int
	var name, path string
	if err := service.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	probe := &browseProbe{Driver: service.readDB.Driver(), afterLevel: afterLevel}
	driverName := fmt.Sprintf("browse-probe-%d", browseProbeSequence.Add(1))
	sql.Register(driverName, probe)
	slash := filepath.ToSlash(path)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	uri := url.URL{Scheme: "file", Path: slash, RawQuery: "mode=ro&_pragma=query_only(1)"}
	reader, err := sql.Open(driverName, uri.String())
	if err != nil {
		t.Fatal(err)
	}
	reader.SetMaxOpenConns(2)
	reader.SetMaxIdleConns(2)
	t.Cleanup(func() { _ = reader.Close() })
	service.readDB = reader
	return probe
}

func browseCalls(service *Service, user int64) map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"models": func(ctx context.Context) error {
			_, err := service.ListAvailableModels(ctx, user, routingTestNow, MaxAvailableModels)
			return err
		},
		"capability": func(ctx context.Context) error { _, err := service.Capability(ctx, user, routingTestNow); return err },
		"catalog": func(ctx context.Context) error {
			_, err := service.Catalog(ctx, user, CatalogFilter{}, pagination.Request{Page: 1, Size: 100})
			return err
		},
	}
}

func TestBrowseSQLCallsStayBoundedAsModelsGrow(t *testing.T) {
	counts := map[string]int64{}
	for _, count := range []int{1, 50} {
		service, user := newCatalogSuccessFixture(t, count, 0)
		probe := attachBrowseProbe(t, service, nil)
		for name, call := range browseCalls(service, user) {
			before := probe.queries.Load()
			if err := call(t.Context()); err != nil {
				t.Fatal(name, err)
			}
			queries := probe.queries.Load() - before
			if count == 1 {
				counts[name] = queries
			} else if queries != counts[name] {
				t.Fatalf("%s queries grew: %d -> %d", name, counts[name], queries)
			}
			if queries > 8 {
				t.Fatalf("%s: %d SQL calls", name, queries)
			}
			t.Logf("models=%d operation=%s SQL calls=%d", count, name, queries)
		}
	}
}

func TestBrowseWaitCancellationAndClosedPoolReturnErrors(t *testing.T) {
	service, user := newCatalogSuccessFixture(t, 1, 0)
	reader := service.readDB
	first, err := reader.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := reader.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for name, call := range browseCalls(service, user) {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		err := call(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	_ = first.Close()
	_ = second.Close()
	if n := reader.Stats().InUse; n != 0 {
		t.Fatalf("leaked connections: %d", n)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	for name, call := range browseCalls(service, user) {
		if err := call(t.Context()); err == nil {
			t.Fatalf("%s hid a closed pool", name)
		}
	}
}

func TestCapabilityUsesOneSnapshotDuringConcurrentCommit(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	model := env.createModel(t, 'a')
	id, _ := parsePositiveID(model.ID)
	_, key, _ := env.seedCandidate(t, owner, 'k', "snapshot-model")
	if _, err := env.service.AddBindingsAdmin(t.Context(), id, routingMutation(t, 'b', http.MethodPost, routeAdminBindingBatch, []int64{id}, map[string]any{"bind": true}), BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(key), UpstreamModelID: "snapshot-model"}}}); err != nil {
		t.Fatal(err)
	}
	read, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	attachBrowseProbe(t, env.service, func() { once.Do(func() { close(read); <-resume }) })
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	type response struct {
		value Capability
		err   error
	}
	done := make(chan response, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	go func() {
		value, err := env.service.Capability(ctx, env.caller, routingTestNow)
		done <- response{value, err}
	}()
	select {
	case <-read:
	case <-ctx.Done():
		t.Fatal("snapshot did not start")
	}
	if _, err := env.store.DB().ExecContext(ctx, `UPDATE charity_models SET enabled=0 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	close(resume)
	first := <-done
	if first.err != nil || first.value.State != "available" || len(first.value.Models) != 1 {
		t.Fatalf("mixed snapshot: %+v %v", first.value, first.err)
	}
	next, err := env.service.Capability(ctx, env.caller, routingTestNow)
	if err != nil || next.State != "no_models" {
		t.Fatalf("committed change was not visible: %+v %v", next, err)
	}
}

func TestCallerListsKeepSixLevelsAndDonorIdentitySeparate(t *testing.T) {
	env := newRoutingTestEnv(t)
	admin := env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	_, key, _ := env.seedCandidate(t, owner, 'k', "upstream")
	for level := 1; level <= 6; level++ {
		model := catalogCreate(t, env, byte('A'+level), fmt.Sprintf("level-%d", level), []int{level}, true)
		catalogBind(t, env, model, key, byte('a'+level))
	}
	for level := 1; level <= 6; level++ {
		if _, err := env.store.DB().Exec(`UPDATE users SET level=? WHERE id IN (?,?)`, level, owner, env.caller); err != nil {
			t.Fatal(err)
		}
		for _, user := range []int64{owner, env.caller} {
			want := 1
			if user == owner && level != 6 {
				want = 0
			}
			models, err := env.service.ListAvailableModels(t.Context(), user, routingTestNow, MaxAvailableModels)
			if err != nil || len(models) != want {
				t.Fatalf("level %d owner=%v models=%+v err=%v", level, user == owner, models, err)
			}
			capability, err := env.service.Capability(t.Context(), user, routingTestNow)
			if err != nil || len(capability.Models) != want {
				t.Fatalf("level %d owner=%v capability=%+v err=%v", level, user == owner, capability, err)
			}
			catalog, err := env.service.Catalog(t.Context(), user, CatalogFilter{}, pagination.Default())
			if err != nil || len(catalog.Models) != 6 {
				t.Fatalf("catalog: %+v %v", catalog, err)
			}
			for _, model := range catalog.Models {
				if !model.CurrentlyAvailable {
					t.Fatal("global supply depended on caller ownership")
				}
			}
		}
	}
	for name, call := range browseCalls(env.service, admin) {
		if err := call(t.Context()); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("administrator %s: %v", name, err)
		}
	}
}
