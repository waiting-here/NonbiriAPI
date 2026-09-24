package main

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/imageactivity"
	"github.com/waiting-here/NonbiriAPI/internal/inactivity"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

func (a *activityRuntime) ExportGovernance(ctx context.Context, tx *sql.Tx, r lifecycle.ExportRequest) (lifecycle.GovernanceExport, error) {
	var out lifecycle.GovernanceExport
	limited, err := a.limited.ExportUserTx(ctx, tx, r.UserID, r.Limit)
	if err != nil {
		return out, activityExportError(err)
	}
	out.LimitedActivities.Wallet = lifecycle.ActivityWalletExport{General: limited.Wallet.General, Paper: limited.Wallet.Paper, Brush: limited.Wallet.Brush}
	out.LimitedActivities.Exchanges = make([]lifecycle.ActivityExchangeExport, 0, len(limited.Exchanges))
	for _, e := range limited.Exchanges {
		out.LimitedActivities.Exchanges = append(out.LimitedActivities.Exchanges, lifecycle.ActivityExchangeExport{
			OperationID: e.OperationID, ActivityKey: e.ActivityKey, ConfigRevision: e.ConfigRevision,
			Asset: string(e.Asset), Quantity: e.Quantity, UnitPrice: e.UnitPrice, Cost: e.Cost, LedgerSeq: e.LedgerSeq, CreatedAt: e.CreatedAt,
		})
	}
	images, err := a.images.ExportUserTx(ctx, tx, r.UserID, a.now().Unix(), r.Limit)
	if err != nil {
		return out, activityExportError(err)
	}
	out.ImageTasks = make([]lifecycle.ImageTaskExport, 0, len(images.Tasks))
	for _, e := range images.Tasks {
		out.ImageTasks = append(out.ImageTasks, lifecycle.ImageTaskExport{
			ID: e.ID, Status: e.Status, N: e.N, CreatedAt: e.CreatedAt, DispatchedAt: e.DispatchedAt, CompletedAt: e.CompletedAt,
			BillingState: e.BillingState, Charge: lifecycle.ImagePriceExport{Paper: e.Charge.Paper, Brush: e.Charge.Brush},
			Refund: lifecycle.ImagePriceExport{Paper: e.Refund.Paper, Brush: e.Refund.Brush}, ActualImages: e.ActualImages,
		})
	}
	policy, err := inactivity.ExportTx(ctx, tx, r.UserID, r.Limit)
	if err != nil {
		return out, activityExportError(err)
	}
	if e := policy.Activity; e != nil {
		out.Inactivity.Activity = &lifecycle.ActiveStateExport{
			ObservationStartedAt: e.ObservationStartedAt, LastActiveAt: e.LastActiveAt, ActivitySeq: e.ActivitySeq,
			ActivityEpoch: e.ActivityEpoch, LastDecayAt: e.LastDecayAt,
		}
	}
	out.Inactivity.Runs = make([]lifecycle.InactivityRunExport, 0, len(policy.Runs))
	for _, e := range policy.Runs {
		out.Inactivity.Runs = append(out.Inactivity.Runs, lifecycle.InactivityRunExport{
			ID: e.ID, UserID: e.UserID, PolicyRevision: e.PolicyRevision, ActivityEpoch: e.ActivityEpoch, DueSlot: e.DueSlot,
			Action: e.Action, GeneralMilli: e.GeneralMilli, GameMilli: e.GameMilli, OperationID: e.OperationID, CreatedAt: e.CreatedAt,
		})
	}
	return out, nil
}

func activityExportError(err error) error {
	if errors.Is(err, limitedactivities.ErrExportLimit) || errors.Is(err, imageactivity.ErrExportLimit) || errors.Is(err, inactivity.ErrTooLarge) {
		return lifecycle.ErrTooLarge
	}
	return err
}

func (a *activityRuntime) PrepareDelete(ctx context.Context, tx *sql.Tx, r lifecycle.DeleteRequest) (lifecycle.DeleteFinalizer, error) {
	f, err := a.limited.PrepareDeleteTx(ctx, tx, r.UserID, r.DecisionNow)
	if err != nil {
		return nil, err
	}
	if err = inactivity.DeleteTx(ctx, tx, r.UserID); err != nil {
		f.Abort()
		return nil, err
	}
	return f, nil
}

func (a *activityRuntime) RecoverBeforeListener(ctx context.Context, _ int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	if a.recovered.Load() {
		return lifecycle.WorkResult{}, nil
	}
	budget := min(time.Until(deadline), lifecycle.WorkerBudget)
	if budget <= 0 {
		return lifecycle.WorkResult{}, context.DeadlineExceeded
	}
	r, err := a.limited.RecoverBeforeListener(ctx, a.now().Unix(), limit, budget)
	if err == nil && !r.More {
		a.recovered.Store(true)
	}
	return r, err
}

func (a *activityRuntime) Retain(ctx context.Context, _ int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	now := a.now().Unix()
	p, err := a.inactivity.Retain(ctx, now, limit, deadline, nil)
	r := lifecycle.WorkResult{Processed: p.Processed, More: p.More}
	if err != nil {
		return r, err
	}
	budget := min(time.Until(deadline), lifecycle.WorkerBudget)
	if r.Processed == limit || budget <= 0 {
		if r.Processed == 0 {
			return r, context.DeadlineExceeded
		}
		r.More = true
		return r, nil
	}
	i, err := a.limited.Retain(ctx, now, limit-r.Processed, budget)
	r.Processed += i.Processed
	r.More = r.More || i.More
	return r, err
}
