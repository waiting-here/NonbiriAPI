package adapters

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	fishingruntime "github.com/waiting-here/NonbiriAPI/internal/game/fishing/runtime"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type FishingLifecycleOwner interface {
	ExportTx(context.Context, *sql.Tx, int64, int64, int) (fishingruntime.UserExport, error)
	PrepareDeleteTx(context.Context, *sql.Tx, int64, int64) error
	Retain(context.Context, int64, int, time.Time) (fishingruntime.RetentionResult, error)
}

type LinkLinkLifecycleOwner interface {
	ExportTx(context.Context, *sql.Tx, int64, int64, int) (linklink.UserExport, *linklink.ExportFinalizer, error)
	PrepareDeleteTx(context.Context, *sql.Tx, int64, int64) (*linklink.DeletionFinalizer, error)
	Retain(context.Context, int64, int, time.Time) (linklink.RetentionResult, error)
}

type RPSLifecycleOwner interface {
	ExportTx(context.Context, *sql.Tx, int64, int64, int) (rps.UserExport, *rps.ExportFinalizer, error)
	PrepareDeleteTx(context.Context, *sql.Tx, int64, int64) (*rps.DeletionFinalizer, error)
	Retain(context.Context, int64, int, time.Time) (rps.RetentionResult, error)
}

type FishingAdapter struct {
	owner      FishingLifecycleOwner
	registered *host.Service
}
type LinkLinkAdapter struct {
	owner      LinkLinkLifecycleOwner
	registered *host.Service
}
type RPSAdapter struct {
	owner      RPSLifecycleOwner
	registered *host.Service
}

func NewRegisteredFishing(service *host.Service) *FishingAdapter {
	return &FishingAdapter{registered: service}
}
func NewRegisteredLinkLink(service *host.Service) *LinkLinkAdapter {
	return &LinkLinkAdapter{registered: service}
}
func NewRegisteredRPS(service *host.Service) *RPSAdapter { return &RPSAdapter{registered: service} }

func NewFishing(owner FishingLifecycleOwner) *FishingAdapter { return &FishingAdapter{owner: owner} }
func NewLinkLink(owner LinkLinkLifecycleOwner) *LinkLinkAdapter {
	return &LinkLinkAdapter{owner: owner}
}
func NewRPS(owner RPSLifecycleOwner) *RPSAdapter { return &RPSAdapter{owner: owner} }

func (adapter *FishingAdapter) ExportFishing(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.ExportRequest,
) (lifecycle.FishingExport, lifecycle.ExportFinalizer, error) {
	if adapter == nil || adapter.owner == nil && adapter.registered == nil {
		return lifecycle.FishingExport{}, nil, lifecycle.ErrUnavailable
	}
	var value fishingruntime.UserExport
	var finalizer host.Finalizer
	var err error
	if adapter.registered != nil {
		value, finalizer, err = exportRegisteredGame[fishingruntime.UserExport](adapter.registered, game.FishingID, ctx, tx, request)
	} else {
		value, err = adapter.owner.ExportTx(ctx, tx, request.UserID, request.DecisionNow, request.Limit)
	}
	if err != nil {
		if errors.Is(err, fishingruntime.ErrLifecycleResourceLimit) {
			return lifecycle.FishingExport{}, finalizer, lifecycle.ErrTooLarge
		}
		return lifecycle.FishingExport{}, finalizer, err
	}
	single, err := mapFishingRank(value.Single)
	if err != nil {
		return lifecycle.FishingExport{}, finalizer, err
	}
	total, err := mapFishingRank(value.Total)
	if err != nil {
		return lifecycle.FishingExport{}, finalizer, err
	}
	recent, err := mapFishingRank(value.RollingBest)
	if err != nil {
		return lifecycle.FishingExport{}, finalizer, err
	}
	out := lifecycle.FishingExport{
		Pending:    make([]lifecycle.FishingPendingExport, len(value.Pending)),
		Terminal:   make([]lifecycle.FishingBatchExport, len(value.Terminal)),
		SingleBest: single, RollingTotal: total, RollingBest: recent,
	}
	for index, pending := range value.Pending {
		out.Pending[index] = lifecycle.FishingPendingExport{
			RulesVersion: pending.RulesVersion, Payment: lifecycle.GamePaymentExport(pending.Payment),
			BatchID: pending.BatchID, Bait: pending.Bait, Count: pending.Count,
			EntryTotal: pending.EntryTotal, State: pending.State,
			NextAttemptAt: cloneInt64(pending.NextAttemptAt), RetryExhausted: pending.RetryExhausted,
		}
	}
	for index, batch := range value.Terminal {
		outcomes := make([]lifecycle.FishingOutcomeExport, len(batch.Outcomes))
		for outcomeIndex, outcome := range batch.Outcomes {
			if !fishingruntime.ValidExportOutcome(outcome) {
				return lifecycle.FishingExport{}, finalizer, lifecycle.ErrInvariant
			}
			outcomes[outcomeIndex] = lifecycle.FishingOutcomeExport{
				Ordinal: outcome.Ordinal, SpeciesKey: outcome.SpeciesKey, Tier: outcome.Tier,
				NetReward: outcome.NetReward, Rake: lifecycle.FishingRakeExport(outcome.Rake), SizeCM: outcome.SizeCM, Reward: outcome.Reward, BlueFatFishLengthCM: cloneString(outcome.BlueFatFishLengthCM),
			}
		}
		out.Terminal[index] = lifecycle.FishingBatchExport{
			RulesVersion: batch.RulesVersion, Payment: lifecycle.GamePaymentExport(batch.Payment),
			BatchID: batch.BatchID, Bait: batch.Bait, Count: batch.Count,
			UnitPrice: batch.UnitPrice, EntryTotal: batch.EntryTotal, Outcomes: outcomes,
			NetPayoutTotal: batch.NetPayoutTotal, Rake: lifecycle.FishingRakeExport(batch.Rake), PayoutTotal: batch.PayoutTotal, SettledAt: batch.SettledAt,
			RevealedAt: cloneInt64(batch.RevealedAt),
		}
	}
	return out, finalizer, nil
}

func mapFishingRank(value *fishingruntime.FishingLeaderboardRow) (*lifecycle.FishingRankExport, error) {
	projected, err := fishingruntime.ProjectExportRank(value)
	if err != nil {
		return nil, lifecycle.ErrInvariant
	}
	if projected == nil {
		return nil, nil
	}
	out := lifecycle.FishingRankExport(*projected)
	return &out, nil
}

func (adapter *FishingAdapter) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter != nil && adapter.registered != nil {
		return deleteRegisteredGame(adapter.registered, game.FishingID, ctx, tx, request)
	}
	if adapter == nil || adapter.owner == nil {
		return nil, lifecycle.ErrUnavailable
	}
	if err := adapter.owner.PrepareDeleteTx(ctx, tx, request.UserID, request.DecisionNow); err != nil {
		return nil, err
	}
	return nil, nil
}

func (adapter *FishingAdapter) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter != nil && adapter.registered != nil {
		return retainRegisteredGame(adapter.registered, game.FishingID, ctx, decisionNow, limit, budgetDeadline)
	}
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	result, err := adapter.owner.Retain(ctx, decisionNow, limit, budgetDeadline)
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, err
}

func (adapter *LinkLinkAdapter) ExportLinkLink(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.ExportRequest,
) (lifecycle.LinkLinkExport, lifecycle.ExportFinalizer, error) {
	if adapter == nil || adapter.owner == nil && adapter.registered == nil {
		return lifecycle.LinkLinkExport{}, nil, lifecycle.ErrUnavailable
	}
	var value linklink.UserExport
	var finalizer host.Finalizer
	var err error
	if adapter.registered != nil {
		value, finalizer, err = exportRegisteredGame[linklink.UserExport](adapter.registered, game.LinkLinkID, ctx, tx, request)
	} else {
		var owned *linklink.ExportFinalizer
		value, owned, err = adapter.owner.ExportTx(ctx, tx, request.UserID, request.DecisionNow, request.Limit)
		if owned != nil {
			finalizer = owned
		}
	}
	if err != nil {
		if errors.Is(err, linklink.ErrLifecycleResourceLimit) {
			return lifecycle.LinkLinkExport{}, finalizer, lifecycle.ErrTooLarge
		}
		return lifecycle.LinkLinkExport{}, finalizer, err
	}
	out := lifecycle.LinkLinkExport{Summaries: make([]lifecycle.LinkLinkSummaryExport, len(value.Summaries))}
	if value.Active != nil {
		active := value.Active
		out.Active = &lifecycle.LinkLinkActiveExport{
			RulesVersion: active.RulesVersion, Payment: lifecycle.GamePaymentExport(active.Payment),
			SessionID: active.SessionID, Spec: active.Spec, Price: active.Price, State: active.State,
			PairsRemoved: active.PairsRemoved, TotalPairs: active.TotalPairs,
			StartedAt: active.StartedAt, Deadline: active.Deadline,
		}
	}
	for index, summary := range value.Summaries {
		out.Summaries[index] = lifecycle.LinkLinkSummaryExport{
			RulesVersion: summary.RulesVersion, Payment: lifecycle.GamePaymentExport(summary.Payment),
			SessionID: summary.SessionID, Spec: summary.Spec, Price: summary.Price,
			TerminalReason: summary.TerminalReason, StartedAt: summary.StartedAt,
			Deadline: summary.Deadline, TerminalAt: summary.TerminalAt,
			PairsRemoved: summary.PairsRemoved, TotalPairs: summary.TotalPairs,
			Score: cloneString(summary.Score),
		}
	}
	return out, finalizer, nil
}

func (adapter *LinkLinkAdapter) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter != nil && adapter.registered != nil {
		return deleteRegisteredGame(adapter.registered, game.LinkLinkID, ctx, tx, request)
	}
	if adapter == nil || adapter.owner == nil {
		return nil, lifecycle.ErrUnavailable
	}
	return adapter.owner.PrepareDeleteTx(ctx, tx, request.UserID, request.DecisionNow)
}

func (adapter *LinkLinkAdapter) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter != nil && adapter.registered != nil {
		return retainRegisteredGame(adapter.registered, game.LinkLinkID, ctx, decisionNow, limit, budgetDeadline)
	}
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	result, err := adapter.owner.Retain(ctx, decisionNow, limit, budgetDeadline)
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, err
}

func (adapter *RPSAdapter) ExportRPS(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.ExportRequest,
) (lifecycle.RPSExport, lifecycle.ExportFinalizer, error) {
	if adapter == nil || adapter.owner == nil && adapter.registered == nil {
		return lifecycle.RPSExport{}, nil, lifecycle.ErrUnavailable
	}
	var value rps.UserExport
	var finalizer host.Finalizer
	var err error
	if adapter.registered != nil {
		value, finalizer, err = exportRegisteredGame[rps.UserExport](adapter.registered, game.RPSID, ctx, tx, request)
	} else {
		var owned *rps.ExportFinalizer
		value, owned, err = adapter.owner.ExportTx(ctx, tx, request.UserID, request.DecisionNow, request.Limit)
		if owned != nil {
			finalizer = owned
		}
	}
	if err != nil {
		if errors.Is(err, rps.ErrResourceLimit) {
			return lifecycle.RPSExport{}, finalizer, lifecycle.ErrTooLarge
		}
		return lifecycle.RPSExport{}, finalizer, err
	}
	current, err := mapRPSCurrent(value.Current)
	if err != nil {
		return lifecycle.RPSExport{}, finalizer, err
	}
	out := lifecycle.RPSExport{
		Current: current, Pending: mapRPSPending(value.Pending),
		Summaries: make([]lifecycle.RPSSummaryExport, len(value.Summaries)),
		FunStats:  mapRPSFunStats(value.FunStats), TutorialSeen: value.TutorialSeen,
	}
	for index, summary := range value.Summaries {
		out.Summaries[index] = lifecycle.RPSSummaryExport{
			SessionID: summary.SessionID, Mode: summary.Mode, TerminalReason: summary.TerminalReason, RulesVersion: summary.RulesVersion,
			StartedAt: summary.StartedAt, TerminalAt: summary.TerminalAt,
			OwnSeat: lifecycle.RPSSeatExport{
				SeatNo: summary.OwnSeat.SeatNo, Input: summary.OwnSeat.Input,
				Returned: summary.OwnSeat.Returned, WalletNet: summary.OwnSeat.WalletNet,
				TimeoutCount: summary.OwnSeat.TimeoutCount, RockCount: summary.OwnSeat.RockCount,
				ScissorsCount: summary.OwnSeat.ScissorsCount, PaperCount: summary.OwnSeat.PaperCount,
				OwnBuyIn: cloneString(summary.OwnSeat.OwnBuyIn), OwnCashOut: cloneString(summary.OwnSeat.OwnCashOut),
				OwnBuyInGeneral: cloneString(summary.OwnSeat.OwnBuyInGeneral), OwnBuyInGame: cloneString(summary.OwnSeat.OwnBuyInGame),
				OwnReturnedGeneral: cloneString(summary.OwnSeat.OwnReturnedGeneral),
			},
		}
	}
	return out, finalizer, nil
}

func mapRPSCurrent(value *rps.HomeState) (*lifecycle.RPSCurrentExport, error) {
	projected, err := rps.ProjectExportCurrent(value)
	if err != nil {
		return nil, lifecycle.ErrInvariant
	}
	if projected == nil {
		return nil, nil
	}
	out := lifecycle.RPSCurrentExport{Kind: projected.Kind, ResourceID: projected.ResourceID, Mode: projected.Mode, State: projected.State,
		Phase: projected.Phase, Deadline: projected.Deadline, RulesVersion: projected.RulesVersion}
	if projected.Payment != nil {
		payment := lifecycle.GamePaymentExport(*projected.Payment)
		out.Payment = &payment
	}
	if projected.Funding != nil {
		funding := lifecycle.RPSFundingExport(*projected.Funding)
		out.Funding = &funding
	}
	return &out, nil
}

func mapRPSPending(value *rps.PendingResult) *lifecycle.RPSPendingExport {
	if value == nil {
		return nil
	}
	seats := make([]lifecycle.RPSPendingSeatExport, len(value.Seats))
	for index, seat := range value.Seats {
		seats[index] = lifecycle.RPSPendingSeatExport{SeatNo: seat.SeatNo, Result: seat.Result}
	}
	return &lifecycle.RPSPendingExport{
		SessionID: value.SessionID, Mode: value.Mode, TerminalReason: value.TerminalReason,
		OwnSeatNo: value.OwnSeatNo, OwnInput: value.OwnInput, OwnReturned: value.OwnReturned,
		OwnWalletNet: value.OwnWalletNet, Seats: seats, CreatedAt: value.CreatedAt,
		OwnBuyIn: cloneString(value.OwnBuyIn), OwnCashOut: cloneString(value.OwnCashOut), RulesVersion: value.RulesVersion,
		OwnBuyInGeneral: cloneString(value.OwnBuyInGeneral), OwnBuyInGame: cloneString(value.OwnBuyInGame),
		OwnReturnedGeneral: cloneString(value.OwnReturnedGeneral),
	}
}

func mapRPSFunStats(value *rps.FunStatsExport) *lifecycle.RPSFunStatsExport {
	if value == nil {
		return nil
	}
	return &lifecycle.RPSFunStatsExport{
		CompletedCount: value.CompletedCount, ProfitableCount: value.ProfitableCount,
		RockCount: value.RockCount, ScissorsCount: value.ScissorsCount, PaperCount: value.PaperCount,
	}
}

func (adapter *RPSAdapter) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter != nil && adapter.registered != nil {
		return deleteRegisteredGame(adapter.registered, game.RPSID, ctx, tx, request)
	}
	if adapter == nil || adapter.owner == nil {
		return nil, lifecycle.ErrUnavailable
	}
	return adapter.owner.PrepareDeleteTx(ctx, tx, request.UserID, request.DecisionNow)
}

func (adapter *RPSAdapter) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter != nil && adapter.registered != nil {
		return retainRegisteredGame(adapter.registered, game.RPSID, ctx, decisionNow, limit, budgetDeadline)
	}
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	result, err := adapter.owner.Retain(ctx, decisionNow, limit, budgetDeadline)
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, err
}

var (
	_ lifecycle.FishingExporter  = (*FishingAdapter)(nil)
	_ lifecycle.DeleteAdapter    = (*FishingAdapter)(nil)
	_ lifecycle.RetentionAdapter = (*FishingAdapter)(nil)
	_ lifecycle.LinkLinkExporter = (*LinkLinkAdapter)(nil)
	_ lifecycle.DeleteAdapter    = (*LinkLinkAdapter)(nil)
	_ lifecycle.RetentionAdapter = (*LinkLinkAdapter)(nil)
	_ lifecycle.RPSExporter      = (*RPSAdapter)(nil)
	_ lifecycle.DeleteAdapter    = (*RPSAdapter)(nil)
	_ lifecycle.RetentionAdapter = (*RPSAdapter)(nil)
)
