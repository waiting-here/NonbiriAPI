package duel_test

import (
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

func TestMaintenanceContinuationRequiresCurrentParticipantSession(t *testing.T) {
	f := newFixture(t, "likes")
	state := f.matched()
	if _, err := f.db.Exec(`UPDATE maintenance_state SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	f.action(0, state, basicPlan)
	f.action(1, state, basicPlan)
	if _, err := f.s.Rounds(f.ctx, f.identity(0), state.ID, duel.PageInput{}, true); err != nil {
		t.Fatal(err)
	}
	wrong := f.identity(0)
	wrong.SessionBinding = f.identity(1).SessionBinding
	if _, err := f.s.Read(f.ctx, wrong); !errors.Is(err, duel.ErrMaintenance) {
		t.Fatal("cross-session read", err)
	}
	if _, err := f.s.Rounds(f.ctx, wrong, state.ID, duel.PageInput{}, true); !errors.Is(err, duel.ErrMaintenance) {
		t.Fatal("cross-session rounds", err)
	}
	state = *f.read(0).Current
	if _, err := f.s.Surrender(f.ctx, duel.ActionInput{Identity: f.identity(1), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.HistoryDetail(f.ctx, f.identity(0), state.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.HistoryDetail(f.ctx, wrong, state.ID); !errors.Is(err, duel.ErrMaintenance) {
		t.Fatal("cross-session history", err)
	}
	if _, err := f.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, f.identity(0).SessionBinding); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.History(f.ctx, f.identity(0), duel.PageInput{}); !errors.Is(err, duel.ErrMaintenance) {
		t.Fatal("revoked session history", err)
	}
	f.ledger()
}

func TestMaintenanceAndDisabledModeRefundQueuedEntry(t *testing.T) {
	for _, maintenance := range []bool{false, true} {
		f := newFixture(t, "bidding")
		f.enqueue(0, 0)
		query := `UPDATE site_config SET value='0' WHERE key='game_bidding_tier1_enabled'`
		if maintenance {
			query = `UPDATE maintenance_state SET enabled=1 WHERE id=1`
		}
		if _, err := f.db.Exec(query); err != nil {
			t.Fatal(err)
		}
		f.tick()
		if got := f.read(0); got.Queue != nil || got.Current != nil {
			t.Fatal(got)
		}
		f.ledger()
	}
}
