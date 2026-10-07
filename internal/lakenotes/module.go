package lakenotes

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func (s *Service) Module() *host.Module {
	available := func(mode, spec string) bool { return mode == "" && spec == "" }
	return &host.Module{
		ValidatePersistedState: func(ctx context.Context) error { return ctx.Err() },
		RecoverBeforeListen: func(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
			r, err := s.RecoverBeforeListener(ctx, now, limit, min(time.Until(deadline), lifecycle.WorkerBudget))
			return host.WorkResult{Processed: r.Processed, More: r.More}, err
		},
		RegisterRoutes: func(r host.Registrars) error { return RegisterRoutes(r.User, r.Continuation, r.Admin, s) },
		StartWorker:    func(ctx context.Context) error { return ctx.Err() },
		Close:          func() error { return nil },
		Available:      available, ReadyTx: func(context.Context, *sql.Tx) bool { return true },
		ConfigurationChangedTx: func(ctx context.Context, tx *sql.Tx, before, after game.ConfigValue) error {
			if before.Enabled() && !after.Enabled() {
				return s.pauseTx(ctx, tx, s.now().UnixNano(), "", nil, false)
			}
			return nil
		},
		UserSnapshotTx: func(ctx context.Context, tx *sql.Tx, user, now int64, value game.ConfigValue) (game.UserSnapshot, error) {
			return game.UserSnapshot{Config: value.UserWire(available)}, nil
		},
		HomeSummaryTx: func(ctx context.Context, tx *sql.Tx, user int64) (game.HomeSummary, error) {
			out := game.HomeSummary{Continue: []game.ContinueItem{}, PendingResults: []game.PendingResult{}}
			var id string
			err := tx.QueryRowContext(ctx, "SELECT id FROM lake_notes_casts WHERE user_id=? AND phase IN ('waiting','playing')", user).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				return out, nil
			}
			if err == nil {
				out.Continue = append(out.Continue, game.ContinueItem{Game: game.LakeNotesID, ResourceID: id, State: "active", RouteID: "game-lake-notes"})
			}
			return out, err
		},
		ActiveCountsTx: func(ctx context.Context, tx *sql.Tx) (game.ActiveCounts, error) {
			out := game.ActiveCounts{Games: []game.GameCount{}, Queues: []game.QueueCount{}}
			var n int64
			err := tx.QueryRowContext(ctx, "SELECT count(*) FROM lake_notes_casts WHERE phase IN ('waiting','playing')").Scan(&n)
			if err == nil && n > 0 {
				out.Games = append(out.Games, game.GameCount{Game: game.LakeNotesID, Count: rev(n)})
			}
			return out, err
		},
		ExportTx: func(ctx context.Context, tx *sql.Tx, user, now int64, limit int) (any, host.Finalizer, error) {
			out, err := s.ExportUserTx(ctx, tx, user, limit)
			return out, nil, err
		},
		PrepareDeleteTx: s.PrepareDeleteTx,
		Retain: func(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
			r, err := s.Retain(ctx, now, limit, min(time.Until(deadline), lifecycle.WorkerBudget))
			return host.WorkResult{Processed: r.Processed, More: r.More}, err
		},
	}
}
