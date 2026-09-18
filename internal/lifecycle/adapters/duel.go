package adapters

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type DuelAdapter struct {
	service *host.Service
	id      string
}

func NewRegisteredDuel(service *host.Service, id string) *DuelAdapter {
	if service == nil || (id != game.BiddingID && id != game.LikesID) {
		return nil
	}
	return &DuelAdapter{service: service, id: id}
}
func (a *DuelAdapter) ExportDuel(ctx context.Context, tx *sql.Tx, request lifecycle.ExportRequest) (lifecycle.DuelExport, lifecycle.ExportFinalizer, error) {
	if a == nil {
		return lifecycle.DuelExport{}, nil, lifecycle.ErrUnavailable
	}
	v, end, err := exportRegisteredGame[duel.Export](a.service, a.id, ctx, tx, request)
	if err != nil {
		return lifecycle.DuelExport{}, end, err
	}
	return mapDuelExport(v), end, nil
}
func (a *DuelAdapter) PrepareDelete(ctx context.Context, tx *sql.Tx, request lifecycle.DeleteRequest) (lifecycle.DeleteFinalizer, error) {
	if a == nil {
		return nil, lifecycle.ErrUnavailable
	}
	return deleteRegisteredGame(a.service, a.id, ctx, tx, request)
}
func (a *DuelAdapter) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	if a == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	return retainRegisteredGame(a.service, a.id, ctx, now, limit, deadline)
}
func mapDuelResolution(v *duel.Resolution) *lifecycle.DuelResolutionExport {
	if v == nil {
		return nil
	}
	return &lifecycle.DuelResolutionExport{Round: v.Round, StartedAt: v.StartedAt, EndsAt: v.EndsAt, Summary: v.Summary}
}
func mapDuelRounds(values []duel.RoundView) []lifecycle.DuelRoundExport {
	out := make([]lifecycle.DuelRoundExport, len(values))
	for i, v := range values {
		out[i] = lifecycle.DuelRoundExport{Round: v.Round, Before: v.Before, After: v.After, Facts: v.Facts, StartEvents: v.StartEvents, Timeouts: v.Timeouts}
	}
	return out
}
func mapDuelExport(v duel.Export) lifecycle.DuelExport {
	out := lifecycle.DuelExport{CurrentRounds: mapDuelRounds(v.CurrentRounds), History: make([]lifecycle.DuelMatchExport, len(v.History))}
	if q := v.Queue; q != nil {
		out.Queue = &lifecycle.DuelQueueExport{ID: q.ID, Revision: q.Revision, Mode: q.Mode, Deadline: q.Deadline, Ticket: q.Ticket, Payment: lifecycle.GamePaymentExport(q.Payment), TermsHash: q.TermsHash, RulesVersion: q.RulesVersion, Loadout: q.Loadout}
	}
	if s := v.Current; s != nil {
		out.Current = &lifecycle.DuelStateExport{ID: s.ID, Game: s.Game, Mode: s.Mode, RulesVersion: s.RulesVersion, ContentHash: s.ContentHash, Revision: s.Revision, PhaseSeq: s.PhaseSeq, Phase: s.Phase, Round: s.Round, Deadline: s.Deadline, ServerNow: s.ServerNow, You: s.You, Locked: s.Locked, Ticket: s.Ticket, Rake: lifecycle.DuelRatesExport(s.Rake), OwnPayment: lifecycle.GamePaymentExport(s.OwnPayment), View: s.View, Resolution: mapDuelResolution(s.Resolution)}
		if r := s.RoundStart; r != nil {
			out.Current.RoundStart = &lifecycle.DuelRoundStartExport{Round: r.Round, StartedAt: r.StartedAt, Events: r.Events}
		}
	}
	for i, m := range v.History {
		d := m.Detail
		out.History[i] = lifecycle.DuelMatchExport{Detail: lifecycle.DuelDetailExport{RulesVersion: d.RulesVersion, ContentHash: d.ContentHash, Ticket: d.Ticket, Rake: lifecycle.DuelRatesExport(d.Rake), Initial: d.Initial, TerminalActions: d.TerminalActions, RoundStartEvents: d.RoundStartEvents}, Rounds: mapDuelRounds(m.Rounds)}
		if r := d.Result; r != nil {
			out.History[i].Detail.Result = &lifecycle.DuelResultExport{ID: r.ID, Game: r.Game, Mode: r.Mode, TerminalAt: r.TerminalAt, Outcome: r.Outcome, Reason: r.Reason, Scores: r.Scores, OwnPayment: lifecycle.GamePaymentExport(r.OwnPayment), OwnRefund: lifecycle.GamePaymentExport(r.OwnRefund), PrizeGeneral: r.PrizeGeneral, Rake: lifecycle.DuelAmountsExport(r.Rake), Resolution: mapDuelResolution(r.Resolution), You: r.You, View: r.View}
		}
	}
	return out
}

var (
	_ lifecycle.DuelExporter     = (*DuelAdapter)(nil)
	_ lifecycle.DeleteAdapter    = (*DuelAdapter)(nil)
	_ lifecycle.RetentionAdapter = (*DuelAdapter)(nil)
)
