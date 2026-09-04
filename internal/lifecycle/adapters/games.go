package adapters

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	fishingruntime "github.com/waiting-here/NonbiriAPI/internal/game/runtime"
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

type FishingAdapter struct{ owner FishingLifecycleOwner }
type LinkLinkAdapter struct{ owner LinkLinkLifecycleOwner }
type RPSAdapter struct{ owner RPSLifecycleOwner }

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
	if adapter == nil || adapter.owner == nil {
		return lifecycle.FishingExport{}, nil, lifecycle.ErrUnavailable
	}
	value, err := adapter.owner.ExportTx(ctx, tx, request.UserID, request.DecisionNow, request.Limit)
	if err != nil {
		if errors.Is(err, fishingruntime.ErrLifecycleResourceLimit) {
			return lifecycle.FishingExport{}, nil, lifecycle.ErrTooLarge
		}
		return lifecycle.FishingExport{}, nil, err
	}
	single, err := mapFishingRank(value.Single)
	if err != nil {
		return lifecycle.FishingExport{}, nil, err
	}
	total, err := mapFishingRank(value.Total)
	if err != nil {
		return lifecycle.FishingExport{}, nil, err
	}
	out := lifecycle.FishingExport{
		Pending:    make([]lifecycle.FishingPendingExport, len(value.Pending)),
		Terminal:   make([]lifecycle.FishingBatchExport, len(value.Terminal)),
		SingleBest: single, RollingTotal: total,
	}
	for index, pending := range value.Pending {
		out.Pending[index] = lifecycle.FishingPendingExport{
			BatchID: pending.BatchID, Bait: pending.Bait, Count: pending.Count,
			EntryTotal: pending.EntryTotal, State: pending.State,
			NextAttemptAt: cloneInt64(pending.NextAttemptAt), RetryExhausted: pending.RetryExhausted,
		}
	}
	for index, batch := range value.Terminal {
		outcomes := make([]lifecycle.FishingOutcomeExport, len(batch.Outcomes))
		for outcomeIndex, outcome := range batch.Outcomes {
			outcomes[outcomeIndex] = lifecycle.FishingOutcomeExport{
				Ordinal: outcome.Ordinal, SpeciesKey: outcome.SpeciesKey, Tier: outcome.Tier,
				SizeCM: outcome.SizeCM, Reward: outcome.Reward,
			}
		}
		out.Terminal[index] = lifecycle.FishingBatchExport{
			BatchID: batch.BatchID, Bait: batch.Bait, Count: batch.Count,
			UnitPrice: batch.UnitPrice, EntryTotal: batch.EntryTotal, Outcomes: outcomes,
			PayoutTotal: batch.PayoutTotal, SettledAt: batch.SettledAt,
			RevealedAt: cloneInt64(batch.RevealedAt),
		}
	}
	return out, nil, nil
}

func mapFishingRank(value *fishingruntime.FishingLeaderboardRow) (*lifecycle.FishingRankExport, error) {
	if value == nil {
		return nil, nil
	}
	if value.Rank == "" || (value.SpeciesKey == "") == (value.TotalCredits == "") {
		return nil, lifecycle.ErrInvariant
	}
	out := &lifecycle.FishingRankExport{Rank: value.Rank}
	if value.SpeciesKey != "" {
		species, size := value.SpeciesKey, value.SizeCM
		out.SpeciesKey, out.SizeCM = &species, &size
	}
	if value.TotalCredits != "" {
		total := value.TotalCredits
		out.TotalCredits = &total
	}
	return out, nil
}

func (adapter *FishingAdapter) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
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
	if adapter == nil || adapter.owner == nil {
		return lifecycle.LinkLinkExport{}, nil, lifecycle.ErrUnavailable
	}
	value, finalizer, err := adapter.owner.ExportTx(ctx, tx, request.UserID, request.DecisionNow, request.Limit)
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
			SessionID: active.SessionID, Spec: active.Spec, Price: active.Price, State: active.State,
			PairsRemoved: active.PairsRemoved, TotalPairs: active.TotalPairs,
			StartedAt: active.StartedAt, Deadline: active.Deadline,
		}
	}
	for index, summary := range value.Summaries {
		out.Summaries[index] = lifecycle.LinkLinkSummaryExport{
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
	if adapter == nil || adapter.owner == nil {
		return lifecycle.RPSExport{}, nil, lifecycle.ErrUnavailable
	}
	value, finalizer, err := adapter.owner.ExportTx(ctx, tx, request.UserID, request.DecisionNow, request.Limit)
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
			SessionID: summary.SessionID, Mode: summary.Mode, TerminalReason: summary.TerminalReason,
			StartedAt: summary.StartedAt, TerminalAt: summary.TerminalAt,
			OwnSeat: lifecycle.RPSSeatExport{
				SeatNo: summary.OwnSeat.SeatNo, Input: summary.OwnSeat.Input,
				Returned: summary.OwnSeat.Returned, WalletNet: summary.OwnSeat.WalletNet,
				TimeoutCount: summary.OwnSeat.TimeoutCount, RockCount: summary.OwnSeat.RockCount,
				ScissorsCount: summary.OwnSeat.ScissorsCount, PaperCount: summary.OwnSeat.PaperCount,
			},
		}
	}
	return out, finalizer, nil
}

func mapRPSCurrent(value *rps.HomeState) (*lifecycle.RPSCurrentExport, error) {
	if value == nil {
		return nil, nil
	}
	switch value.Kind {
	case "queue":
		if value.Queue == nil || value.Session != nil || value.Result != nil {
			return nil, lifecycle.ErrInvariant
		}
		deadline := value.Queue.Deadline
		return &lifecycle.RPSCurrentExport{
			Kind: "queue", ResourceID: value.Queue.ID, Mode: value.Queue.Mode,
			State: value.Queue.State, Deadline: &deadline,
		}, nil
	case "session":
		if value.Session == nil || value.Queue != nil || value.Result != nil {
			return nil, lifecycle.ErrInvariant
		}
		phase := value.Session.Phase
		return &lifecycle.RPSCurrentExport{
			Kind: "session", ResourceID: value.Session.SessionID, Mode: value.Session.Mode,
			State: value.Session.State, Phase: &phase, Deadline: cloneInt64(value.Session.Deadline),
		}, nil
	default:
		return nil, lifecycle.ErrInvariant
	}
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
