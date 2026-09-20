package adapters

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type RankingAdapter struct{}

func (RankingAdapter) ExportRankings(ctx context.Context, tx *sql.Tx, request lifecycle.ExportRequest) (lifecycle.RankingExport, error) {
	ready, err := ranking.AdvanceTx(ctx, tx, request.DecisionNow)
	if errors.Is(err, ranking.ErrCatchingUp) || err == nil && !ready {
		return lifecycle.RankingExport{}, lifecycle.ErrUnavailable
	}
	if err != nil {
		return lifecycle.RankingExport{}, err
	}
	value, err := ranking.ExportTx(ctx, tx, request.UserID, request.DecisionNow, request.Limit)
	if errors.Is(err, ranking.ErrExportTooLarge) {
		return lifecycle.RankingExport{}, lifecycle.ErrTooLarge
	}
	if errors.Is(err, ranking.ErrCatchingUp) {
		return lifecycle.RankingExport{}, lifecycle.ErrUnavailable
	}
	if err != nil {
		return lifecycle.RankingExport{}, err
	}
	out := lifecycle.RankingExport{StatisticsStart: value.StatisticsStart,
		Totals: make([]lifecycle.RankingTotalExport, len(value.Totals)), Events: make([]lifecycle.RankingEventExport, len(value.Events))}
	for i, item := range value.Totals {
		out.Totals[i] = lifecycle.RankingTotalExport{Board: item.Board, Window: item.Window, Amount: item.Amount, AchievedAt: item.AchievedAt}
	}
	for i, item := range value.Events {
		out.Events[i] = lifecycle.RankingEventExport{Game: item.Game, SettledAt: item.SettledAt, Loss: cloneString(item.Loss), PositiveProfit: cloneString(item.PositiveProfit)}
	}
	return out, nil
}

type PenaltyAdapter struct{}

func (PenaltyAdapter) ExportPenalties(ctx context.Context, tx *sql.Tx, request lifecycle.ExportRequest) ([]lifecycle.PenaltyExport, error) {
	values, err := antiabuse.ExportTx(ctx, tx, request.UserID, request.DecisionNow, request.Limit)
	if errors.Is(err, antiabuse.ErrExportTooLarge) {
		return nil, lifecycle.ErrTooLarge
	}
	if err != nil {
		return nil, err
	}
	out := make([]lifecycle.PenaltyExport, len(values))
	for i, item := range values {
		out[i] = lifecycle.PenaltyExport{ID: item.ID, Kind: item.Kind, ReasonCode: item.ReasonCode,
			StartedAt: item.StartedAt, EndsAt: cloneInt64(item.EndsAt), EndedAt: cloneInt64(item.EndedAt), State: item.State, Result: item.Result,
			Actions: make([]lifecycle.PenaltyActionExport, len(item.Actions))}
		for j, action := range item.Actions {
			out[i].Actions[j] = lifecycle.PenaltyActionExport{Action: action.Action, OccurredAt: action.OccurredAt, ReasonCode: action.ReasonCode,
				PreviousEndsAt: cloneInt64(action.PreviousEndsAt), EndsAt: cloneInt64(action.EndsAt), RequestID: cloneString(action.RequestID), OperationID: cloneString(action.OperationID)}
		}
	}
	return out, nil
}

var (
	_ lifecycle.RankingExporter = RankingAdapter{}
	_ lifecycle.PenaltyExporter = PenaltyAdapter{}
)
