package adapters

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/resources"

	_ "modernc.org/sqlite"
)

type adapterTestState struct {
	expectedTx        *sql.Tx
	expectedNow       int64
	expectedLimit     int
	exportCalls       []string
	deleteCalls       []string
	deleteFailure     string
	resourceExportErr error
	finalizer         *adapterTestFinalizer
}

func (state *adapterTestState) observeExport(ctx context.Context, tx *sql.Tx, domain string, now int64, limit int) (string, error) {
	if tx != state.expectedTx || now != state.expectedNow || limit != state.expectedLimit {
		return "", errors.New("adapter did not preserve shared export request")
	}
	var value string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM snapshot_value`).Scan(&value); err != nil {
		return "", err
	}
	state.exportCalls = append(state.exportCalls, domain)
	return value, nil
}

func (state *adapterTestState) prepareDelete(ctx context.Context, tx *sql.Tx, domain string, now int64) error {
	if tx != state.expectedTx || now != state.expectedNow {
		return errors.New("adapter did not preserve shared delete request")
	}
	state.deleteCalls = append(state.deleteCalls, domain)
	if _, err := tx.ExecContext(ctx, `INSERT INTO delete_steps(domain_name) VALUES(?)`, domain); err != nil {
		return err
	}
	if state.deleteFailure == domain {
		return issues.ErrUnavailable
	}
	return nil
}

type adapterTestFinalizer struct {
	committed bool
	aborted   bool
}

func (finalizer *adapterTestFinalizer) Commit() bool {
	if finalizer == nil || finalizer.committed || finalizer.aborted {
		return false
	}
	finalizer.committed = true
	return true
}

func (finalizer *adapterTestFinalizer) Abort() bool {
	if finalizer == nil || finalizer.committed || finalizer.aborted {
		return false
	}
	finalizer.aborted = true
	return true
}

type adapterTestIdentity struct{ state *adapterTestState }

func (source adapterTestIdentity) ExportLifecycleIdentity(
	ctx context.Context,
	tx *sql.Tx,
	_ int64,
	now int64,
	limit int,
) (auth.LifecycleIdentity, auth.LifecycleUsage, error) {
	value, err := source.state.observeExport(ctx, tx, "identity", now, limit)
	return auth.LifecycleIdentity{ID: "1", Username: value}, auth.LifecycleUsage{TotalRequests: value}, err
}

func (source adapterTestIdentity) PrepareLifecycleAccountDeletion(
	ctx context.Context,
	tx *sql.Tx,
	_ int64,
	now int64,
) (auth.LifecycleDeletionFinalizer, error) {
	if err := source.state.prepareDelete(ctx, tx, "identity", now); err != nil {
		return nil, err
	}
	return source.state.finalizer, nil
}

type adapterTestResources struct{ state *adapterTestState }

func (source adapterTestResources) ExportLifecycleResources(
	ctx context.Context,
	tx *sql.Tx,
	_ int64,
	now int64,
	limit int,
) (resources.LifecycleResourceExport, error) {
	value, err := source.state.observeExport(ctx, tx, "resources", now, limit)
	if err == nil && source.state.resourceExportErr != nil {
		err = source.state.resourceExportErr
	}
	return resources.LifecycleResourceExport{
		Endpoints: []resources.LifecycleEndpoint{{
			ID: "2", Note: value,
			Origin: resources.EndpointOrigin{Kind: "mainstream", ChannelID: "mch_safe", Name: "Safe channel"},
			Keys:   []resources.LifecycleEndpointKey{},
		}},
		CatalogPairs: []resources.LifecycleCatalogPair{}, Models: []resources.LifecycleModel{},
	}, err
}

func (source adapterTestResources) PrepareLifecycleAccountDeletion(
	ctx context.Context,
	tx *sql.Tx,
	_ int64,
	now int64,
) error {
	return source.state.prepareDelete(ctx, tx, "resources", now)
}

type adapterTestIssues struct{ state *adapterTestState }

func (source adapterTestIssues) ExportLifecycleIssues(
	ctx context.Context,
	tx *sql.Tx,
	_ int64,
	now int64,
	limit int,
) ([]issues.LifecycleIssue, error) {
	value, err := source.state.observeExport(ctx, tx, "issues", now, limit)
	return []issues.LifecycleIssue{{ID: "iss_safe", SafeDetail: value}}, err
}

func (source adapterTestIssues) PrepareAccountDeletion(
	ctx context.Context,
	tx *sql.Tx,
	_ int64,
	now int64,
) error {
	return source.state.prepareDelete(ctx, tx, "issues", now)
}

type adapterTestLogs struct{ state *adapterTestState }

func (source adapterTestLogs) ExportLifecycleSummary(
	ctx context.Context,
	tx *sql.Tx,
	_ int64,
	now int64,
	limit int,
) (logapi.LifecycleLogSummary, error) {
	value, err := source.state.observeExport(ctx, tx, "logs", now, limit)
	return logapi.LifecycleLogSummary{TotalLogs: value}, err
}

func (source adapterTestLogs) PrepareLifecycleAccountDeletion(
	ctx context.Context,
	tx *sql.Tx,
	_ int64,
	now int64,
) error {
	return source.state.prepareDelete(ctx, tx, "logs", now)
}

func newAdapterTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`CREATE TABLE snapshot_value(value TEXT NOT NULL)`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO snapshot_value(value) VALUES('shared-snapshot')`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE delete_steps(domain_name TEXT NOT NULL)`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func newAdapterTestSubject(state *adapterTestState) *AccountResources {
	return &AccountResources{
		identity: adapterTestIdentity{state}, resources: adapterTestResources{state},
		issues: adapterTestIssues{state}, logs: adapterTestLogs{state},
	}
}

func TestAccountResourcesUsesOneFrozenSnapshot(t *testing.T) {
	database := newAdapterTestDatabase(t)
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	state := &adapterTestState{expectedTx: tx, expectedNow: 1_700_000_123, expectedLimit: 7}
	adapter := newAdapterTestSubject(state)
	request := lifecycle.ExportRequest{UserID: 9, DecisionNow: state.expectedNow, Limit: state.expectedLimit}
	user, usage, logs, err := adapter.ExportIdentity(context.Background(), tx, request)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	endpoints, _, _, _, err := adapter.ExportResources(context.Background(), tx, request)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	exportedIssues, err := adapter.ExportIssues(context.Background(), tx, request)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"identity", "logs", "resources", "issues"}
	if len(state.exportCalls) != len(wantCalls) {
		t.Fatalf("export calls=%v", state.exportCalls)
	}
	for index := range wantCalls {
		if state.exportCalls[index] != wantCalls[index] {
			t.Fatalf("export calls=%v", state.exportCalls)
		}
	}
	if user.Username != "shared-snapshot" || usage.TotalRequests != "shared-snapshot" ||
		logs.TotalLogs != "shared-snapshot" || len(endpoints) != 1 || endpoints[0].Note != "shared-snapshot" ||
		endpoints[0].Origin != (lifecycle.EndpointOriginExport{
			Kind: "mainstream", ChannelID: "mch_safe", Name: "Safe channel",
		}) ||
		len(exportedIssues) != 1 || exportedIssues[0].SafeDetail != "shared-snapshot" {
		t.Fatalf("snapshot outputs user=%+v usage=%+v logs=%+v endpoints=%+v issues=%+v",
			user, usage, logs, endpoints, exportedIssues)
	}
}

func TestAccountResourcesTranslatesLimit(t *testing.T) {
	database := newAdapterTestDatabase(t)
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	state := &adapterTestState{
		expectedTx: tx, expectedNow: 1_700_000_456, expectedLimit: 3,
		resourceExportErr: resources.ErrResourceLimit,
	}
	adapter := newAdapterTestSubject(state)
	request := lifecycle.ExportRequest{UserID: 12, DecisionNow: state.expectedNow, Limit: state.expectedLimit}
	if _, _, _, _, err := adapter.ExportResources(context.Background(), tx, request); !errors.Is(err, lifecycle.ErrTooLarge) {
		_ = tx.Rollback()
		t.Fatalf("resource limit translation=%v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
