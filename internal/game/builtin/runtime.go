// Package builtin assembles the audited production game modules and adapters.
package builtin

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding"
	biddingconfig "github.com/waiting-here/NonbiriAPI/internal/game/bidding/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
	builtinconfig "github.com/waiting-here/NonbiriAPI/internal/game/builtin/config"
	builtinfinance "github.com/waiting-here/NonbiriAPI/internal/game/builtin/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	fishingruntime "github.com/waiting-here/NonbiriAPI/internal/game/fishing/runtime"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes"
	likesconfig "github.com/waiting-here/NonbiriAPI/internal/game/likes/config"
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
	DuelAdminAudit    func(duel.AdminAudit)
	BindAccountSource func(AccountEventSource) error
	Now               func() time.Time
}
type Runtime struct {
	*host.Service
	accountContinuation AccountContinuation
	cancelUserDuelsTx   func(context.Context, *sql.Tx, int64, string, int64) (func(bool), error)
}

func (runtime *Runtime) CancelUserDuelsTx(ctx context.Context, tx *sql.Tx, user int64, reason string, now int64) (func(bool), error) {
	if runtime == nil || runtime.cancelUserDuelsTx == nil {
		return nil, game.ErrRuntimeUnavailable
	}
	return runtime.cancelUserDuelsTx(ctx, tx, user, reason, now)
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
	duels := map[string]*duel.Service{}
	var blackjackService *blackjack.Service
	duelFactory := func(descriptor game.ModuleDescriptor, rules duel.Rules) host.Factory {
		return func(shared host.Services) (*host.Module, error) {
			financial, err := builtinfinance.ForModule(descriptor.ID)
			if err != nil {
				return nil, err
			}
			service, err := duel.New(duel.Options{
				Database: shared.Database, Descriptor: descriptor, Rules: rules, Finance: financial.Duel,
				UserAuthorizer: shared.UserAuthorizer, AdminAuthorizer: shared.AdminAuthorizer,
				AdminAudit: options.DuelAdminAudit, Continuation: options.Continuation,
				Limiter: shared.Limiter, Pools: options.Pools, Publisher: options.ActivityEvents,
				Keys: options.Vault, Now: shared.Now, ReportError: func(err error) {
					if options.PublishErrors != nil {
						options.PublishErrors.ReportRPSPublishError(err)
					}
				},
			})
			if err != nil {
				return nil, err
			}
			duels[descriptor.ID] = service
			return service.Module(), nil
		}
	}
	likesRules, err := likes.NewRules()
	if err != nil {
		return nil, err
	}
	factories := map[string]host.Factory{
		game.BlackjackID: func(shared host.Services) (*host.Module, error) {
			financial, err := builtinfinance.ForModule(game.BlackjackID)
			if err != nil {
				return nil, err
			}
			service, err := blackjack.New(blackjack.Options{
				Database: shared.Database, Finance: financial.Blackjack, UserAuthorizer: shared.UserAuthorizer,
				AdminAuthorizer: shared.AdminAuthorizer, Continuation: options.Continuation, Limiter: shared.Limiter,
				Pools: options.Pools, Publisher: options.ActivityEvents, Keys: options.Vault, Now: shared.Now,
				ReportError: func(err error) {
					if options.PublishErrors != nil {
						options.PublishErrors.ReportRPSPublishError(err)
					}
				},
				AdminAudit: func(event blackjack.AdminAudit) {
					if options.DuelAdminAudit != nil {
						options.DuelAdminAudit(duel.AdminAudit{Actor: event.Actor, Role: "administrator", Game: game.BlackjackID, Dataset: event.Dataset, Filters: []string{}, Records: event.Records, Result: event.Result})
					}
				},
			})
			if err != nil {
				return nil, err
			}
			blackjackService = service
			return service.Module(), nil
		},
		game.BiddingID: duelFactory(biddingconfig.Descriptor(), bidding.Rules{}),
		game.LikesID:   duelFactory(likesconfig.Descriptor(), likesRules),
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
	cancelDuels, err := duel.Cancellation(duels[game.BiddingID], duels[game.LikesID])
	if err != nil {
		_ = runtime.Close()
		return nil, err
	}
	runtime.cancelUserDuelsTx = func(ctx context.Context, tx *sql.Tx, user int64, reason string, now int64) (func(bool), error) {
		finish, err := cancelDuels(ctx, tx, user, reason, now)
		if err != nil {
			return nil, err
		}
		end, err := blackjackService.CancelUserTx(ctx, tx, user, now)
		if err != nil {
			if finish != nil {
				finish(false)
			}
			return nil, err
		}
		return func(committed bool) {
			if finish != nil {
				finish(committed)
			}
			if end != nil {
				if committed {
					end.Commit()
				} else {
					end.Abort()
				}
			}
		}, nil
	}
	return runtime, nil
}
