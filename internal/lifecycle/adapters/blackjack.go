package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type BlackjackAdapter struct{ service *host.Service }

func NewRegisteredBlackjack(service *host.Service) *BlackjackAdapter {
	if service == nil {
		return nil
	}
	return &BlackjackAdapter{service: service}
}
func (a *BlackjackAdapter) ExportBlackjack(ctx context.Context, tx *sql.Tx, r lifecycle.ExportRequest) (lifecycle.BlackjackExport, lifecycle.ExportFinalizer, error) {
	v, end, err := exportRegisteredGame[blackjack.PersonalExport](a.service, game.BlackjackID, ctx, tx, r)
	if err != nil {
		return lifecycle.BlackjackExport{}, end, err
	}
	out := lifecycle.BlackjackExport{History: make([]lifecycle.BlackjackHistoryExport, len(v.History))}
	mapTable := func(v blackjack.TableView) (lifecycle.BlackjackTableExport, error) {
		fact, err := json.Marshal(v.Fact)
		return lifecycle.BlackjackTableExport{ID: v.ID, Revision: v.Revision, StartedAt: v.StartedAt, Phase: v.Phase, Deadline: v.Deadline, NextRoundAt: v.NextRoundAt, TerminalAt: v.TerminalAt, Reason: v.Reason, Fact: fact}, err
	}
	if h := v.Current; h != nil {
		out.Current = &lifecycle.BlackjackCurrentExport{ServerNow: h.ServerNow, Phase: h.Phase, Deadline: h.Deadline, NextRoundAt: h.NextRoundAt, YourSeat: h.YourSeat}
		if e := h.You; e != nil {
			out.Current.You = &lifecycle.BlackjackEntryExport{ID: e.ID, Position: e.Position, State: e.State, Stake: e.Stake, Rake: lifecycle.DuelRatesExport(e.Rake), Payment: lifecycle.GamePaymentExport(e.Payment), Seat: e.Seat, SessionID: e.SessionID, Pending: e.Pending}
		}
		if h.Table != nil {
			table, err := mapTable(*h.Table)
			if err != nil {
				return out, end, err
			}
			out.Current.Table = &table
		}
	}
	for i, h := range v.History {
		table, err := mapTable(h.Table)
		if err != nil {
			return out, end, err
		}
		m := h.Summary
		out.History[i] = lifecycle.BlackjackHistoryExport{Summary: lifecycle.BlackjackSummaryExport{ID: m.ID, StartedAt: m.StartedAt, TerminalAt: m.TerminalAt, Phase: m.Phase, Reason: m.Reason, Seat: m.Seat, Stake: m.Stake, TotalStake: m.TotalStake, Net: m.Net, Payment: lifecycle.GamePaymentExport(m.Payment)}, Table: table}
	}
	return out, end, nil
}
func (a *BlackjackAdapter) PrepareDelete(ctx context.Context, tx *sql.Tx, r lifecycle.DeleteRequest) (lifecycle.DeleteFinalizer, error) {
	return deleteRegisteredGame(a.service, game.BlackjackID, ctx, tx, r)
}
func (a *BlackjackAdapter) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	return retainRegisteredGame(a.service, game.BlackjackID, ctx, now, limit, deadline)
}
