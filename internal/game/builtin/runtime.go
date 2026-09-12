// Package builtin assembles the audited production game modules and adapters.
package builtin

import (
	"context"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	builtinconfig "github.com/waiting-here/NonbiriAPI/internal/game/builtin/config"
	builtinfinance "github.com/waiting-here/NonbiriAPI/internal/game/builtin/finance"
	fishingruntime "github.com/waiting-here/NonbiriAPI/internal/game/fishing/runtime"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type AccountEventSource interface {
	accountstream.SnapshotAdapter
	accountstream.IdentityEpochGuard
}
type AccountContinuation interface {
	Read(context.Context, rps.ReadInput) (rps.HomeState, error)
}
type Options struct {
	Store             *db.Store
	Vault             *secret.Vault
	UserAuthorizer    resources.FinalTxAuthorizer
	AdminAuthorizer   host.AdminAuthorizer
	Continuation      *maintenance.Service
	Pools             rps.PoolRepository
	AccountEvents     rps.AccountEventSink
	ActivityEvents    rps.ActivityPublisher
	PublishErrors     rps.PublishErrorReporter
	BindAccountSource func(AccountEventSource) error
	Now               func() time.Time
}
type Runtime struct {
	*host.Service
	accountContinuation AccountContinuation
}

func (runtime *Runtime) AccountContinuation() AccountContinuation {
	if runtime == nil {
		return nil
	}
	return runtime.accountContinuation
}

func New(options Options) (*Runtime, error) {
	if options.Store == nil || options.Vault == nil || options.Continuation == nil || options.Pools == nil || options.AccountEvents == nil || options.ActivityEvents == nil || options.BindAccountSource == nil {
		return nil, errors.New("game modules: missing production dependency")
	}
	registry, err := builtinconfig.Registry()
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{}
	factories := map[string]host.Factory{
		game.FishingID: func(shared host.Services) (*host.Module, error) {
			financial, err := builtinfinance.ForModule(game.FishingID)
			if err != nil {
				return nil, err
			}
			key, err := options.Vault.DeriveGenerationTwoSubkey([]byte("game-leaderboard-tie/v1"))
			if err != nil {
				return nil, err
			}
			defer clear(key)
			service, err := fishingruntime.New(fishingruntime.Options{Store: options.Store, Finance: financial.Fishing, Pools: options.Pools, ActivityEvents: options.ActivityEvents, UserAuthorizer: shared.UserAuthorizer, Limiter: shared.Limiter, LeaderboardTieKey: key, Now: shared.Now})
			if err != nil {
				return nil, err
			}
			return service.Module(), nil
		},
		game.LinkLinkID: func(shared host.Services) (*host.Module, error) {
			financial, err := builtinfinance.ForModule(game.LinkLinkID)
			if err != nil {
				return nil, err
			}
			service, err := linklink.New(linklink.Options{Store: options.Store, Finance: financial.LinkLink, UserAuthorizer: shared.UserAuthorizer, Continuation: options.Continuation, Limiter: shared.Limiter, Now: shared.Now})
			if err != nil {
				return nil, err
			}
			return service.Module(), nil
		},
		game.RPSID: func(shared host.Services) (*host.Module, error) {
			financial, err := builtinfinance.ForModule(game.RPSID)
			if err != nil {
				return nil, err
			}
			service, err := rps.New(rps.Options{Store: options.Store, Finance: financial.RPS, UserAuthorizer: shared.UserAuthorizer, Continuation: options.Continuation, Limiter: shared.Limiter, Pools: options.Pools, AccountEvents: options.AccountEvents, ActivityEvents: options.ActivityEvents, Keys: options.Vault, PublishErrors: options.PublishErrors, Now: shared.Now})
			if err != nil {
				return nil, err
			}
			runtime.accountContinuation = service
			return service.Module(), options.BindAccountSource(service)
		},
	}
	runtime.Service, err = host.New(host.Options{Database: options.Store.DB(), Registry: registry, Factories: factories, UserAuthorizer: options.UserAuthorizer, AdminAuthorizer: options.AdminAuthorizer, Now: options.Now})
	if err != nil {
		return nil, err
	}
	return runtime, nil
}
