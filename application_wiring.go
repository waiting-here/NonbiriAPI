package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/adminusers"
	"github.com/waiting-here/NonbiriAPI/internal/announcements"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/charity"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/checkin"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/creditapi"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	gamehost "github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/reports"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/resourcebridge"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/stewardautomation"
	"github.com/waiting-here/NonbiriAPI/internal/timeapi"
)

// emptyResourceValidationAuthority is the production authority when no
// explicit endpoint validator feed is configured. Discovery and routing
// remain live authorities; this closed provider contributes no synthetic
// credential/configuration evidence.
type emptyResourceValidationAuthority struct{}

var _ issues.ResourceValidationAuthority = emptyResourceValidationAuthority{}

func (emptyResourceValidationAuthority) Current(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	kind issues.ResourceKind,
	resourceID int64,
	root issues.RootCause,
) (issues.ResourceValidationState, error) {
	if ctx == nil || tx == nil || userID <= 0 || resourceID <= 0 || kind == "" || root == "" {
		return issues.ResourceValidationState{}, errors.New("resource validation authority received an invalid target")
	}
	return issues.ResourceValidationState{ObservedAt: 0}, nil
}

func (emptyResourceValidationAuthority) Scan(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	cursor string,
	limit int,
) (issues.ResourceValidationBatch, error) {
	if ctx == nil || tx == nil || userID <= 0 || cursor != "" || limit < 1 || limit > 100 {
		return issues.ResourceValidationBatch{}, errors.New("resource validation authority received an invalid scan")
	}
	return issues.ResourceValidationBatch{Items: []issues.ResourceValidationTarget{}, Done: true}, nil
}

type activityMutationGate struct{}

var _ activities.UserMutationGate = activityMutationGate{}

func (activityMutationGate) AuthorizeUserActivity(ctx context.Context, tx *sql.Tx, userID int64) error {
	if ctx == nil || tx == nil || userID <= 0 {
		return activities.ErrUnauthorized
	}
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM maintenance_state WHERE id=1`).Scan(&enabled); err != nil {
		return fmt.Errorf("read maintenance state: %w", err)
	}
	switch enabled {
	case 0:
		return nil
	case 1:
		return activities.ErrMaintenance
	default:
		return activities.ErrInvariant
	}
}

type activityPublishReporter struct{}

func (activityPublishReporter) ReportActivitiesPublishError(err error) {
	if err != nil {
		slog.Error("activity account event publication failed", "err", err)
	}
}

func buildApplication(ctx context.Context, cfg *config.Config, store *db.Store, vault *secret.Vault) (*application, error) {
	return buildApplicationWithRuntimeOptions(ctx, cfg, store, vault, applicationRuntimeOptions{})
}

func buildApplicationWithGameClock(ctx context.Context, cfg *config.Config, store *db.Store, vault *secret.Vault, gameNow func() time.Time) (*application, error) {
	return buildApplicationWithRuntimeOptions(ctx, cfg, store, vault, applicationRuntimeOptions{GameNow: gameNow})
}

// Runtime options are supplied only by the process composition root. Request
// data and persisted configuration cannot replace the outbound policy.
type applicationRuntimeOptions struct {
	Egress      *egress.Stack
	GameNow     func() time.Time
	ActivityNow func() time.Time
}

func buildApplicationWithRuntimeOptions(startupContext context.Context, cfg *config.Config, store *db.Store, vault *secret.Vault, options applicationRuntimeOptions) (built *application, result error) {
	if startupContext == nil || cfg == nil || store == nil || vault == nil {
		return nil, errors.New("application dependencies are required")
	}
	if _, bounded := startupContext.Deadline(); !bounded {
		var cancel context.CancelFunc
		startupContext, cancel = context.WithTimeout(startupContext, db.DefaultStartupTimeout)
		defer cancel()
	}
	if err := startupContext.Err(); err != nil {
		return nil, err
	}
	db.RecordStartupStage(startupContext, db.StageDomainRecovery)
	workerContext, workerCancel := context.WithCancel(context.Background())
	defer func() {
		if built == nil {
			workerCancel()
		}
	}()
	gameNow := options.GameNow
	if gameNow == nil {
		gameNow = time.Now
	}

	elevationManager, err := elevation.NewManager()
	if err != nil {
		return nil, fmt.Errorf("create elevation manager: %w", err)
	}
	authorizer := authz.New(authz.Options{Elevation: elevationManager})
	gate := maintenance.NewGate()
	registry := maintenance.NewRegistry()
	var activityEngines *activityRuntime
	maintenanceService, err := maintenance.NewService(maintenance.ServiceOptions{
		Authorizer: authorizer,
		Gate:       gate,
		Registry:   registry,
		PrepareEnableTx: func(ctx context.Context, tx *sql.Tx, at int64) (func(bool), error) {
			return activityEngines.PrepareMaintenanceTx(ctx, tx, at)
		},
	})
	if err != nil {
		_ = elevationManager.Close()
		return nil, fmt.Errorf("create maintenance service: %w", err)
	}

	outbound := options.Egress
	if outbound == nil {
		outbound, err = egress.NewStack(egress.StackOptions{})
		if err != nil {
			_ = elevationManager.Close()
			return nil, fmt.Errorf("create egress stack: %w", err)
		}
	}
	var bridgeRuntime *resourcebridge.Runtime
	var discoveryWorker *resources.DiscoveryWorkerPool
	var resourceRepository *resources.Repository
	var reportRepository *reports.Repository
	var authRuntime *auth.Runtime
	var activityEvents *accountstream.Hub
	var lifecycleCoordinator *lifecycle.Coordinator
	var debugHub *debug.Hub
	var accountConnections *accountEventConnections
	var forwardRuntime *publicForwardRuntime
	var gameRuntimes *gameRuntimeBundle
	var audits *auditRuntime
	defer func() {
		if built != nil {
			return
		}
		partial := &application{
			activityRuntime: activityEngines, accountEvents: accountConnections,
			debug: debugHub, reports: reportRepository, lifecycle: lifecycleCoordinator,
			games: gameRuntimes, forward: forwardRuntime, audits: audits,
			activityEvents: activityEvents, authRuntime: authRuntime, elevation: elevationManager,
			discoveryWorker: discoveryWorker, bridge: bridgeRuntime, egress: outbound,
			workerCancel: workerCancel,
		}
		if err := partial.CloseContext(startupContext); err != nil {
			var pending *applicationCleanupError
			if errors.As(err, &pending) {
				pending.primary = result
				result = pending
			} else {
				result = db.WithStartupCleanupError(result, err)
			}
		}
	}()

	if err := outbound.AddSelfOrigins(startupContext, cfg); err != nil {
		return nil, fmt.Errorf("register egress self origins: %w", err)
	}
	rpmLimits, concurrencyLimits, err := loadRuntimeLimits(startupContext, store)
	if err != nil {
		return nil, fmt.Errorf("load runtime limits: %w", err)
	}
	if err := outbound.SetConcurrencyLimits(concurrencyLimits); err != nil {
		return nil, fmt.Errorf("apply egress concurrency limits: %w", err)
	}
	localBackend, err := backend.NewLocal(outbound)
	if err != nil {
		return nil, fmt.Errorf("create local backend: %w", err)
	}
	discordProvider, err := auth.NewHTTPDiscordProvider(auth.HTTPDiscordProviderConfig{
		ClientID:     cfg.DiscordClientID,
		ClientSecret: cfg.DiscordClientSecret,
		Scopes:       cfg.DiscordOAuthScopes,
	})
	if err != nil {
		return nil, fmt.Errorf("create Discord authentication provider: %w", err)
	}
	authRuntime, err = auth.NewRuntime(auth.RuntimeConfig{
		Store:                store,
		Provider:             discordProvider,
		DiscordClientID:      cfg.DiscordClientID,
		UserSiteBaseURL:      cfg.SiteBaseURL,
		AdminUsername:        cfg.AdminUsername,
		AdminPassword:        cfg.AdminPassword,
		CredentialKeyDeriver: vault,
		Authorizer:           authorizer,
		Maintenance:          gate,
		Elevation:            elevationManager,
	})
	if err != nil {
		return nil, fmt.Errorf("create authentication runtime: %w", err)
	}
	for cursor := int64(0); ; {
		next, count, err := authRuntime.IdentityContinuity().BackfillBatch(startupContext, cursor, 1000)
		if err != nil {
			return nil, fmt.Errorf("bind account continuity: %w", err)
		}
		if count < 1000 {
			break
		}
		cursor = next
	}
	roleAuthorizer := &roleFinalTxAuthorizer{authorizer: authorizer}
	audits, err = newAuditRuntime(startupContext, store, vault, roleAuthorizer)
	if err != nil {
		return nil, fmt.Errorf("create audit runtime: %w", err)
	}
	adminConfigRepository, err := adminapi.NewSiteConfigRepository(adminapi.SiteConfigRepositoryOptions{
		Store: store, FinalAuthorizer: roleAuthorizer,
		Committed: audits.configurationChanged,
	})
	if err != nil {
		return nil, fmt.Errorf("create administrator site configuration repository: %w", err)
	}
	adminConfigRuntime, err := adminapi.NewSiteConfigRuntime(adminConfigRepository)
	if err != nil {
		return nil, fmt.Errorf("create administrator site configuration runtime: %w", err)
	}
	adminAlertRepository, err := adminalerts.NewRepository(adminalerts.Config{
		Store: store, CursorKeys: vault, FinalAuth: roleAuthorizer,
	})
	if err != nil {
		return nil, fmt.Errorf("create administrator alert repository: %w", err)
	}
	donationService, err := donation.New(donation.Config{
		Store:      store,
		OwnerAuth:  authRuntime,
		RoleAuth:   roleAuthorizer,
		CursorKeys: vault,
	})
	if err != nil {
		return nil, fmt.Errorf("create donation service: %w", err)
	}
	charityService, err := charity.New(charity.Config{
		Store:       store,
		KeyDeletion: donationService,
	})
	if err != nil {
		return nil, fmt.Errorf("create charity service: %w", err)
	}
	claimService, err := claim.New(claim.Dependencies{
		Observations: audits.observations,
		DB:           store.DB(),
		Secrets:      vault,
		Accounting:   claim.NewLedgerAccounting(),
		Charity:      charityService,
		Acceptance:   maintenanceService,
	})
	if err != nil {
		return nil, fmt.Errorf("create claim service: %w", err)
	}
	usageContext, cancelUsageInitialization := context.WithTimeout(startupContext, 30*time.Second)
	err = claimService.InitializeUsageTotals(usageContext)
	cancelUsageInitialization()
	if err != nil {
		return nil, fmt.Errorf("initialize request usage totals: %w", err)
	}
	bridgeRuntime, err = resourcebridge.New(resourcebridge.Config{
		ErrorScope: audits.observations.DiscoveryScope,
		Store:      store,
		Vault:      vault,
		Claims:     claimService,
		Backend:    localBackend,
	})
	if err != nil {
		return nil, fmt.Errorf("create resource bridge: %w", err)
	}
	discoveryWorker, err = resources.NewDiscoveryWorkerPool(
		discoveryWorkerMaxConcurrent,
		discoveryWorkerMaxAdmitted,
		discoveryWorkerTimeout,
	)
	if err != nil {
		return nil, fmt.Errorf("create discovery worker: %w", err)
	}
	connectorRegistry := connector.NewDefaultRegistry()
	announcementRepository, err := announcements.NewRepository(announcements.Config{
		Store: store, CursorKeys: vault, FinalAuth: roleAuthorizer,
	})
	if err != nil {
		return nil, fmt.Errorf("create announcement repository: %w", err)
	}
	announcementService, err := announcements.NewService(announcementRepository)
	if err != nil {
		return nil, fmt.Errorf("create announcement service: %w", err)
	}
	issueRepository, err := issues.NewRepository(issues.Config{
		Store: store, CursorKeys: vault, ResourceValidation: emptyResourceValidationAuthority{},
	})
	if err != nil {
		return nil, fmt.Errorf("create issue repository: %w", err)
	}
	issueService, err := issues.NewService(issueRepository)
	if err != nil {
		return nil, fmt.Errorf("create issue service: %w", err)
	}
	reportRepository, err = reports.New(reports.Config{
		Store: store, Connectors: connectorRegistry, BaseURLs: outbound,
		KeyDeriver: vault, Authorizer: authorizer, IssueProjection: issueService.Sources(),
		DeleteKey: func(ctx context.Context, tx *sql.Tx, ownerUserID, endpointKeyID, decisionNow int64) error {
			if resourceRepository == nil {
				return errors.New("resource deletion capability unavailable")
			}
			return resourceRepository.DeleteEndpointKeyForReport(ctx, tx, ownerUserID, endpointKeyID, decisionNow)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create report repository: %w", err)
	}
	adaptations, err := requestadaptation.New(requestadaptation.Config{DB: store.DB(), Codec: vault, KeyDeriver: vault})
	if err != nil {
		return nil, fmt.Errorf("create request adaptation store: %w", err)
	}
	resourceRepository, err = resources.New(resources.Config{
		Adaptations:      adaptations,
		Store:            store,
		Connectors:       connectorRegistry,
		BaseURLs:         outbound,
		Secrets:          bridgeRuntime,
		KeyDeletion:      charityService,
		KeyCreation:      reportRepository,
		Projection:       issueService.Sources(),
		DiscoveryRail:    bridgeRuntime,
		DiscoveryWorker:  discoveryWorker,
		ManagedDiscovery: donationService,
		CursorKeys:       vault,
		FinalAuth:        authRuntime,
		AdminFinalAuth:   roleAuthorizer,
	})
	if err != nil {
		return nil, fmt.Errorf("create resource repository: %w", err)
	}
	charityRoutingService, err := charityrouting.New(charityrouting.Config{
		Adaptations:   adaptations,
		Store:         store,
		RoleAuth:      roleAuthorizer,
		DonationState: donationService,
		CursorKeys:    vault,
	})
	if err != nil {
		return nil, fmt.Errorf("create charity routing service: %w", err)
	}
	activityRepository, err := activities.NewRepository(activities.RepositoryConfig{
		Store:          store,
		UserFinalAuth:  activityUserAuthorizer{runtime: authRuntime},
		AdminFinalAuth: roleAuthorizer,
		UserGate:       activityMutationGate{},
		CursorKeys:     vault,
	})
	if err != nil {
		return nil, fmt.Errorf("create activities repository: %w", err)
	}
	accountSources, err := newAccountEventSources(activityRepository)
	if err != nil {
		return nil, fmt.Errorf("create account event sources: %w", err)
	}
	activityEvents, err = accountstream.New(accountSources, accountSources)
	if err != nil {
		return nil, fmt.Errorf("create account event hub: %w", err)
	}
	accountConnections = newAccountEventConnections()
	activityPublisher, err := activities.NewAccountstreamPublisher(activityRepository, activityEvents)
	if err != nil {
		return nil, fmt.Errorf("create activities publisher: %w", err)
	}
	activityService, err := activities.NewService(activities.ServiceConfig{
		Repository: activityRepository,
		Publisher:  activityPublisher,
		Reporter:   activityPublishReporter{},
	})
	if err != nil {
		return nil, fmt.Errorf("create activities service: %w", err)
	}
	if err := recoverAnnouncementsBeforeListener(startupContext, announcementService); err != nil {
		return nil, err
	}
	if err := recoverIssuesBeforeListener(startupContext, issueService); err != nil {
		return nil, err
	}
	debugHub, err = debug.NewHub(debugIdentityAuthority{runtime: authRuntime})
	if err != nil {
		return nil, fmt.Errorf("create Debug hub: %w", err)
	}
	debugMutations, err := debug.NewMutationRepository(store.DB())
	if err != nil {
		return nil, fmt.Errorf("create Debug mutation repository: %w", err)
	}
	logRepository, err := logapi.NewRepository(store.DB(), vault)
	if err != nil {
		return nil, fmt.Errorf("create log repository: %w", err)
	}
	gameRuntimes, err = newGameRuntimeBundle(
		store, vault, authRuntime, roleAuthorizer, maintenanceService, activityRepository,
		activityPublisher, activityEvents, accountSources,
		gameNow,
	)
	if err != nil {
		return nil, err
	}
	checkinService, err := checkin.NewService(checkin.ServiceConfig{
		Store: store, FinalAuth: authRuntime, Maintenance: maintenanceService,
	})
	if err != nil {
		return nil, fmt.Errorf("create check-in service: %w", err)
	}
	homeGameService := gameRuntimes.Service
	rankingService, err := ranking.New(store.DB(), authRuntime, gameNow)
	if err != nil {
		return nil, err
	}
	if err := ranking.RegisterRoutes(authRuntime, rankingService); err != nil {
		return nil, err
	}
	if err := gameRuntimes.RegisterRoutes(gamehost.Registrars{
		User: authRuntime, Admin: authRuntime, Continuation: authRuntime, Maintenance: registry,
	}); err != nil {
		return nil, fmt.Errorf("register game routes: %w", err)
	}
	userInvalidations := &userSessionInvalidationFanout{
		debug: debugHub, connections: accountConnections,
	}
	if err := authRuntime.AttachUserSessionInvalidationObserver(userInvalidations); err != nil {
		return nil, fmt.Errorf("attach user-session invalidation observer: %w", err)
	}
	activityEngines, err = newActivityRuntime(store, vault, authRuntime, roleAuthorizer,
		maintenanceService, outbound, audits, userInvalidations, gameRuntimes.CancelUserDuelsTx, options.ActivityNow)
	if err != nil {
		return nil, fmt.Errorf("create limited activity runtimes: %w", err)
	}
	adminUserService, err := adminusers.NewService(adminusers.ServiceConfig{
		Database: store.DB(), CursorKeys: vault, FinalAuth: roleAuthorizer, Invalidator: userInvalidations,
		CancelUserDuelsTx: activityEngines.CancelUserTx,
	})
	if err != nil {
		return nil, fmt.Errorf("create administrator user service: %w", err)
	}
	forwardRuntime, err = newPublicForwardRuntime(
		store, vault, adaptations, authRuntime.IdentityContinuity(), claimService, charityService, charityRoutingService, resourceRepository,
		connectorRegistry, localBackend, debugHub, gate, rpmLimits, activityEngines.CancelUserTx, audits, userInvalidations.InvalidateUserAuthority,
	)
	if err != nil {
		return nil, err
	}
	if err := authRuntime.AttachUserLifecycleGate(forwardRuntime.lifecycle); err != nil {
		return nil, fmt.Errorf("attach shared user lifecycle gate: %w", err)
	}
	if err := adminUserService.AttachIdentityMutationBarrier(func(ctx context.Context, discordID string) (func(), error) {
		key, err := authRuntime.IdentityContinuity().KeyForDiscord(discordID)
		if err != nil {
			return nil, err
		}
		change, err := forwardRuntime.lifecycle.BeginIdentityChange(ctx, [32]byte(key))
		if errors.Is(err, lifecyclegate.ErrRetiring) {
			return nil, adminusers.ErrConflict
		}
		if err != nil {
			return nil, err
		}
		return func() { change.Abort() }, nil
	}); err != nil {
		return nil, fmt.Errorf("attach account management identity barrier: %w", err)
	}
	audits.flow = forwardRuntime.flow
	userInvalidations.limitsChanged = forwardRuntime.flow.NotifyUserLimitsChanged
	if err := audits.attachAccess(resourceRepository); err != nil {
		return nil, fmt.Errorf("attach auxiliary access observations: %w", err)
	}
	lifecycleCoordinator, err = newLifecycleCoordinator(
		store, vault, authRuntime, roleAuthorizer, forwardRuntime, gameRuntimes,
		claimService, resourceRepository, issueService, logRepository,
		activityService, activityRepository, donationService, charityService,
		reportRepository, announcementRepository, maintenanceService,
		activityEvents, debugHub, gameNow, audits, activityEngines,
	)
	if err != nil {
		return nil, fmt.Errorf("create account lifecycle coordinator: %w", err)
	}
	if err := adminapi.RegisterSiteConfigRoutes(siteConfigRouteRegistrar{runtime: authRuntime}, adminConfigRuntime); err != nil {
		return nil, fmt.Errorf("register administrator site configuration routes: %w", err)
	}
	if err := adminusers.RegisterRoutes(adminUserRouteRegistrar{runtime: authRuntime}, adminUserService); err != nil {
		return nil, fmt.Errorf("register administrator user routes: %w", err)
	}
	if err := adminusers.RegisterStewardRoutes(adminUserRouteRegistrar{runtime: authRuntime}, adminUserService); err != nil {
		return nil, fmt.Errorf("register steward user routes: %w", err)
	}
	if err := adminalerts.RegisterRoutes(adminAlertRouteRegistrar{runtime: authRuntime}, adminAlertRepository); err != nil {
		return nil, fmt.Errorf("register administrator alert routes: %w", err)
	}
	if err := resources.RegisterRoutes(authRuntime, resourceRepository); err != nil {
		return nil, fmt.Errorf("register resource routes: %w", err)
	}
	if err := resources.RegisterAdminRoutes(resourceAdminRouteRegistrar{runtime: authRuntime}, resourceRepository); err != nil {
		return nil, fmt.Errorf("register resource administrator routes: %w", err)
	}
	if err := resources.RegisterManagedDiscoveryRoutes(authRuntime, resourceAdminRouteRegistrar{runtime: authRuntime}, resourceRepository); err != nil {
		return nil, fmt.Errorf("register managed model discovery routes: %w", err)
	}
	if err := donation.RegisterOwnerRoutes(authRuntime, donationService); err != nil {
		return nil, fmt.Errorf("register donation owner routes: %w", err)
	}
	if err := donation.RegisterAdminRoutes(authRuntime, donationService); err != nil {
		return nil, fmt.Errorf("register donation administrator routes: %w", err)
	}
	if err := donation.RegisterStewardRoutes(authRuntime, donationService); err != nil {
		return nil, fmt.Errorf("register donation steward routes: %w", err)
	}
	if err := charityrouting.RegisterOwnerRoutes(authRuntime, charityRoutingService); err != nil {
		return nil, fmt.Errorf("register charity capability routes: %w", err)
	}
	if err := charityrouting.RegisterAdminRoutes(authRuntime, charityRoutingService); err != nil {
		return nil, fmt.Errorf("register charity administrator routes: %w", err)
	}
	if err := charityrouting.RegisterStewardRoutes(authRuntime, charityRoutingService); err != nil {
		return nil, fmt.Errorf("register charity steward routes: %w", err)
	}
	if err := checkin.RegisterRoutes(authRuntime, checkinService); err != nil {
		return nil, fmt.Errorf("register check-in routes: %w", err)
	}
	activityRoutes := activityRouteRegistrar{runtime: authRuntime}
	if err := activities.RegisterRoutes(activityRoutes, activityRoutes, activityService); err != nil {
		return nil, fmt.Errorf("register activities routes: %w", err)
	}
	announcementRoutes := announcementRouteRegistrar{runtime: authRuntime}
	if err := announcements.RegisterRoutes(announcementRoutes, announcementRoutes, announcementService); err != nil {
		return nil, fmt.Errorf("register announcement routes: %w", err)
	}
	if err := announcements.RegisterStewardRoutes(announcementRoutes, announcementService); err != nil {
		return nil, fmt.Errorf("register steward announcement routes: %w", err)
	}
	if err := issues.RegisterRoutes(issueRouteRegistrar{runtime: authRuntime}, issueService); err != nil {
		return nil, fmt.Errorf("register issue routes: %w", err)
	}
	if err := reportRepository.RegisterRoutes(authRuntime); err != nil {
		return nil, fmt.Errorf("register report routes: %w", err)
	}
	maintenanceRoutes := maintenanceRouteRegistrar{runtime: authRuntime}
	if err := maintenance.RegisterRoutes(maintenanceRoutes, maintenanceRoutes, maintenance.HTTPOptions{
		Database: store.DB(), Service: maintenanceService,
	}); err != nil {
		return nil, fmt.Errorf("register maintenance routes: %w", err)
	}
	if err := debug.RegisterRoutes(debugRouteRegistrar{runtime: authRuntime}, debugHub, debugMutations); err != nil {
		return nil, fmt.Errorf("register Debug routes: %w", err)
	}
	if err := logapi.RegisterUserRoutes(authRuntime, logRepository); err != nil {
		return nil, fmt.Errorf("register user log routes: %w", err)
	}
	if err := creditapi.RegisterUserRoutes(authRuntime, store.DB()); err != nil {
		return nil, fmt.Errorf("register credit history route: %w", err)
	}
	if err := logapi.RegisterStewardRoutes(authRuntime, logRepository, roleAuthorizer); err != nil {
		return nil, fmt.Errorf("register steward log routes: %w", err)
	}
	if err := logapi.RegisterAdminRoutes(authRuntime, logRepository); err != nil {
		return nil, fmt.Errorf("register administrator log routes: %w", err)
	}
	if err := audits.registerRoutes(authRuntime, logRepository, roleAuthorizer); err != nil {
		return nil, fmt.Errorf("register audit routes: %w", err)
	}
	if err := activityEngines.RegisterRoutes(authRuntime); err != nil {
		return nil, fmt.Errorf("register limited activity routes: %w", err)
	}
	lifecycleRoutes := lifecycleRouteRegistrar{runtime: authRuntime}
	if err := lifecycle.RegisterRoutes(lifecycleRoutes, lifecycleRoutes, lifecycleCoordinator); err != nil {
		return nil, fmt.Errorf("register account lifecycle routes: %w", err)
	}
	if err := timeapi.RegisterRoutes(
		authRuntime,
		resourceAdminRouteRegistrar{runtime: authRuntime},
		&timeContextResolver{store: store, authority: roleAuthorizer},
	); err != nil {
		return nil, fmt.Errorf("register time API routes: %w", err)
	}
	if err := registerAccountEventRoute(authRuntime, gate, gameRuntimes.AccountContinuation(), activityEvents, accountConnections); err != nil {
		return nil, fmt.Errorf("register account event route: %w", err)
	}
	if _, err := maintenanceService.PrepareListener(startupContext, store.DB()); err != nil {
		return nil, fmt.Errorf("prepare maintenance state: %w", err)
	}
	if err := gameRuntimes.ValidatePersistedState(startupContext); err != nil {
		return nil, fmt.Errorf("validate game persisted state: %w", err)
	}
	if err := charityService.ValidateRecurringState(startupContext); err != nil {
		return nil, fmt.Errorf("validate recurring charity limits: %w", err)
	}
	if err := recoverRankingsAndLifecycleBeforeListener(startupContext, rankingService, lifecycleCoordinator, gameNow().Unix()); err != nil {
		return nil, fmt.Errorf("recover account lifecycle before listener: %w", err)
	}

	automationService, err := stewardautomation.New(stewardautomation.Config{Database: store.DB(), Authorizer: authorizer, Resources: resourceRepository, Donations: donationService, Charity: charityRoutingService})
	if err != nil {
		return nil, fmt.Errorf("create steward automation service: %w", err)
	}
	automationHandler, err := newStewardAutomationHandler(automationService, resourceRepository, forwardRuntime.lifecycle, gate)
	if err != nil {
		return nil, err
	}
	mux, err := generationTwoMux(cfg, store, authRuntime, forwardRuntime.handler, automationHandler)
	if err != nil {
		return nil, err
	}
	handler, err := stationBoundary(cfg, audits.Wrap(mux))
	if err != nil {
		return nil, err
	}
	if err := startupContext.Err(); err != nil {
		return nil, err
	}
	db.RecordStartupStage(startupContext, db.StageRoutesReady)
	if err := startupContext.Err(); err != nil {
		return nil, err
	}
	readiness := &readinessState{}
	mux.HandleFunc("/readyz", readiness.serveHTTP)
	if err := gameRuntimes.StartWorker(workerContext); err != nil {
		return nil, fmt.Errorf("start game workers: %w", err)
	}
	lifecycleCancel, lifecycleDone, err := startLifecycleWorker(workerContext, lifecycleCoordinator)
	if err != nil {
		return nil, fmt.Errorf("start account lifecycle worker: %w", err)
	}
	failures := make(chan error, 1)
	rankingContext, rankingCancel := context.WithCancel(workerContext)
	rankingDone := make(chan struct{})
	go func() { defer close(rankingDone); rankingService.Run(rankingContext) }()
	audits.Start(workerContext)
	activityEngines.Start(workerContext, failures, func() { readiness.failed.Store(true) })
	return &application{
		readiness:       readiness,
		workerCancel:    workerCancel,
		audits:          audits,
		activityRuntime: activityEngines,
		handler:         handler,
		authRuntime:     authRuntime,
		bridge:          bridgeRuntime,
		claims:          claimService,
		resourceRepo:    resourceRepository,
		discoveryWorker: discoveryWorker,
		donations:       donationService,
		charity:         charityService,
		charityRouting:  charityRoutingService,
		adaptations:     adaptations,
		checkin:         checkinService,
		homeGames:       homeGameService,
		announcements:   announcementService,
		issues:          issueService,
		reports:         reportRepository,
		activities:      activityService,
		activityRepo:    activityRepository,
		activityEvents:  activityEvents,
		adminConfig:     adminConfigRuntime,
		adminAlerts:     adminAlertRepository,
		adminUsers:      adminUserService,
		lifecycle:       lifecycleCoordinator,
		lifecycleCancel: lifecycleCancel,
		lifecycleDone:   lifecycleDone,
		rankingCancel:   rankingCancel,
		rankingDone:     rankingDone,
		debug:           debugHub,
		logs:            logRepository,
		accountEvents:   accountConnections,
		forward:         forwardRuntime,
		games:           gameRuntimes,
		failures:        failures,
		authorizer:      authorizer,
		elevation:       elevationManager,
		gate:            gate,
		registry:        registry,
		maintenance:     maintenanceService,
		egress:          outbound,
	}, nil
}

const lifecycleRecoveryBatch = 100

func recoverAnnouncementsBeforeListener(ctx context.Context, service *announcements.Service) error {
	if ctx == nil || service == nil {
		return errors.New("announcement recovery dependencies are required")
	}
	for {
		result, err := service.RecoverBeforeListener(ctx, lifecycleRecoveryBatch)
		if err != nil {
			return fmt.Errorf("recover announcements before listener: %w", err)
		}
		if result.Expired < lifecycleRecoveryBatch && result.ActorsDeidentified < lifecycleRecoveryBatch &&
			result.AuditsDeleted < lifecycleRecoveryBatch {
			return nil
		}
	}
}

func recoverIssuesBeforeListener(ctx context.Context, service *issues.Service) error {
	if ctx == nil || service == nil {
		return errors.New("issue recovery dependencies are required")
	}
	if _, _, err := service.RecoverBeforeListener(ctx, lifecycleRecoveryBatch); err != nil {
		return fmt.Errorf("recover issues before listener: %w", err)
	}
	return nil
}
