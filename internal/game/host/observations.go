package host

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (service *Service) GamesSnapshot(ctx context.Context, userID int64, now time.Time) (map[string]json.RawMessage, error) {
	if service == nil || ctx == nil || userID <= 0 || !validTime(now.UTC().Unix()) {
		return nil, ErrInvalidRequest
	}
	if service.closed.Load() {
		return nil, ErrClosed
	}
	tx, err := service.services.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, classifyDB(err)
	}
	defer tx.Rollback()
	if err = service.services.UserAuthorizer.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return nil, mapAuthorization(err)
	}
	configuration, _, err := service.readSnapshot(ctx, tx)
	if err != nil {
		return nil, err
	}
	account, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		return nil, ErrNotFound
	}
	result := map[string]json.RawMessage{"server_now": game.ConfigJSON(now.UTC().Unix()), "balance": game.ConfigJSON(formatWideMilli(account.Balance.Big())), "games_enabled": game.ConfigJSON(configuration.Enabled())}
	for _, descriptor := range service.registry.Descriptors() {
		value, _ := configuration.Value(descriptor.ID)
		fragment, err := service.modules[descriptor.ID].UserSnapshotTx(ctx, tx, userID, now.UTC().Unix(), value)
		if err != nil {
			return nil, classifyDB(err)
		}
		if !json.Valid(fragment.Config) || len(fragment.Config) > 65536 || len(fragment.Fields) != len(descriptor.SnapshotFields) {
			return nil, ErrInvariant
		}
		result[descriptor.ID] = fragment.Config
		for _, key := range descriptor.SnapshotFields {
			value, ok := fragment.Fields[key]
			if !ok || !json.Valid(value) || len(value) > 65536 {
				return nil, ErrInvariant
			}
			result[key] = value
		}
	}
	return result, nil
}

func (service *Service) HomeSummary(ctx context.Context, userID int64) (game.HomeSummary, error) {
	if service == nil || ctx == nil || userID <= 0 {
		return game.HomeSummary{}, ErrInvalidRequest
	}
	if service.closed.Load() {
		return game.HomeSummary{}, ErrClosed
	}
	tx, err := service.services.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return game.HomeSummary{}, classifyDB(err)
	}
	defer tx.Rollback()
	if err = service.services.UserAuthorizer.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return game.HomeSummary{}, mapAuthorization(err)
	}
	result := game.HomeSummary{Continue: []game.ContinueItem{}, PendingResults: []game.PendingResult{}}
	order := make(map[string]int)
	for _, descriptor := range service.registry.Descriptors() {
		order[descriptor.ID] = descriptor.StableOrder
		fragment, err := service.modules[descriptor.ID].HomeSummaryTx(ctx, tx, userID)
		if err != nil {
			return game.HomeSummary{}, classifyDB(err)
		}
		if len(fragment.Continue) > 1 {
			return game.HomeSummary{}, ErrInvariant
		}
		for _, item := range fragment.Continue {
			if item.Game != descriptor.ID || item.RouteID != descriptor.HomeRouteID || !descriptor.ValidResource(item.ResourceID) || item.State == "" || len(item.State) > 64 {
				return game.HomeSummary{}, ErrInvariant
			}
		}
		for _, item := range fragment.PendingResults {
			if item.Game != descriptor.ID || item.RouteID != descriptor.HomeRouteID || !descriptor.ValidResource(item.ResourceID) || !validTime(item.CreatedAt) {
				return game.HomeSummary{}, ErrInvariant
			}
		}
		if len(result.PendingResults)+len(fragment.PendingResults) > 100 {
			return game.HomeSummary{}, ErrResourceLimit
		}
		result.Continue = append(result.Continue, fragment.Continue...)
		result.PendingResults = append(result.PendingResults, fragment.PendingResults...)
	}
	sort.SliceStable(result.Continue, func(a, b int) bool { return order[result.Continue[a].Game] < order[result.Continue[b].Game] })
	sort.Slice(result.PendingResults, func(a, b int) bool {
		first, second := result.PendingResults[a], result.PendingResults[b]
		if first.CreatedAt != second.CreatedAt {
			return first.CreatedAt < second.CreatedAt
		}
		if first.Game != second.Game {
			return order[first.Game] < order[second.Game]
		}
		return first.ResourceID < second.ResourceID
	})
	return result, nil
}

func (service *Service) ActiveCounts(ctx context.Context) (game.ActiveCounts, error) {
	result := game.ActiveCounts{Games: []game.GameCount{}, Queues: []game.QueueCount{}}
	if service == nil || ctx == nil {
		return result, ErrInvalidRequest
	}
	if service.closed.Load() {
		return result, ErrClosed
	}
	tx, err := service.services.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, classifyDB(err)
	}
	defer tx.Rollback()
	for _, descriptor := range service.registry.Descriptors() {
		fragment, err := service.modules[descriptor.ID].ActiveCountsTx(ctx, tx)
		if err != nil {
			return game.ActiveCounts{}, classifyDB(err)
		}
		if len(fragment.Games) > 100 || len(fragment.Queues) > 100 {
			return game.ActiveCounts{}, ErrInvariant
		}
		for _, item := range fragment.Games {
			if item.Game != descriptor.ID || !validCount(item.Count) || item.Mode != nil && descriptor.ResolveMode(*item.Mode) != nil || item.Spec != nil && descriptor.ResolveSpec(*item.Spec) != nil {
				return game.ActiveCounts{}, ErrInvariant
			}
		}
		for _, item := range fragment.Queues {
			if descriptor.ResolveMode(item.Mode) != nil || !validCount(item.Count) {
				return game.ActiveCounts{}, ErrInvariant
			}
		}
		result.Games = append(result.Games, fragment.Games...)
		result.Queues = append(result.Queues, fragment.Queues...)
	}
	return result, nil
}

func validCount(value string) bool {
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == value
}

func formatWideMilli(value *big.Int) string {
	if value == nil {
		return "0"
	}
	negative := value.Sign() < 0
	absolute := new(big.Int).Abs(new(big.Int).Set(value))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(absolute, big.NewInt(1000), remainder)
	text := quotient.String()
	if remainder.Sign() != 0 {
		text += "." + strings.TrimRight(fmt.Sprintf("%03d", remainder.Int64()), "0")
	}
	if negative {
		text = "-" + text
	}
	return text
}
