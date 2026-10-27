package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/adminusers"
	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
	"github.com/waiting-here/NonbiriAPI/internal/agentusers"
	"github.com/waiting-here/NonbiriAPI/internal/aiagent"
	"github.com/waiting-here/NonbiriAPI/internal/announcements"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/charity"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/chatgptauth"
	"github.com/waiting-here/NonbiriAPI/internal/checkin"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	gamehost "github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/reports"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/resourcebridge"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/returnmarker"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/stewardautomation"
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
	Egress         *egress.Stack
	GameNow        func() time.Time
	ActivityNow    func() time.Time
	LakeRandom     interface{ Float53() (float64, error) }
	LakeMotionSeed func() (uint32, error)
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
	startupContext = db.ActiveRecoveryContext(startupContext)
	db.RecordStartupStage(startupContext, db.StageAccountRecovery)
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
		outbound, err = egress.NewStack(egress.StackOptions{CloudflareRoutes: cfg.CloudflareRoutes, CloudflareWorker: cfg.CloudflareWorker})
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
	var automationService *stewardautomation.Service
	var debugHub *debug.Hub
	var accountConnections *accountEventConnections
	var forwardRuntime *publicForwardRuntime
	var gameRuntimes *gameRuntimeBundle
	var audits *auditRuntime
	var returnAlerts *returnmarker.Recorder
	var claimService *claim.Service
	var assistant *aiagent.Service
	var assistantRequests *agentrequest.Runtime
	performance := observability.NewPerformance()
	store.SetConnectionObserver(performance)
	defer func() {
		if built != nil {
			return
		}
		partial := &application{
			agent: assistant, agentRequests: assistantRequests,
			activityRuntime: activityEngines, accountEvents: accountConnections,
			debug: debugHub, reports: reportRepository, lifecycle: lifecycleCoordinator,
			games: gameRuntimes, forward: forwardRuntime, audits: audits,
			returnAlerts:   returnAlerts,
			activityEvents: activityEvents, authRuntime: authRuntime, elevation: elevationManager,
			discoveryWorker: discoveryWorker, bridge: bridgeRuntime, egress: outbound,
			workerCancel: workerCancel, automation: automationService, claims: claimService,
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
	roleAuthorizer := &roleFinalTxAuthorizer{authorizer: authorizer}
	audits, err = newAuditRuntime(startupContext, store, vault, roleAuthorizer)
	if err != nil {
		return nil, fmt.Errorf("create audit runtime: %w", err)
	}
	audits.outbound = outbound
	artworkPolicy, err := loadArtworkPolicy(startupContext, store)
	if err != nil {
		return nil, fmt.Errorf("load artwork policy: %w", err)
	}
	adminConfigRepository, err := adminapi.NewSiteConfigRepository(adminapi.SiteConfigRepositoryOptions{
		Store: store, FinalAuthorizer: roleAuthorizer,
		Committed: func(change adminapi.SiteConfigCommit) {
			artworkPolicy.committed(change)
			audits.configurationChanged(change)
			for _, key := range change.Keys {
				if strings.HasPrefix(key, "mfa_required_level_") {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					err := authRuntime.InvalidateUnverifiedMFASessions(ctx)
					cancel()
					if err != nil {
						slog.Warn("MFA stream invalidation unavailable")
					}
					break
				}
			}
		},
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
	marker, err := returnmarker.New(vault)
	if err != nil {
		return nil, fmt.Errorf("create outbound return marker: %w", err)
	}
	outbound.SetReturnMarker(marker)
	returnAlerts = returnmarker.NewRecorder(workerContext, marker, adminAlertRepository.RecordReturnDetected)
	donationReview, err := initializeDonationReview(startupContext, store.DB(), vault)
	if err != nil {
		return nil, fmt.Errorf("initialize donation review identities: %w", err)
	}
	donationService, err := donation.New(donation.Config{
		Store:      store,
		OwnerAuth:  authRuntime,
		RoleAuth:   roleAuthorizer,
		CursorKeys: vault,
		Review:     donationReview,
		PendingSubmission: func(ctx context.Context, tx *sql.Tx, id int64) error {
			if assistant == nil {
				return aiagent.ErrUnavailable
			}
			return assistant.PendingSubmission(ctx, tx, id)
		},
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
	claimService, err = claim.New(claim.Dependencies{
		Performance:  performance,
		Observations: audits.observations,
		DB:           store.DB(),
		Secrets:      vault,
		Accounting:   claim.NewLedgerAccounting(),
		Charity:      charityService,
		Acceptance:   forward.CallerKeyAcceptanceGate{Next: maintenanceService},
	})
	if err != nil {
		return nil, fmt.Errorf("create claim service: %w", err)
	}
	bridgeRuntime, err = resourcebridge.New(resourcebridge.Config{
		ErrorScope: audits.observations.DiscoveryScope,
		Store:      store,
		Vault:      vault,
		Claims:     claimService,
		Backend:    localBackend,
		Review:     donationReview,
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
	adaptations, err := requestadaptation.New(requestadaptation.Config{DB: store.DB(), ReadDB: store.ReadDB(), Codec: vault, KeyDeriver: vault})
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
		PendingSubmission: func(ctx context.Context, tx *sql.Tx, id int64) error {
			if assistant == nil {
				return aiagent.ErrUnavailable
			}
			return assistant.PendingSubmission(ctx, tx, id)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create resource repository: %w", err)
	}
	charityRoutingService, err := charityrouting.New(charityrouting.Config{
		Adaptations:   adaptations,
		Store:         store,
		Connectors:    connectorRegistry,
		BaseURLs:      outbound,
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
	if err := recoverAnnouncementsBeforeListener(startupContext, announcementRepository); err != nil {
		return nil, err
	}
	if err := recoverIssuesBeforeListener(startupContext, issueService); err != nil {
		return nil, err
	}
	debugHub, err = debug.NewHub(debugIdentityAuthority{runtime: authRuntime})
	if err != nil {
		return nil, fmt.Errorf("create Debug hub: %w", err)
	}
	debugMutations, err := debug.NewMutationRepository(store.DB(), authRuntime)
	if err != nil {
		return nil, fmt.Errorf("create Debug mutation repository: %w", err)
	}
	logRepository, err := logapi.NewRepositoryWithReader(store.DB(), store.ReadDB(), vault, roleAuthorizer)
	if err != nil {
		return nil, fmt.Errorf("create log repository: %w", err)
	}
	gameRuntimes, err = newGameRuntimeBundle(
		store, vault, authRuntime, roleAuthorizer, maintenanceService, activityRepository,
		activityPublisher, activityEvents, accountSources,
		gameNow, options.LakeRandom, options.LakeMotionSeed,
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
	rankingService, err := ranking.NewWithReader(store.DB(), store.ReadDB(), authRuntime, gameNow)
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
	activityEngines, err = newActivityRuntime(startupContext, store, vault, authRuntime, roleAuthorizer,
		maintenanceService, outbound, audits, userInvalidations, gameRuntimes.CancelUserDuelsTx, gameRuntimes, options.ActivityNow)
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
	if err := activityEngines.rewards.AttachAbuse(forwardRuntime.abuse); err != nil {
		return nil, fmt.Errorf("attach redemption abuse protection: %w", err)
	}
	forwardRuntime.callerKey.SetReturnObserver(returnAlerts.Observe)
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
	automationService, err = stewardautomation.New(stewardautomation.Config{Database: store.DB(), Authorizer: authorizer, Resources: resourceRepository, Donations: donationService, Charity: charityRoutingService, Now: gameNow})
	if err != nil {
		return nil, fmt.Errorf("create automation service: %w", err)
	}
	managementHistory, err := configureManagementOperations(store, authRuntime, roleAuthorizer)
	if err != nil {
		return nil, fmt.Errorf("configure management history: %w", err)
	}
	automationService.ConfigureManagementHistory(managementHistory)
	lifecycleCoordinator, err = newLifecycleCoordinator(
		store, vault, authRuntime, roleAuthorizer, forwardRuntime, gameRuntimes,
		claimService, resourceRepository, issueService, logRepository,
		activityService, activityRepository, donationService, charityService, charityRoutingService,
		reportRepository, announcementRepository, maintenanceService,
		activityEvents, debugHub, gameNow, audits, activityEngines, automationService,
	)
	if err != nil {
		return nil, fmt.Errorf("create account lifecycle coordinator: %w", err)
	}
	assistantAuthority := agentAuthority{authorizer: authorizer}
	assistantUsers, err := agentusers.New(agentusers.Config{Users: adminUserService, Accounts: lifecycleCoordinator, Auth: authRuntime})
	if err != nil {
		return nil, fmt.Errorf("create assistant account operations: %w", err)
	}
	assistantAdminAuthorize := func(ctx context.Context, tx *sql.Tx) error {
		actor, ok := auth.ActorFromContext(ctx)
		if !ok {
			return authz.ErrUnauthorized
		}
		_, err := authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleAdministrator})
		return err
	}
	assistantChatGPT, err := chatgptauth.New(chatgptauth.Config{
		DB: store.DB(), ReadDB: store.ReadDB(), Stack: outbound, Secrets: vault, AdminAuthorize: assistantAdminAuthorize,
	})
	if err != nil {
		return nil, fmt.Errorf("create assistant ChatGPT connection: %w", err)
	}
	assistantRequests, err = agentrequest.New(agentrequest.Config{
		DB: store.DB(), ReadDB: store.ReadDB(), Stack: outbound, Flow: forwardRuntime.flow, Secrets: vault, ChatGPT: assistantChatGPT,
		Authorize: func(ctx context.Context, attr agentrequest.Attribution) error {
			if assistant == nil {
				return aiagent.ErrUnavailable
			}
			return assistant.AuthorizeRequest(ctx, attr)
		},
		AdminAuthorize: assistantAdminAuthorize,
	})
	if err != nil {
		return nil, fmt.Errorf("create assistant model runtime: %w", err)
	}
	assistant, err = aiagent.New(aiagent.Config{
		DB: store.DB(), ReadDB: store.ReadDB(), Driver: assistantRequests, Authorize: assistantAuthority.Authorize,
		History:    managementHistory,
		Operations: &agentOperations{read: store.ReadDB(), donations: donationService, charity: charityRoutingService, users: assistantUsers, authority: assistantAuthority, pressure: audits.pressure, history: managementHistory},
		Search:     agentSearch(store.ReadDB(), outbound, forwardRuntime.flow, assistantAuthority), Secrets: vault,
	})
	if err != nil {
		return nil, fmt.Errorf("create assistant workflows: %w", err)
	}
	userInvalidations.agentChanged = assistant.InvalidateActor
	if err = aiagent.RegisterRoutes(agentRouteRegistrar{runtime: authRuntime, database: store.ReadDB()}, assistant, assistantRequests); err != nil {
		return nil, fmt.Errorf("register assistant routes: %w", err)
	}
	if err = aiagent.RegisterDriverRoutes(agentRouteRegistrar{runtime: authRuntime, database: store.ReadDB()}, assistant, assistantRequests, assistantChatGPT); err != nil {
		return nil, fmt.Errorf("register assistant driver routes: %w", err)
	}
	app := &application{
		agent: assistant, agentRequests: assistantRequests,
		managementHistory: managementHistory,
		workerCancel:      workerCancel,
		audits:            audits,
		activityRuntime:   activityEngines,
		authRuntime:       authRuntime,
		bridge:            bridgeRuntime,
		claims:            claimService,
		resourceRepo:      resourceRepository,
		automation:        automationService,
		discoveryWorker:   discoveryWorker,
		donations:         donationService,
		charity:           charityService,
		charityRouting:    charityRoutingService,
		adaptations:       adaptations,
		checkin:           checkinService,
		homeGames:         homeGameService,
		announcements:     announcementService,
		issues:            issueService,
		reports:           reportRepository,
		activities:        activityService,
		activityRepo:      activityRepository,
		activityEvents:    activityEvents,
		adminConfig:       adminConfigRuntime,
		adminAlerts:       adminAlertRepository,
		returnAlerts:      returnAlerts,
		adminUsers:        adminUserService,
		lifecycle:         lifecycleCoordinator,
		debug:             debugHub,
		logs:              logRepository,
		accountEvents:     accountConnections,
		forward:           forwardRuntime,
		games:             gameRuntimes,
		authorizer:        authorizer,
		elevation:         elevationManager,
		gate:              gate,
		registry:          registry,
		maintenance:       maintenanceService,
		egress:            outbound,
	}
	if err := app.registerRoutes(store, roleAuthorizer, debugMutations); err != nil {
		return nil, err
	}
	if _, err := maintenanceService.PrepareListener(startupContext, store.DB()); err != nil {
		return nil, fmt.Errorf("prepare maintenance state: %w", err)
	}
	db.RecordStartupStage(startupContext, db.StageGameValidation)
	if err := gameRuntimes.ValidatePersistedState(startupContext); err != nil {
		return nil, fmt.Errorf("validate game persisted state: %w", err)
	}
	db.RecordStartupStage(startupContext, db.StageQuotaValidation)
	if err := charityService.ValidateRecurringState(startupContext); err != nil {
		return nil, fmt.Errorf("validate recurring charity limits: %w", err)
	}
	db.RecordStartupStage(startupContext, db.StageBusinessRecovery)
	if err := recoverRankingsAndLifecycleBeforeListener(startupContext, rankingService, lifecycleCoordinator, gameNow().Unix()); err != nil {
		return nil, fmt.Errorf("recover account lifecycle before listener: %w", err)
	}
	if err := assistant.Recover(startupContext); err != nil {
		return nil, fmt.Errorf("recover assistant workflows: %w", err)
	}
	if err := managementHistory.RecoverInterrupted(startupContext); err != nil {
		return nil, fmt.Errorf("recover management history: %w", err)
	}

	automationHandler, err := newAutomationHandler(automationService, resourceRepository, forwardRuntime.lifecycle, gate)
	if err != nil {
		return nil, err
	}
	automationHandler = wrapAutomationHistory(managementHistory, automationHandler)
	mux, err := generationTwoMux(cfg, store, authRuntime, forwardRuntime.handler, artworkPolicy.disabled, automationHandler)
	if err != nil {
		return nil, err
	}
	app.performance = performance
	workerContext = performance.Context(workerContext)
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
	if err := assistantRequests.Start(); err != nil {
		return nil, fmt.Errorf("start assistant model runtime: %w", err)
	}
	assistant.Start()
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
	performanceDone := make(chan struct{})
	go func() {
		defer close(performanceDone)
		performance.Run(workerContext, store.StatsByPool, func(window observability.PerformanceWindow) {
			slog.Info("runtime performance", "metrics", window)
		})
	}()
	app.performanceDone = performanceDone
	_, app.managementDone = startManagementCleanup(workerContext, managementHistory)
	app.readiness, app.handler, app.failures = readiness, performance.Wrap(handler), failures
	app.lifecycleCancel, app.lifecycleDone = lifecycleCancel, lifecycleDone
	app.rankingCancel, app.rankingDone = rankingCancel, rankingDone
	return app, nil
}

const lifecycleRecoveryBatch = 100

func recoverAnnouncementsBeforeListener(ctx context.Context, service *announcements.Repository) error {
	if ctx == nil || service == nil {
		return errors.New("announcement recovery dependencies are required")
	}
	for {
		result, err := service.ExpireDue(ctx, lifecycleRecoveryBatch)
		if err != nil {
			return fmt.Errorf("recover announcements before listener: %w", err)
		}
		if result < lifecycleRecoveryBatch {
			return nil
		}
	}
}

func recoverIssuesBeforeListener(ctx context.Context, service *issues.Service) error {
	if ctx == nil || service == nil {
		return errors.New("issue recovery dependencies are required")
	}
	if _, err := service.RebuildIncomplete(ctx, lifecycleRecoveryBatch); err != nil {
		return fmt.Errorf("recover issues before listener: %w", err)
	}
	return nil
}
