package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type testFinalAuth struct {
	userErr       error
	adminErr      error
	freshAdminErr error
	mu            sync.Mutex
	userCalls     int
	adminCalls    int
	freshCalls    int
}

func (auth *testFinalAuth) AuthorizeFreshUser(_ context.Context, _ *sql.Tx, _ int64) error {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	auth.userCalls++
	return auth.userErr
}

func (auth *testFinalAuth) AuthorizeAdmin(_ context.Context, _ *sql.Tx, _ int64) error {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	auth.adminCalls++
	return auth.adminErr
}

func (auth *testFinalAuth) AuthorizeFreshAdmin(_ context.Context, _ *sql.Tx, _ int64) error {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	auth.freshCalls++
	return auth.freshAdminErr
}

type testCursorKeys struct{ err error }

func (keys testCursorKeys) DeriveGenerationTwoSubkey([]byte) ([]byte, error) {
	if keys.err != nil {
		return nil, keys.err
	}
	return []byte("0123456789abcdef0123456789abcdef"), nil
}

type testExportAdapter struct {
	mu               sync.Mutex
	calls            []string
	tx               *sql.Tx
	user             UserExport
	usage            UsageExport
	log              LogSummaryExport
	endpoints        []EndpointExport
	pairs            []CatalogPairExport
	models           []ModelExport
	callerKey        *CallerKeyExport
	issues           []IssueExport
	ledger           []LedgerEntryExport
	welfare          []WelfareExport
	thursday         []ThursdayExport
	donations        []DonationExport
	charity          CharityExport
	fishing          FishingExport
	linklink         LinkLinkExport
	rps              RPSExport
	errAt            string
	fishingFinalizer ExportFinalizer
	linkFinalizer    ExportFinalizer
	rpsFinalizer     ExportFinalizer
}

func (adapter *testExportAdapter) record(name string, tx *sql.Tx) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if name == "identity" {
		adapter.tx = tx
	} else if adapter.tx == nil {
		adapter.tx = tx
	} else if adapter.tx != tx {
		return ErrInvariant
	}
	adapter.calls = append(adapter.calls, name)
	if adapter.errAt == name {
		return errors.New("export failure")
	}
	return nil
}

func (adapter *testExportAdapter) ExportIdentity(_ context.Context, tx *sql.Tx, _ ExportRequest) (UserExport, UsageExport, LogSummaryExport, error) {
	err := adapter.record("identity", tx)
	return adapter.user, adapter.usage, adapter.log, err
}

func (adapter *testExportAdapter) ExportResources(_ context.Context, tx *sql.Tx, _ ExportRequest) ([]EndpointExport, []CatalogPairExport, []ModelExport, *CallerKeyExport, error) {
	err := adapter.record("resources", tx)
	return adapter.endpoints, adapter.pairs, adapter.models, adapter.callerKey, err
}

func (adapter *testExportAdapter) ExportIssues(_ context.Context, tx *sql.Tx, _ ExportRequest) ([]IssueExport, error) {
	return adapter.issues, adapter.record("issues", tx)
}

func (adapter *testExportAdapter) ExportLedger(_ context.Context, tx *sql.Tx, _ ExportRequest) ([]LedgerEntryExport, error) {
	return adapter.ledger, adapter.record("ledger", tx)
}

func (adapter *testExportAdapter) ExportActivities(_ context.Context, tx *sql.Tx, _ ExportRequest) ([]WelfareExport, []ThursdayExport, error) {
	err := adapter.record("activities", tx)
	return adapter.welfare, adapter.thursday, err
}

func (adapter *testExportAdapter) ExportDonations(_ context.Context, tx *sql.Tx, _ ExportRequest) ([]DonationExport, error) {
	return adapter.donations, adapter.record("donations", tx)
}

func (adapter *testExportAdapter) ExportCharity(_ context.Context, tx *sql.Tx, _ ExportRequest) (CharityExport, error) {
	return adapter.charity, adapter.record("charity", tx)
}

func (adapter *testExportAdapter) ExportFishing(_ context.Context, tx *sql.Tx, _ ExportRequest) (FishingExport, ExportFinalizer, error) {
	return adapter.fishing, adapter.fishingFinalizer, adapter.record("fishing", tx)
}

func (adapter *testExportAdapter) ExportLinkLink(_ context.Context, tx *sql.Tx, _ ExportRequest) (LinkLinkExport, ExportFinalizer, error) {
	return adapter.linklink, adapter.linkFinalizer, adapter.record("linklink", tx)
}

func (adapter *testExportAdapter) ExportRPS(_ context.Context, tx *sql.Tx, _ ExportRequest) (RPSExport, ExportFinalizer, error) {
	return adapter.rps, adapter.rpsFinalizer, adapter.record("rps", tx)
}

type testFinalizer struct {
	mu      sync.Mutex
	commits int
	aborts  int
}

func (finalizer *testFinalizer) Commit() bool {
	finalizer.mu.Lock()
	defer finalizer.mu.Unlock()
	finalizer.commits++
	return finalizer.commits == 1 && finalizer.aborts == 0
}

func (finalizer *testFinalizer) Abort() bool {
	finalizer.mu.Lock()
	defer finalizer.mu.Unlock()
	finalizer.aborts++
	return finalizer.aborts == 1 && finalizer.commits == 0
}

type testDeleteAdapter struct {
	name      string
	calls     *[]string
	finalizer DeleteFinalizer
	err       error
	update    func(context.Context, *sql.Tx, DeleteRequest) error
}

func (adapter testDeleteAdapter) PrepareDelete(ctx context.Context, tx *sql.Tx, request DeleteRequest) (DeleteFinalizer, error) {
	if adapter.calls != nil {
		*adapter.calls = append(*adapter.calls, adapter.name)
	}
	if adapter.update != nil {
		if err := adapter.update(ctx, tx, request); err != nil {
			return nil, err
		}
	}
	return adapter.finalizer, adapter.err
}

type testLedgerDelete struct {
	calls       *[]string
	operationID string
	err         error
	deleteUser  bool
}

func (ledger *testLedgerDelete) ZeroAndDeleteAccount(ctx context.Context, tx *sql.Tx, request DeleteRequest, operationID string) error {
	if ledger.calls != nil {
		*ledger.calls = append(*ledger.calls, "ledger")
	}
	ledger.operationID = operationID
	if ledger.err != nil {
		return ledger.err
	}
	if ledger.deleteUser {
		_, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, request.UserID)
		return err
	}
	return nil
}

type testRetirement struct {
	mu      sync.Mutex
	commits int
	aborts  int
}

func (retirement *testRetirement) Commit() bool {
	retirement.mu.Lock()
	defer retirement.mu.Unlock()
	retirement.commits++
	return retirement.commits == 1 && retirement.aborts == 0
}

func (retirement *testRetirement) Abort() bool {
	retirement.mu.Lock()
	defer retirement.mu.Unlock()
	retirement.aborts++
	return retirement.aborts == 1 && retirement.commits == 0
}

type testRetirementBoundary struct {
	retirement Retirement
	err        error
}

func (boundary testRetirementBoundary) BeginUserRetirement(context.Context, int64) (Retirement, error) {
	if boundary.err != nil {
		return nil, boundary.err
	}
	return boundary.retirement, nil
}

type testRecoveryAdapter struct {
	name string
	run  func(context.Context, int64, int, time.Time) (WorkResult, error)
}

func (adapter testRecoveryAdapter) RecoverBeforeListener(ctx context.Context, now int64, limit int, deadline time.Time) (WorkResult, error) {
	if adapter.run != nil {
		return adapter.run(ctx, now, limit, deadline)
	}
	return WorkResult{}, nil
}

type testRetentionAdapter struct {
	name string
	run  func(context.Context, int64, int, time.Time) (WorkResult, error)
}

func (adapter testRetentionAdapter) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (WorkResult, error) {
	if adapter.run != nil {
		return adapter.run(ctx, now, limit, deadline)
	}
	return WorkResult{}, nil
}

type testHeldObjectAdapter struct {
	inspect func(context.Context, *sql.Tx, string, int64) (HeldObjectState, error)
	consume func(context.Context, *sql.Tx, string) error
	read    func(context.Context, *sql.Tx, string, int64) (bool, error)
}

func (adapter testHeldObjectAdapter) InspectForCreate(ctx context.Context, tx *sql.Tx, ref string, now int64) (HeldObjectState, error) {
	if adapter.inspect != nil {
		return adapter.inspect(ctx, tx, ref, now)
	}
	return HeldObjectState{Exists: true, OrdinaryDeadline: maximumUnixSecond}, nil
}

func (adapter testHeldObjectAdapter) ConsumeMarker(ctx context.Context, tx *sql.Tx, ref string) error {
	if adapter.consume != nil {
		return adapter.consume(ctx, tx, ref)
	}
	return nil
}

func (adapter testHeldObjectAdapter) ReadHeld(ctx context.Context, tx *sql.Tx, ref string, now int64) (bool, error) {
	if adapter.read != nil {
		return adapter.read(ctx, tx, ref, now)
	}
	return true, nil
}

type lifecycleTestFixture struct {
	store   *db.Store
	auth    *testFinalAuth
	exports *testExportAdapter
	config  Config
}

func newLifecycleTestFixture(t *testing.T, now int64) *lifecycleTestFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "lifecycle.sqlite")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	auth := &testFinalAuth{}
	exports := &testExportAdapter{}
	deleteCalls := []string{}
	noopDelete := func(name string) DeleteAdapter { return testDeleteAdapter{name: name, calls: &deleteCalls} }
	noopRecovery := func(name string) RecoveryAdapter { return testRecoveryAdapter{name: name} }
	noopRetention := func(name string) RetentionAdapter { return testRetentionAdapter{name: name} }
	held := testHeldObjectAdapter{}
	retirement := &testRetirement{}
	config := Config{
		Store: store, UserAuth: auth, AdminAuth: auth, CursorKeys: testCursorKeys{},
		Retirement: testRetirementBoundary{retirement: retirement}, Ledger: &testLedgerDelete{},
		Export: ExportAdapters{
			Identity: exports, Resources: exports, Issues: exports, Ledger: exports, Activities: exports,
			Donations: exports, Charity: exports, Fishing: exports, LinkLink: exports, RPS: exports,
		},
		Delete: DeleteAdapters{
			AuthSessionCallerKey: noopDelete("auth"), Resources: noopDelete("resources"), ClaimLog: noopDelete("claim_log"),
			IssuesAnnouncements: noopDelete("issues"), Donations: noopDelete("donations"), Activities: noopDelete("activities"),
			Reports: noopDelete("reports"), Fishing: noopDelete("fishing"), LinkLink: noopDelete("linklink"),
			RPS: noopDelete("rps"), DebugAccountStream: noopDelete("debug"),
		},
		Recovery: RecoveryAdapters{
			Idempotency: noopRecovery("idempotency"), Discovery: noopRecovery("discovery"), Claims: noopRecovery("claims"),
			Thursday: noopRecovery("thursday"), Reports: noopRecovery("reports"), Fishing: noopRecovery("fishing"),
			LinkLink: noopRecovery("linklink"), RPS: noopRecovery("rps"), Donations: noopRecovery("donations"), Secrets: noopRecovery("secrets"),
		},
		Retention: RetentionAdapters{
			Sessions: noopRetention("sessions"), RequestLogs: noopRetention("request_logs"), Audits: noopRetention("audits"),
			Issues: noopRetention("issues"), Fishing: noopRetention("fishing"), LinkLink: noopRetention("linklink"),
			RPS: noopRetention("rps"), Reports: noopRetention("reports"), Donations: noopRetention("donations"),
			Charity: noopRetention("charity"), Idempotency: noopRetention("idempotency"), Secrets: noopRetention("secrets"),
		},
		HeldObjects: HeldObjectAdapters{
			MaintenanceEvent: held, ReportCase: held, AnnouncementAudit: held, Donation: held, RequestLog: held,
		},
		Now: func() time.Time { return time.Unix(now, 0) }, NewID: db.GenerateOpaqueID,
	}
	return &lifecycleTestFixture{store: store, auth: auth, exports: exports, config: config}
}

func mustNewLifecycleCoordinator(t *testing.T, config Config) *Coordinator {
	t.Helper()
	coordinator, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return coordinator
}
