package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const maximumUnixSecond = int64(253402300799)

type OpaqueIDSource func(string) (string, error)

// ExportAdapters is the closed Generation 2 export registry. The coordinator
// calls every member in this order inside one transaction.
type ExportAdapters struct {
	Identity   IdentityExporter
	Resources  ResourceExporter
	Issues     IssueExporter
	Ledger     LedgerExporter
	Activities ActivityExporter
	Donations  DonationExporter
	Charity    CharityExporter
	Fishing    FishingExporter
	LinkLink   LinkLinkExporter
	RPS        RPSExporter
}

// DeleteAdapters is the closed account-deletion registry. Each adapter owns
// its domain SQL; the coordinator only fixes the cross-domain order.
type DeleteAdapters struct {
	AuthSessionCallerKey DeleteAdapter
	Resources            DeleteAdapter
	ClaimLog             DeleteAdapter
	IssuesAnnouncements  DeleteAdapter
	Donations            DeleteAdapter
	Activities           DeleteAdapter
	Reports              DeleteAdapter
	Fishing              DeleteAdapter
	LinkLink             DeleteAdapter
	RPS                  DeleteAdapter
	DebugAccountStream   DeleteAdapter
}

func (adapters DeleteAdapters) ordered() []DeleteAdapter {
	return []DeleteAdapter{
		adapters.AuthSessionCallerKey,
		adapters.Resources,
		adapters.ClaimLog,
		adapters.IssuesAnnouncements,
		adapters.Donations,
		adapters.Activities,
		adapters.Reports,
		adapters.Fishing,
		adapters.LinkLink,
		adapters.RPS,
		adapters.DebugAccountStream,
	}
}

// RecoveryAdapters is intentionally compile-time ordered rather than a
// runtime registry. Legal-hold expiry is owned by the coordinator and runs
// before this list.
type RecoveryAdapters struct {
	Idempotency RecoveryAdapter
	Discovery   RecoveryAdapter
	Claims      RecoveryAdapter
	Thursday    RecoveryAdapter
	Reports     RecoveryAdapter
	Fishing     RecoveryAdapter
	LinkLink    RecoveryAdapter
	RPS         RecoveryAdapter
	Donations   RecoveryAdapter
	Secrets     RecoveryAdapter
}

func (adapters RecoveryAdapters) ordered() []RecoveryAdapter {
	return []RecoveryAdapter{
		adapters.Idempotency,
		adapters.Discovery,
		adapters.Claims,
		adapters.Thursday,
		adapters.Reports,
		adapters.Fishing,
		adapters.LinkLink,
		adapters.RPS,
		adapters.Donations,
		adapters.Secrets,
	}
}

// RetentionAdapters fixes the six-hour cleanup order. Separate game fields
// keep each reducer and retention cursor under its domain owner.
type RetentionAdapters struct {
	Sessions    RetentionAdapter
	RequestLogs RetentionAdapter
	Audits      RetentionAdapter
	Issues      RetentionAdapter
	Fishing     RetentionAdapter
	LinkLink    RetentionAdapter
	RPS         RetentionAdapter
	Reports     RetentionAdapter
	Donations   RetentionAdapter
	Charity     RetentionAdapter
	Idempotency RetentionAdapter
	Secrets     RetentionAdapter
}

func (adapters RetentionAdapters) ordered() []RetentionAdapter {
	return []RetentionAdapter{
		adapters.Sessions,
		adapters.RequestLogs,
		adapters.Audits,
		adapters.Issues,
		adapters.Fishing,
		adapters.LinkLink,
		adapters.RPS,
		adapters.Reports,
		adapters.Donations,
		adapters.Charity,
		adapters.Idempotency,
		adapters.Secrets,
	}
}

type HeldObjectAdapters struct {
	MaintenanceEvent  HeldObjectAdapter
	ReportCase        HeldObjectAdapter
	AnnouncementAudit HeldObjectAdapter
	Donation          HeldObjectAdapter
	RequestLog        HeldObjectAdapter
}

func (adapters HeldObjectAdapters) forKind(kind HeldObjectKind) HeldObjectAdapter {
	switch kind {
	case HeldMaintenanceEvent:
		return adapters.MaintenanceEvent
	case HeldReportCase:
		return adapters.ReportCase
	case HeldAnnouncementAudit:
		return adapters.AnnouncementAudit
	case HeldDonation:
		return adapters.Donation
	case HeldRequestLog:
		return adapters.RequestLog
	default:
		return nil
	}
}

type Config struct {
	Store       *db.Store
	UserAuth    UserFinalAuthorizer
	AdminAuth   AdminFinalAuthorizer
	CursorKeys  CursorKeyDeriver
	Retirement  RetirementBoundary
	Ledger      LedgerDeleteAdapter
	Export      ExportAdapters
	Delete      DeleteAdapters
	Recovery    RecoveryAdapters
	Retention   RetentionAdapters
	HeldObjects HeldObjectAdapters
	Now         func() time.Time
	NewID       OpaqueIDSource
}

type Coordinator struct {
	database    *sql.DB
	userAuth    UserFinalAuthorizer
	adminAuth   AdminFinalAuthorizer
	retirement  RetirementBoundary
	ledger      LedgerDeleteAdapter
	export      ExportAdapters
	delete      DeleteAdapters
	recovery    RecoveryAdapters
	retention   RetentionAdapters
	heldObjects HeldObjectAdapters
	cursorKeys  CursorKeyDeriver
	now         func() time.Time
	newID       OpaqueIDSource
	closed      atomic.Bool

	runGate sync.Mutex
	runMu   sync.Mutex

	runGeneration           uint64
	lastRecoveryGeneration  uint64
	lastRecoveryResult      error
	lastRetentionGeneration uint64
	lastRetentionResult     error
	lastRunResult           error
}

func New(config Config) (*Coordinator, error) {
	if config.Store == nil || config.Store.DB() == nil || config.UserAuth == nil || config.AdminAuth == nil || config.CursorKeys == nil ||
		config.Retirement == nil || config.Ledger == nil || !completeExportAdapters(config.Export) ||
		!completeDeleteAdapters(config.Delete) || !completeRecoveryAdapters(config.Recovery) ||
		!completeRetentionAdapters(config.Retention) || !completeHeldObjectAdapters(config.HeldObjects) {
		return nil, ErrInvalid
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewID == nil {
		config.NewID = db.GenerateOpaqueID
	}
	return &Coordinator{
		database: config.Store.DB(), userAuth: config.UserAuth, adminAuth: config.AdminAuth,
		retirement: config.Retirement, ledger: config.Ledger, export: config.Export,
		delete: config.Delete, recovery: config.Recovery, retention: config.Retention,
		heldObjects: config.HeldObjects, cursorKeys: config.CursorKeys, now: config.Now, newID: config.NewID,
	}, nil
}

func completeExportAdapters(a ExportAdapters) bool {
	return a.Identity != nil && a.Resources != nil && a.Issues != nil && a.Ledger != nil &&
		a.Activities != nil && a.Donations != nil && a.Charity != nil && a.Fishing != nil &&
		a.LinkLink != nil && a.RPS != nil
}

func completeDeleteAdapters(a DeleteAdapters) bool {
	for _, adapter := range a.ordered() {
		if adapter == nil {
			return false
		}
	}
	return true
}

func completeRecoveryAdapters(a RecoveryAdapters) bool {
	for _, adapter := range a.ordered() {
		if adapter == nil {
			return false
		}
	}
	return true
}

func completeRetentionAdapters(a RetentionAdapters) bool {
	for _, adapter := range a.ordered() {
		if adapter == nil {
			return false
		}
	}
	return true
}

func completeHeldObjectAdapters(a HeldObjectAdapters) bool {
	return a.MaintenanceEvent != nil && a.ReportCase != nil && a.AnnouncementAudit != nil &&
		a.Donation != nil && a.RequestLog != nil
}

func validDecision(userID, decisionNow int64) bool {
	return userID > 0 && decisionNow >= 0 && decisionNow <= maximumUnixSecond
}

// Export builds and commits one authoritative schema-v4 snapshot. The encoded
// bytes are finalized before commit so an oversized document never commits a
// lazy-expiry write performed by a domain exporter.
func (coordinator *Coordinator) Export(ctx context.Context, userID, decisionNow int64) ([]byte, error) {
	if coordinator == nil || ctx == nil || !validDecision(userID, decisionNow) {
		return nil, ErrInvalid
	}
	if coordinator.closed.Load() {
		return nil, ErrClosed
	}
	tx, err := coordinator.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: begin export: %w", err)
	}
	defer tx.Rollback()
	var finalizers []ExportFinalizer
	finalizersFinished := false
	defer func() {
		if finalizersFinished {
			return
		}
		for index := len(finalizers) - 1; index >= 0; index-- {
			_ = finalizers[index].Abort()
		}
	}()
	if err := coordinator.userAuth.AuthorizeFreshUser(ctx, tx, userID); err != nil {
		return nil, err
	}
	request := ExportRequest{UserID: userID, DecisionNow: decisionNow, Limit: CollectionLimit}
	document := ExportDocument{SchemaVersion: SchemaVersion, GeneratedAt: decisionNow}
	if document.User, document.Usage, document.LogSummary, err = coordinator.export.Identity.ExportIdentity(ctx, tx, request); err != nil {
		return nil, err
	}
	if document.Endpoints, document.CatalogPairs, document.Models, document.CallerKey, err = coordinator.export.Resources.ExportResources(ctx, tx, request); err != nil {
		return nil, err
	}
	if document.Issues, err = coordinator.export.Issues.ExportIssues(ctx, tx, request); err != nil {
		return nil, err
	}
	if document.CreditLedger, err = coordinator.export.Ledger.ExportLedger(ctx, tx, request); err != nil {
		return nil, err
	}
	if document.WelfareClaims, document.Thursday, err = coordinator.export.Activities.ExportActivities(ctx, tx, request); err != nil {
		return nil, err
	}
	if document.Donations, err = coordinator.export.Donations.ExportDonations(ctx, tx, request); err != nil {
		return nil, err
	}
	if document.Charity, err = coordinator.export.Charity.ExportCharity(ctx, tx, request); err != nil {
		return nil, err
	}
	var finalizer ExportFinalizer
	if document.Fishing, finalizer, err = coordinator.export.Fishing.ExportFishing(ctx, tx, request); finalizer != nil {
		finalizers = append(finalizers, finalizer)
	}
	if err != nil {
		return nil, err
	}
	if document.LinkLink, finalizer, err = coordinator.export.LinkLink.ExportLinkLink(ctx, tx, request); finalizer != nil {
		finalizers = append(finalizers, finalizer)
	}
	if err != nil {
		return nil, err
	}
	if document.RPS, finalizer, err = coordinator.export.RPS.ExportRPS(ctx, tx, request); finalizer != nil {
		finalizers = append(finalizers, finalizer)
	}
	if err != nil {
		return nil, err
	}
	normalizeExportDocument(&document)
	if err := validateExportCollectionBounds(document); err != nil {
		return nil, err
	}
	body, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: encode export: %w", err)
	}
	if len(body) > MaxExportBytes {
		return nil, ErrTooLarge
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("lifecycle: commit export: %w", err)
	}
	finalizersFinished = true
	for _, prepared := range finalizers {
		_ = prepared.Commit()
	}
	return body, nil
}

func normalizeExportDocument(document *ExportDocument) {
	if document.Endpoints == nil {
		document.Endpoints = []EndpointExport{}
	}
	if document.CatalogPairs == nil {
		document.CatalogPairs = []CatalogPairExport{}
	}
	if document.Models == nil {
		document.Models = []ModelExport{}
	}
	if document.Issues == nil {
		document.Issues = []IssueExport{}
	}
	if document.CreditLedger == nil {
		document.CreditLedger = []LedgerEntryExport{}
	}
	if document.WelfareClaims == nil {
		document.WelfareClaims = []WelfareExport{}
	}
	if document.Thursday == nil {
		document.Thursday = []ThursdayExport{}
	}
	if document.Donations == nil {
		document.Donations = []DonationExport{}
	}
	if document.Fishing.Pending == nil {
		document.Fishing.Pending = []FishingPendingExport{}
	}
	if document.Fishing.Terminal == nil {
		document.Fishing.Terminal = []FishingBatchExport{}
	}
	if document.LinkLink.Summaries == nil {
		document.LinkLink.Summaries = []LinkLinkSummaryExport{}
	}
	if document.RPS.Summaries == nil {
		document.RPS.Summaries = []RPSSummaryExport{}
	}
	if document.RPS.Pending != nil && document.RPS.Pending.Seats == nil {
		document.RPS.Pending.Seats = []RPSPendingSeatExport{}
	}
	for index := range document.Endpoints {
		if document.Endpoints[index].Keys == nil {
			document.Endpoints[index].Keys = []EndpointKeyExport{}
		}
	}
	for index := range document.CatalogPairs {
		pair := &document.CatalogPairs[index]
		if pair.AutomaticEntries == nil {
			pair.AutomaticEntries = []CatalogEntryExport{}
		}
		if pair.ManualEntries == nil {
			pair.ManualEntries = []CatalogEntryExport{}
		}
	}
	for index := range document.Models {
		if document.Models[index].Bindings == nil {
			document.Models[index].Bindings = []BindingExport{}
		}
	}
	for index := range document.Donations {
		if document.Donations[index].Keys == nil {
			document.Donations[index].Keys = []DonationKeyExport{}
		}
	}
	for index := range document.Fishing.Terminal {
		if document.Fishing.Terminal[index].Outcomes == nil {
			document.Fishing.Terminal[index].Outcomes = []FishingOutcomeExport{}
		}
	}
}

func validateExportCollectionBounds(document ExportDocument) error {
	lengths := []int{
		len(document.Endpoints), len(document.CatalogPairs), len(document.Models), len(document.Issues),
		len(document.CreditLedger), len(document.WelfareClaims), len(document.Thursday), len(document.Donations),
		len(document.Fishing.Pending), len(document.Fishing.Terminal), len(document.LinkLink.Summaries), len(document.RPS.Summaries),
	}
	for _, endpoint := range document.Endpoints {
		lengths = append(lengths, len(endpoint.Keys))
	}
	for _, pair := range document.CatalogPairs {
		lengths = append(lengths, len(pair.AutomaticEntries), len(pair.ManualEntries))
	}
	for _, model := range document.Models {
		lengths = append(lengths, len(model.Bindings))
	}
	for _, donation := range document.Donations {
		lengths = append(lengths, len(donation.Keys))
	}
	for _, batch := range document.Fishing.Terminal {
		lengths = append(lengths, len(batch.Outcomes))
	}
	if document.RPS.Pending != nil {
		lengths = append(lengths, len(document.RPS.Pending.Seats))
	}
	for _, length := range lengths {
		if length > CollectionLimit {
			return ErrTooLarge
		}
	}
	return nil
}

// DeleteAccount executes every domain handoff and the final ledger/user delete
// in one transaction. Process-local retirement is committed only afterward.
func (coordinator *Coordinator) DeleteAccount(ctx context.Context, userID, decisionNow int64) error {
	if coordinator == nil || ctx == nil || !validDecision(userID, decisionNow) {
		return ErrInvalid
	}
	if coordinator.closed.Load() {
		return ErrClosed
	}
	retirement, err := coordinator.retirement.BeginUserRetirement(ctx, userID)
	if err != nil {
		return err
	}
	retirementFinished := false
	defer func() {
		if !retirementFinished {
			retirement.Abort()
		}
	}()

	tx, err := coordinator.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lifecycle: begin account deletion: %w", err)
	}
	defer tx.Rollback()
	if err := coordinator.userAuth.AuthorizeFreshUser(ctx, tx, userID); err != nil {
		return err
	}
	request := DeleteRequest{UserID: userID, DecisionNow: decisionNow}
	finalizers := make([]DeleteFinalizer, 0, len(coordinator.delete.ordered()))
	abortFinalizers := func() {
		for index := len(finalizers) - 1; index >= 0; index-- {
			if finalizers[index] != nil {
				finalizers[index].Abort()
			}
		}
	}
	for _, adapter := range coordinator.delete.ordered() {
		finalizer, prepareErr := adapter.PrepareDelete(ctx, tx, request)
		if prepareErr != nil {
			abortFinalizers()
			return prepareErr
		}
		finalizers = append(finalizers, finalizer)
	}
	operationID, err := coordinator.newID("op_")
	if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
		abortFinalizers()
		return ErrUnavailable
	}
	if err := coordinator.ledger.ZeroAndDeleteAccount(ctx, tx, request, operationID); err != nil {
		abortFinalizers()
		return err
	}
	if err := tx.Commit(); err != nil {
		abortFinalizers()
		return fmt.Errorf("lifecycle: commit account deletion: %w", err)
	}
	retirement.Commit()
	retirementFinished = true
	for _, finalizer := range finalizers {
		if finalizer != nil {
			finalizer.Commit()
		}
	}
	return nil
}

func (coordinator *Coordinator) Close() error {
	if coordinator == nil {
		return nil
	}
	coordinator.closed.Store(true)
	return nil
}

func joinErrors(items []error) error {
	filtered := items[:0]
	for _, err := range items {
		if err != nil {
			filtered = append(filtered, err)
		}
	}
	return errors.Join(filtered...)
}
