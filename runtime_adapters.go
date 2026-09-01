package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/connector/anthropic"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type anthropicDefaultMaxTokensProvider struct {
	store *db.Store
}

func (provider anthropicDefaultMaxTokensProvider) RawAnthropicDefaultMaxTokens(ctx context.Context) (*int64, error) {
	if provider.store == nil || ctx == nil {
		return nil, errors.New("anthropic max-tokens configuration is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := provider.store.GetSiteConfigValueContext(ctx, adminapi.KeyAnthropicDefaultMaxTokens)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 || value > anthropic.MaxMaxTokens {
		return nil, errors.New("anthropic max-tokens configuration is invalid")
	}
	return &value, nil
}

func loadRuntimeLimits(store *db.Store) (ratelimit.RPMConfig, egress.ConcurrencyLimits, error) {
	if store == nil {
		return ratelimit.RPMConfig{}, egress.ConcurrencyLimits{}, errors.New("runtime limit store is required")
	}
	values, err := store.GetAllSiteConfigValues()
	if err != nil {
		return ratelimit.RPMConfig{}, egress.ConcurrencyLimits{}, err
	}
	parse := func(key string, maximum int) (int, error) {
		raw, ok := values[key]
		if !ok {
			return 0, fmt.Errorf("runtime setting %s is missing", key)
		}
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1 || value > maximum {
			return 0, fmt.Errorf("runtime setting %s is invalid", key)
		}
		return value, nil
	}
	globalRPM, err := parse(adminapi.KeyGlobalRPM, 4096)
	if err != nil {
		return ratelimit.RPMConfig{}, egress.ConcurrencyLimits{}, err
	}
	defaultRPM, err := parse(adminapi.KeyDefaultRPMPerUser, 4096)
	if err != nil {
		return ratelimit.RPMConfig{}, egress.ConcurrencyLimits{}, err
	}
	egressGlobal, err := parse(egress.GlobalConcurrencyConfigKey, 100000)
	if err != nil {
		return ratelimit.RPMConfig{}, egress.ConcurrencyLimits{}, err
	}
	egressPerEndpoint, err := parse(egress.PerEndpointConcurrencyConfigKey, 100000)
	if err != nil {
		return ratelimit.RPMConfig{}, egress.ConcurrencyLimits{}, err
	}
	rpm := ratelimit.DefaultRPMConfig()
	rpm.GlobalLimit = globalRPM
	rpm.PerUserLimit = defaultRPM
	return rpm, egress.ConcurrencyLimits{Global: egressGlobal, PerEndpoint: egressPerEndpoint}, nil
}

type debugIdentityAuthority struct {
	runtime *auth.Runtime
}

func (authority debugIdentityAuthority) VerifyDebugIdentity(ctx context.Context, userID int64, binding string) (debug.IdentityState, error) {
	if authority.runtime == nil {
		return debug.IdentityUncertain, errors.New("debug identity authority is unavailable")
	}
	state, err := authority.runtime.VerifyUserSessionBinding(ctx, userID, binding)
	switch state {
	case auth.UserSessionBindingActive:
		return debug.IdentityActive, err
	case auth.UserSessionBindingRevoked:
		return debug.IdentityRevoked, err
	case auth.UserSessionBindingBanned:
		return debug.IdentityBanned, err
	case auth.UserSessionBindingDeleted:
		return debug.IdentityDeleted, err
	default:
		return debug.IdentityUncertain, err
	}
}

type debugRouteRegistrar struct {
	runtime *auth.Runtime
}

func (registrar debugRouteRegistrar) RegisterDebugUserRoute(method, pattern string, handler debug.AuthorizedUserHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterUserRoute(method, pattern, func(writer http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.UserID != principal.UserID || actor.SessionTokenHash == "" {
			httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(writer, request, debug.UserPrincipal{UserID: principal.UserID, SessionBinding: actor.SessionTokenHash})
	})
}

type rpsAccountEventSource interface {
	accountstream.SnapshotAdapter
	accountstream.IdentityEpochGuard
}

// accountEventSources breaks the startup-only cycle between the shared Hub
// and RPS. RPS is bound before recovery, route freezing, or worker startup.
type accountEventSources struct {
	activities accountstream.SnapshotAdapter
	rps        rpsAccountEventSource
}

func newAccountEventSources(activities accountstream.SnapshotAdapter) (*accountEventSources, error) {
	if activities == nil {
		return nil, errors.New("activities account event source is required")
	}
	return &accountEventSources{activities: activities}, nil
}

func (sources *accountEventSources) BindRPS(source rpsAccountEventSource) error {
	if sources == nil || source == nil || sources.rps != nil {
		return errors.New("RPS account event source cannot be bound")
	}
	sources.rps = source
	return nil
}

func (sources *accountEventSources) Snapshot(ctx context.Context, accountID int64, channel accountstream.Channel) (accountstream.Snapshot, error) {
	if sources == nil {
		return accountstream.Snapshot{}, accountstream.ErrSnapshot
	}
	switch channel {
	case accountstream.ChannelActivities:
		return sources.activities.Snapshot(ctx, accountID, channel)
	case accountstream.ChannelRPS:
		if sources.rps == nil {
			return accountstream.Snapshot{}, accountstream.ErrSnapshot
		}
		return sources.rps.Snapshot(ctx, accountID, channel)
	default:
		return accountstream.Snapshot{}, accountstream.ErrSnapshot
	}
}

func (sources *accountEventSources) CurrentIdentityEpoch(ctx context.Context, accountID int64) (*string, error) {
	if sources == nil || sources.rps == nil {
		return nil, accountstream.ErrSnapshot
	}
	return sources.rps.CurrentIdentityEpoch(ctx, accountID)
}

type accountEventConnections struct {
	mu     sync.Mutex
	next   uint64
	users  map[int64]map[uint64]context.CancelFunc
	closed bool
}

func newAccountEventConnections() *accountEventConnections {
	return &accountEventConnections{users: make(map[int64]map[uint64]context.CancelFunc)}
}

func (connections *accountEventConnections) Register(parent context.Context, userID int64) (context.Context, func(), error) {
	if connections == nil || parent == nil || userID <= 0 {
		return nil, nil, errors.New("account event connection is invalid")
	}
	ctx, cancel := context.WithCancel(parent)
	connections.mu.Lock()
	if connections.closed {
		connections.mu.Unlock()
		cancel()
		return nil, nil, accountstream.ErrClosed
	}
	connections.next++
	id := connections.next
	if connections.users[userID] == nil {
		connections.users[userID] = make(map[uint64]context.CancelFunc)
	}
	connections.users[userID][id] = cancel
	connections.mu.Unlock()
	var once sync.Once
	unregister := func() {
		once.Do(func() {
			connections.mu.Lock()
			if entries := connections.users[userID]; entries != nil {
				delete(entries, id)
				if len(entries) == 0 {
					delete(connections.users, userID)
				}
			}
			connections.mu.Unlock()
			cancel()
		})
	}
	return ctx, unregister, nil
}

func (connections *accountEventConnections) CancelUser(userID int64) {
	if connections == nil || userID <= 0 {
		return
	}
	connections.mu.Lock()
	entries := connections.users[userID]
	delete(connections.users, userID)
	cancels := make([]context.CancelFunc, 0, len(entries))
	for _, cancel := range entries {
		cancels = append(cancels, cancel)
	}
	connections.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (connections *accountEventConnections) Close() error {
	if connections == nil {
		return nil
	}
	connections.mu.Lock()
	if connections.closed {
		connections.mu.Unlock()
		return nil
	}
	connections.closed = true
	cancels := make([]context.CancelFunc, 0)
	for _, entries := range connections.users {
		for _, cancel := range entries {
			cancels = append(cancels, cancel)
		}
	}
	connections.users = make(map[int64]map[uint64]context.CancelFunc)
	connections.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

type userSessionInvalidationFanout struct {
	debug       *debug.Hub
	connections *accountEventConnections
}

func (fanout *userSessionInvalidationFanout) UserSessionInvalidated(userID int64) {
	if fanout == nil || userID <= 0 {
		return
	}
	if fanout.connections != nil {
		fanout.connections.CancelUser(userID)
	}
	if fanout.debug != nil {
		_ = fanout.debug.TerminateUser(userID, debug.EndAuthRevoked)
	}
}
