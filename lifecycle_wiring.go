package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/announcements"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charity"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	lifecycleadapters "github.com/waiting-here/NonbiriAPI/internal/lifecycle/adapters"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/reports"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

var (
	_ lifecycle.UserFinalAuthorizer  = (*roleFinalTxAuthorizer)(nil)
	_ lifecycle.AdminFinalAuthorizer = (*roleFinalTxAuthorizer)(nil)
)

func (authorizer *roleFinalTxAuthorizer) AuthorizeFreshUser(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorizeFresh(ctx, tx, userID, authz.ActorUserSession, authz.RoleUser)
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeFreshAdmin(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorizeFresh(ctx, tx, userID, authz.ActorAdminSession, authz.RoleAdministrator)
}

func (authorizer *roleFinalTxAuthorizer) authorizeFresh(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	kind authz.ActorKind,
	role authz.Role,
) error {
	if authorizer == nil || authorizer.authorizer == nil || ctx == nil || tx == nil {
		return authz.ErrUnauthorized
	}
	actor, ok := auth.ActorFromContext(ctx)
	if !ok || actor.Kind != kind || actor.UserID != userID {
		return authz.ErrUnauthorized
	}
	_, err := authorizer.authorizer.Authorize(ctx, tx, actor, authz.Requirement{
		Role: role, FreshElevation: true,
	})
	return err
}

type lifecycleRouteRegistrar struct{ runtime *auth.Runtime }

func (registrar lifecycleRouteRegistrar) RegisterUserRoute(
	method, pattern string,
	handler lifecycle.AuthorizedUserHandler,
) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterUserRoute(method, pattern,
		func(writer http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
			handler(writer, request, lifecycle.UserPrincipal{UserID: principal.UserID})
		})
}

type productionHeldReadAuthorizer struct{ coordinator *lifecycle.Coordinator }

func (authorizer productionHeldReadAuthorizer) AuthorizeHeldDonationRead(
	ctx context.Context,
	tx *sql.Tx,
	donationID int64,
	decisionNow int64,
) (bool, error) {
	adminID, err := heldReadAdminID(ctx)
	if err != nil {
		return false, donation.ErrUnauthorized
	}
	allowed, err := authorizer.coordinator.AuthorizeHeldObjectRead(
		ctx, tx, adminID, lifecycle.HeldDonation, strconv.FormatInt(donationID, 10), decisionNow,
	)
	if err != nil {
		return false, translateDonationHeldReadError(err)
	}
	return allowed, nil
}

func (authorizer productionHeldReadAuthorizer) AuthorizeHeldRequestLogRead(
	ctx context.Context,
	tx *sql.Tx,
	requestLogID int64,
	decisionNow int64,
) (bool, error) {
	adminID, err := heldReadAdminID(ctx)
	if err != nil {
		return false, logapi.ErrForbidden
	}
	allowed, err := authorizer.coordinator.AuthorizeHeldObjectRead(
		ctx, tx, adminID, lifecycle.HeldRequestLog, strconv.FormatInt(requestLogID, 10), decisionNow,
	)
	if err != nil {
		return false, translateLogHeldReadError(err)
	}
	return allowed, nil
}

func heldReadAdminID(ctx context.Context) (int64, error) {
	actor, ok := auth.ActorFromContext(ctx)
	if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
		return 0, authz.ErrUnauthorized
	}
	return actor.UserID, nil
}

func translateDonationHeldReadError(err error) error {
	var target error
	switch {
	case errors.Is(err, lifecycle.ErrInvalid):
		target = donation.ErrInvalidRequest
	case errors.Is(err, lifecycle.ErrUnauthorized), errors.Is(err, authz.ErrUnauthorized):
		target = donation.ErrUnauthorized
	case errors.Is(err, lifecycle.ErrForbidden), errors.Is(err, authz.ErrForbidden):
		target = donation.ErrForbidden
	case errors.Is(err, lifecycle.ErrNotFound):
		target = donation.ErrNotFound
	case errors.Is(err, lifecycle.ErrConflict):
		target = donation.ErrConflict
	case errors.Is(err, lifecycle.ErrInvariant):
		target = donation.ErrInvariant
	default:
		target = donation.ErrUnavailable
	}
	return fmt.Errorf("%w: %v", target, err)
}

func translateLogHeldReadError(err error) error {
	var target error
	switch {
	case errors.Is(err, lifecycle.ErrInvalid):
		target = logapi.ErrInvalid
	case errors.Is(err, lifecycle.ErrUnauthorized), errors.Is(err, lifecycle.ErrForbidden),
		errors.Is(err, authz.ErrUnauthorized), errors.Is(err, authz.ErrForbidden):
		target = logapi.ErrForbidden
	case errors.Is(err, lifecycle.ErrNotFound):
		target = logapi.ErrNotFound
	case errors.Is(err, lifecycle.ErrConflict):
		target = logapi.ErrConflict
	case errors.Is(err, lifecycle.ErrInvariant):
		target = logapi.ErrInvariant
	default:
		target = logapi.ErrUnavailable
	}
	return fmt.Errorf("%w: %v", target, err)
}

func (registrar lifecycleRouteRegistrar) RegisterAdminRoute(
	method, pattern string,
	handler lifecycle.AuthorizedAdminHandler,
) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterAdminRoute(method, pattern, http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			actor, ok := auth.ActorFromContext(request.Context())
			if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
				httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
				return
			}
			handler(writer, request, lifecycle.AdminPrincipal{UserID: actor.UserID})
		}))
}

type productionRetirementBoundary struct {
	gate  *lifecyclegate.Gate
	flow  *flowcontrol.Controller
	games *game.StartLimiter
}

func (boundary *productionRetirementBoundary) BeginUserRetirement(
	ctx context.Context,
	userID int64,
) (lifecycle.Retirement, error) {
	if boundary == nil || boundary.gate == nil || boundary.flow == nil || boundary.games == nil ||
		ctx == nil || userID <= 0 {
		return nil, lifecycle.ErrInvalid
	}
	gateRetirement, err := boundary.gate.BeginUserRetirementExcludingContext(ctx, userID)
	if err != nil {
		return nil, translateRetirementError(err)
	}
	flowRetirement, err := boundary.flow.BeginUserRetirement(userID)
	if err != nil {
		gateRetirement.Abort()
		return nil, translateRetirementError(err)
	}
	gameCommit, gameAbort, err := boundary.games.BeginUserDeletion(userID)
	if err != nil {
		flowRetirement.Abort()
		gateRetirement.Abort()
		return nil, translateRetirementError(err)
	}
	return &productionRetirement{
		gate: gateRetirement, flow: flowRetirement,
		gameCommit: gameCommit, gameAbort: gameAbort,
	}, nil
}

func translateRetirementError(err error) error {
	switch {
	case errors.Is(err, lifecyclegate.ErrInvalid), errors.Is(err, flowcontrol.ErrInvalidUser):
		return fmt.Errorf("%w: %v", lifecycle.ErrInvalid, err)
	case errors.Is(err, lifecyclegate.ErrRetiring), errors.Is(err, game.ErrUserDeleting):
		return fmt.Errorf("%w: %v", lifecycle.ErrConflict, err)
	default:
		return fmt.Errorf("%w: %v", lifecycle.ErrUnavailable, err)
	}
}

type productionRetirement struct {
	gate       *lifecyclegate.UserRetirement
	flow       *flowcontrol.UserRetirement
	gameCommit func() bool
	gameAbort  func() bool
	done       atomic.Bool
}

func (retirement *productionRetirement) Commit() bool {
	if retirement == nil || !retirement.done.CompareAndSwap(false, true) {
		return false
	}
	retirement.gameCommit()
	retirement.flow.Commit()
	retirement.gate.Commit()
	return true
}

func (retirement *productionRetirement) Abort() bool {
	if retirement == nil || !retirement.done.CompareAndSwap(false, true) {
		return false
	}
	retirement.gameAbort()
	retirement.flow.Abort()
	retirement.gate.Abort()
	return true
}

func newLifecycleCoordinator(
	store *db.Store,
	vault *secret.Vault,
	authRuntime *auth.Runtime,
	roleAuthorizer *roleFinalTxAuthorizer,
	forwardRuntime *publicForwardRuntime,
	gameRuntimes *gameRuntimeBundle,
	claimService *claim.Service,
	resourceRepository *resources.Repository,
	issueService *issues.Service,
	logRepository *logapi.Repository,
	activityService *activities.Service,
	activityRepository *activities.Repository,
	donationService *donation.Service,
	charityService *charity.Service,
	reportRepository *reports.Repository,
	announcementRepository *announcements.Repository,
	maintenanceService *maintenance.Service,
	activityEvents *accountstream.Hub,
	debugHub *debug.Hub,
) (*lifecycle.Coordinator, error) {
	if store == nil || vault == nil || authRuntime == nil || roleAuthorizer == nil ||
		forwardRuntime == nil || forwardRuntime.lifecycle == nil || forwardRuntime.flow == nil ||
		gameRuntimes == nil || gameRuntimes.limiter == nil || gameRuntimes.fishing == nil ||
		gameRuntimes.linklink == nil || gameRuntimes.rps == nil || claimService == nil ||
		resourceRepository == nil || issueService == nil || logRepository == nil ||
		activityService == nil || activityRepository == nil || donationService == nil || charityService == nil ||
		reportRepository == nil || announcementRepository == nil || maintenanceService == nil ||
		activityEvents == nil || debugHub == nil {
		return nil, lifecycle.ErrInvalid
	}

	accountResources, err := lifecycleadapters.NewAccountResources(
		authRuntime, resourceRepository, issueService.Sources(), logRepository,
	)
	if err != nil {
		return nil, err
	}
	authDelete, err := lifecycleadapters.NewAuthDeleteAdapter(authRuntime)
	if err != nil {
		return nil, err
	}
	resourceDelete, err := lifecycleadapters.NewResourceDeleteAdapter(resourceRepository)
	if err != nil {
		return nil, err
	}
	claimLogDelete, err := lifecycleadapters.NewClaimLogDeleteAdapter(claimService, logRepository)
	if err != nil {
		return nil, err
	}
	runtimeMemory, err := lifecycleadapters.NewRuntimeMemoryDeleteAdapter(activityEvents, debugHub)
	if err != nil {
		return nil, err
	}
	maintenanceRetention, err := maintenance.NewLifecycleRetention(store.DB())
	if err != nil {
		return nil, err
	}

	ledgerAdapter := lifecycleadapters.NewLedgerAdapter()
	activityAdapter := lifecycleadapters.NewActivity(activityRepository)
	donationAdapter := lifecycleadapters.NewDonation(donationService)
	charityAdapter := lifecycleadapters.NewCharity(charityService)
	fishingAdapter := lifecycleadapters.NewFishing(gameRuntimes.fishing.Lifecycle())
	linkLinkAdapter := lifecycleadapters.NewLinkLink(gameRuntimes.linklink.Lifecycle())
	rpsAdapter := lifecycleadapters.NewRPS(gameRuntimes.rps.Lifecycle())
	reportAdapter := lifecycleadapters.NewReportLifecycle(reportRepository)
	announcementAdapter := lifecycleadapters.NewAnnouncementAuditLifecycle(announcementRepository)
	secretAdapter := lifecycleadapters.NewOrphanSecretRecovery(claimService)
	idempotencyAdapter := lifecycleadapters.NewIdempotencyMaintenance(
		idempotency.NewMaintenance(store.DB()),
	)

	coordinator, err := lifecycle.New(lifecycle.Config{
		Store: store, UserAuth: roleAuthorizer, AdminAuth: roleAuthorizer, CursorKeys: vault,
		Retirement: &productionRetirementBoundary{
			gate: forwardRuntime.lifecycle, flow: forwardRuntime.flow, games: gameRuntimes.limiter,
		},
		Ledger: ledgerAdapter,
		Export: lifecycle.ExportAdapters{
			Identity: accountResources, Resources: accountResources, Issues: accountResources,
			Ledger: ledgerAdapter, Activities: activityAdapter, Donations: donationAdapter,
			Charity: charityAdapter, Fishing: fishingAdapter, LinkLink: linkLinkAdapter, RPS: rpsAdapter,
		},
		Delete: lifecycle.DeleteAdapters{
			AuthSessionCallerKey: authDelete, Resources: resourceDelete, ClaimLog: claimLogDelete,
			IssuesAnnouncements: lifecycleadapters.NewIssueAnnouncementDelete(issueService.Sources()),
			Donations:           donationAdapter, Activities: activityAdapter, Reports: reportAdapter,
			Fishing: fishingAdapter, LinkLink: linkLinkAdapter, RPS: rpsAdapter,
			DebugAccountStream: runtimeMemory,
		},
		Recovery: lifecycle.RecoveryAdapters{
			Idempotency: idempotencyAdapter,
			Discovery:   lifecycleadapters.NewDiscoveryRecovery(resourceRepository),
			Claims:      lifecycleadapters.NewClaimRecovery(claimService),
			Thursday:    lifecycleadapters.NewThursdayRecovery(activityService),
			Reports:     reportAdapter,
			Fishing:     lifecycleadapters.NewFishingRecovery(gameRuntimes.fishing),
			LinkLink:    lifecycleadapters.NewLinkLinkRecovery(gameRuntimes.linklink),
			RPS:         lifecycleadapters.NewRPSRecovery(gameRuntimes.rps),
			Donations:   lifecycleadapters.NewDonationRecovery(donationService),
			Secrets:     secretAdapter,
		},
		Retention: lifecycle.RetentionAdapters{
			Sessions:    lifecycleadapters.NewAuthSessionRetention(authRuntime),
			RequestLogs: lifecycleadapters.NewRequestLogRetention(logRepository),
			Audits:      lifecycleadapters.NewAuditRetention(maintenanceRetention, announcementRepository),
			Issues:      lifecycleadapters.NewIssueRetention(issueService),
			Fishing:     fishingAdapter, LinkLink: linkLinkAdapter, RPS: rpsAdapter,
			Reports: reportAdapter, Donations: donationAdapter, Charity: charityAdapter,
			Idempotency: idempotencyAdapter, Secrets: secretAdapter,
		},
		HeldObjects: lifecycle.HeldObjectAdapters{
			MaintenanceEvent: lifecycleadapters.NewMaintenanceHeldObject(maintenanceService),
			ReportCase:       reportAdapter, AnnouncementAudit: announcementAdapter,
			Donation:   lifecycleadapters.NewDonationHeldObject(donationService),
			RequestLog: lifecycleadapters.NewRequestLogHeldObject(logRepository),
		},
	})
	if err != nil {
		return nil, err
	}
	heldRead := productionHeldReadAuthorizer{coordinator: coordinator}
	if err := donationService.AttachAdminHeldReadAuthorizer(heldRead); err != nil {
		_ = coordinator.Close()
		return nil, err
	}
	if err := logRepository.AttachAdminHeldReadAuthorizer(heldRead); err != nil {
		_ = coordinator.Close()
		return nil, err
	}
	return coordinator, nil
}

func startLifecycleWorker(parent context.Context, coordinator *lifecycle.Coordinator) (context.CancelFunc, <-chan struct{}, error) {
	if parent == nil || coordinator == nil {
		return nil, nil, lifecycle.ErrInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(lifecycle.WorkerSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := coordinator.RunDue(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, lifecycle.ErrClosed) {
					slog.Error("account lifecycle maintenance pass failed", "err", err)
				}
			}
		}
	}()
	return cancel, done, nil
}
