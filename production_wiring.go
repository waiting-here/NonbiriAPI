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
	gamebuiltin "github.com/waiting-here/NonbiriAPI/internal/game/builtin"
	gamehost "github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/stewardautomation"
)

type publicForwardRuntime struct {
	service   *forward.Service
	flow      *flowcontrol.Controller
	abuse     *antiabuse.Service
	lifecycle *lifecyclegate.Gate
	handler   http.Handler
}

func newStewardAutomationHandler(service *stewardautomation.Service, repository *resources.Repository, lifecycle *lifecyclegate.Gate, gate *maintenance.Gate) (http.Handler, error) {
	callerKey, err := forward.NewCallerKeyMiddleware(repository, lifecycle)
	if err != nil {
		return nil, err
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := forward.CallerKeyIdentity(r)
		if err != nil {
			httperr.WriteError(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		ctx := authz.WithStewardCaller(r.Context(), authz.StewardCaller{UserID: identity.UserID, Generation: identity.Generation})
		service.ServeHTTP(w, r.WithContext(ctx))
	})
	return maintenance.GateMiddleware(gate, callerKey.WrapExact(inner, map[string]string{
		stewardautomation.DonationsPath: http.MethodPost,
		stewardautomation.BindingsPath:  http.MethodPost,
	})), nil
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
			applyPublicRPMDenial(ctx, userID, reason, abuse)
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
	flowHandler, err := publicFlowHandler(flow, forward.NewHandler(service))
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
		callerKey.Wrap(flowHandler))
	return &publicForwardRuntime{service: service, flow: flow, abuse: abuse, lifecycle: lifecycle, handler: handler}, nil
}

type charityPolicyRouter struct {
	forward.CharityRouter
	abuse *antiabuse.Service
}

func applyPublicRPMDenial(ctx context.Context, userID int64, reason ratelimit.RPMReason, abuse *antiabuse.Service) {
	if abuse != nil && reason == ratelimit.RPMUserLimit && forward.CharityRPMDenial(ctx, userID) {
		abuse.RPMDenied(ctx, userID, reason)
	}
}

func publicFlowHandler(flow *flowcontrol.Controller, next http.Handler) (http.Handler, error) {
	middleware, err := flowcontrol.NewMiddleware(flow, forward.CallerIdentity)
	if err != nil {
		return nil, err
	}
	return forward.WithRPMDenialScope(middleware.Wrap(next)), nil
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

type gameRuntimeBundle = gamebuiltin.Runtime

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
	adminAuthorization := gamehost.AdminAuthorizerFunc(func(ctx context.Context, tx *sql.Tx) error {
		actor, ok := auth.ActorFromContext(ctx)
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			return authz.ErrUnauthorized
		}
		return roleAuthorizer.AuthorizeAdminMutation(ctx, tx, actor.UserID)
	})
	return gamebuiltin.New(gamebuiltin.Options{
		Store: store, Vault: vault, UserAuthorizer: authRuntime, AdminAuthorizer: adminAuthorization,
		Continuation: continuation, Pools: pools, AccountEvents: accountEvents, ActivityEvents: activityEvents,
		PublishErrors: rpsPublishReporter{}, BindAccountSource: func(source gamebuiltin.AccountEventSource) error {
			return sources.BindRPS(source)
		},
	})
}
