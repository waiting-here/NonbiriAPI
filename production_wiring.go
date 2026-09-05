package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/charity"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	gameruntime "github.com/waiting-here/NonbiriAPI/internal/game/runtime"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type publicForwardRuntime struct {
	service   *forward.Service
	flow      *flowcontrol.Controller
	abuse     *antiabuse.Service
	lifecycle *lifecyclegate.Gate
	handler   http.Handler
}

func newPublicForwardRuntime(
	store *db.Store,
	vault *secret.Vault,
	claims *claim.Service,
	charityService *charity.Service,
	charityRoutes *charityrouting.Service,
	resourcesRepository *resources.Repository,
	registry *connector.Registry,
	outboundBackend backend.Backend,
	debugHub *debug.Hub,
	maintenanceGate *maintenance.Gate,
	rpm ratelimit.RPMConfig,
	onBan ...func(int64),
) (*publicForwardRuntime, error) {
	if store == nil || vault == nil || claims == nil || charityService == nil || charityRoutes == nil ||
		resourcesRepository == nil || registry == nil || outboundBackend == nil || debugHub == nil || maintenanceGate == nil {
		return nil, errors.New("public forward runtime dependencies are required")
	}
	lifecycle, err := lifecyclegate.New(lifecyclegate.Config{})
	if err != nil {
		return nil, fmt.Errorf("create caller lifecycle gate: %w", err)
	}
	var abuse *antiabuse.Service
	flow, err := flowcontrol.New(flowcontrol.Config{RPM: rpm, UserLimits: flowcontrol.DBUserLimitResolver(store),
		OnDenied: func(ctx context.Context, userID int64, reason ratelimit.RPMReason) {
			if abuse != nil {
				abuse.RPMDenied(ctx, userID, reason)
			}
		},
	})
	if err != nil {
		_ = lifecycle.Close()
		return nil, fmt.Errorf("create forward flow controller: %w", err)
	}
	fail := func(err error) (*publicForwardRuntime, error) {
		_ = abuse.Close()
		_ = flow.Close()
		_ = lifecycle.Close()
		return nil, err
	}
	var invalidate func(int64)
	if len(onBan) > 0 {
		invalidate = onBan[0]
	}
	abuse, err = antiabuse.NewService(antiabuse.ServiceConfig{Database: store.DB(), Rejections: claims, OnBan: invalidate,
		BeginUserRetirement: func(ctx context.Context, userID int64) (antiabuse.Retirement, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return flow.BeginUserRetirement(userID)
		},
	})
	if err != nil {
		return fail(fmt.Errorf("create request abuse prevention: %w", err))
	}
	claimRail, err := forward.NewClaimServiceAdapter(claims)
	if err != nil {
		return fail(fmt.Errorf("create forward claim adapter: %w", err))
	}
	routingStore, err := routing.New(store)
	if err != nil {
		return fail(fmt.Errorf("create forward routing store: %w", err))
	}
	personal, err := forward.NewPersonalRoutingAdapter(routingStore)
	if err != nil {
		return fail(fmt.Errorf("create forward personal adapter: %w", err))
	}
	charity, err := forward.NewCharityRoutingAdapter(charityRoutes)
	if err != nil {
		return fail(fmt.Errorf("create forward charity adapter: %w", err))
	}
	safety, err := forward.NewSafetyIdentifierFactory(vault)
	if err != nil {
		return fail(fmt.Errorf("create safety identifier factory: %w", err))
	}
	provider := anthropicDefaultMaxTokensProvider{store: store}
	connectors := make([]connector.Connector, 0, len(registry.Types()))
	for _, connectorType := range registry.Types() {
		instance, createErr := registry.NewConnector(connectorType, connector.Dependencies{
			Backend: outboundBackend, AnthropicDefaultMaxTokens: provider,
		})
		if createErr != nil {
			_ = safety.Close()
			return fail(fmt.Errorf("create %s connector: %w", connectorType, createErr))
		}
		connectors = append(connectors, instance)
	}
	service, err := forward.NewService(forward.Config{
		Personal: personal, Charity: charityPolicyRouter{CharityRouter: charity, abuse: abuse}, Claims: claimRail, CharityCharges: charityService,
		Debug: debugHub, Registry: registry, Connectors: connectors, Safety: safety,
	})
	if err != nil {
		_ = safety.Close()
		return fail(fmt.Errorf("create public forward service: %w", err))
	}
	flowMiddleware, err := flowcontrol.NewMiddleware(flow, forward.CallerIdentity)
	if err != nil {
		_ = service.Close()
		return fail(fmt.Errorf("create forward flow middleware: %w", err))
	}
	callerKey, err := forward.NewCallerKeyMiddleware(resourcesRepository, lifecycle)
	if err != nil {
		_ = service.Close()
		return fail(fmt.Errorf("create CallerKey middleware: %w", err))
	}
	handler := maintenance.GateMiddleware(maintenanceGate,
		callerKey.Wrap(flowMiddleware.Wrap(forward.NewHandler(service))))
	return &publicForwardRuntime{service: service, flow: flow, abuse: abuse, lifecycle: lifecycle, handler: handler}, nil
}

type charityPolicyRouter struct {
	forward.CharityRouter
	abuse *antiabuse.Service
}

func (router charityPolicyRouter) Preflight(ctx context.Context, userID int64, model string, request *openai.ChatRequest, now int64) (forward.CharityPreflight, error) {
	value, err := router.CharityRouter.Preflight(ctx, userID, model, request, now)
	var short *charityrouting.ContentTooShortError
	if !errors.As(err, &short) {
		return value, err
	}
	rejection, recordErr := router.abuse.RecordShort(ctx, userID, model, short.Actual)
	if recordErr != nil {
		return forward.CharityPreflight{}, recordErr
	}
	if rejection == nil {
		return router.CharityRouter.Preflight(ctx, userID, model, request, now)
	}
	return forward.CharityPreflight{}, rejection
}

var _ forward.CharityRouter = charityPolicyRouter{}

func (runtime *publicForwardRuntime) BeginShutdown() {
	if runtime != nil && runtime.lifecycle != nil {
		_ = runtime.lifecycle.Close()
	}
}

func (runtime *publicForwardRuntime) Close() error {
	if runtime == nil {
		return nil
	}
	var failures []error
	if runtime.service != nil {
		failures = append(failures, runtime.service.Close())
	}
	if runtime.flow != nil {
		failures = append(failures, runtime.abuse.Close())
		failures = append(failures, runtime.flow.Close())
	}
	if runtime.lifecycle != nil {
		failures = append(failures, runtime.lifecycle.Close())
	}
	return errors.Join(failures...)
}

type rpsPublishReporter struct{}

func (rpsPublishReporter) ReportRPSPublishError(err error) {
	if err != nil {
		activityPublishReporter{}.ReportActivitiesPublishError(fmt.Errorf("RPS account event publication: %w", err))
	}
}

type gameRuntimeBundle struct {
	limiter  *game.StartLimiter
	linklink *linklink.Service
	rps      *rps.Service
	fishing  *gameruntime.Service
}

func newGameRuntimeBundle(
	store *db.Store,
	vault *secret.Vault,
	authRuntime *auth.Runtime,
	roleAuthorizer *roleFinalTxAuthorizer,
	continuation *maintenance.Service,
	pools *activities.Repository,
	activityEvents *activities.AccountstreamPublisher,
	accountEvents *accountstream.Hub,
	sources *accountEventSources,
) (*gameRuntimeBundle, error) {
	if store == nil || vault == nil || authRuntime == nil || roleAuthorizer == nil || continuation == nil ||
		pools == nil || activityEvents == nil || accountEvents == nil || sources == nil {
		return nil, errors.New("game runtime dependencies are required")
	}
	limiter, err := game.NewStartLimiter(game.StartLimiterConfig{})
	if err != nil {
		return nil, fmt.Errorf("create shared game start limiter: %w", err)
	}
	bundle := &gameRuntimeBundle{limiter: limiter}
	fail := func(err error) (*gameRuntimeBundle, error) {
		_ = bundle.Close()
		return nil, err
	}
	bundle.linklink, err = linklink.New(linklink.Options{
		Store: store, UserAuthorizer: authRuntime, Continuation: continuation, Limiter: limiter,
	})
	if err != nil {
		return fail(fmt.Errorf("create LinkLink runtime: %w", err))
	}
	bundle.rps, err = rps.New(rps.Options{
		Store: store, UserAuthorizer: authRuntime, Continuation: continuation, Limiter: limiter,
		Pools: pools, AccountEvents: accountEvents, ActivityEvents: activityEvents, Keys: vault,
		PublishErrors: rpsPublishReporter{},
	})
	if err != nil {
		return fail(fmt.Errorf("create RPS runtime: %w", err))
	}
	if err := sources.BindRPS(bundle.rps); err != nil {
		return fail(fmt.Errorf("bind RPS account event source: %w", err))
	}
	leaderboardKey, err := vault.DeriveGenerationTwoSubkey([]byte("game-leaderboard-tie/v1"))
	if err != nil {
		return fail(fmt.Errorf("derive game leaderboard key: %w", err))
	}
	defer clear(leaderboardKey)
	capability := game.RuntimeCapabilityFunc(func(gameID, mode, spec string) bool {
		switch gameID {
		case game.FishingID:
			return mode == "" && spec == ""
		case game.LinkLinkID:
			return mode == "" && game.ResolveSpec(game.LinkLinkID, spec) == nil
		case game.RPSID:
			return bundle.rps.Available(gameID, mode, spec)
		default:
			return false
		}
	})
	adminAuthorization := gameruntime.AdminFinalAuthorizerFunc(func(ctx context.Context, tx *sql.Tx) error {
		actor, ok := auth.ActorFromContext(ctx)
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			return authz.ErrUnauthorized
		}
		return roleAuthorizer.AuthorizeAdminMutation(ctx, tx, actor.UserID)
	})
	bundle.fishing, err = gameruntime.New(gameruntime.Options{
		Store: store, UserAuthorizer: authRuntime, AdminAuthorizer: adminAuthorization,
		Limiter: limiter, LeaderboardTieKey: leaderboardKey, Capability: capability, RPSHealth: bundle.rps,
	})
	if err != nil {
		return fail(fmt.Errorf("create Fishing runtime: %w", err))
	}
	return bundle, nil
}

func (bundle *gameRuntimeBundle) Close() error {
	if bundle == nil {
		return nil
	}
	var failures []error
	if bundle.rps != nil {
		failures = append(failures, bundle.rps.Close())
	}
	if bundle.linklink != nil {
		failures = append(failures, bundle.linklink.Close())
	}
	if bundle.fishing != nil {
		failures = append(failures, bundle.fishing.Close())
	} else if bundle.limiter != nil {
		failures = append(failures, bundle.limiter.Close())
	}
	return errors.Join(failures...)
}
