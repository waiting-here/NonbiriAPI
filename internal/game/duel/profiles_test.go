package duel_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

func TestProfilesFollowCurrentPreferenceAndStayOutOfExport(t *testing.T) {
	f := newFixture(t, "bidding")
	state := f.matched()
	if state.Profiles[1-state.You].Kind != "anonymous" {
		t.Fatal(state.Profiles)
	}
	if _, err := f.db.Exec(`UPDATE users SET game_profile_public=1,guild_nick='Visible participant',guild_avatar_url='javascript:alert(1)' WHERE id=?`, f.users[1]); err != nil {
		t.Fatal(err)
	}
	state = *f.read(0).Current
	if state.Profiles[1-state.You].DisplayName != "Visible participant" || state.Profiles[1-state.You].AvatarURL != nil {
		t.Fatal(state.Profiles)
	}
	if _, err := f.s.Surrender(f.ctx, duel.ActionInput{Identity: f.identity(0), SessionID: state.ID, PhaseSeq: state.PhaseSeq, IdempotencyKey: f.key()}); err != nil {
		t.Fatal(err)
	}
	detail, err := f.s.HistoryDetail(f.ctx, f.identity(0), state.ID)
	if err != nil || detail.Result.Profiles[1-state.You].Kind != "public" {
		t.Fatal(detail, err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, _, err := f.s.ExportTx(f.ctx, tx, f.users[0], 100, 10000)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(exported)
	if err != nil || strings.Contains(string(body), "Visible participant") || strings.Contains(string(body), `"profiles"`) {
		t.Fatal("opponent identity exported", err)
	}
	if _, err := f.db.Exec(`UPDATE users SET game_profile_public=0 WHERE id=?`, f.users[1]); err != nil {
		t.Fatal(err)
	}
	detail, err = f.s.HistoryDetail(f.ctx, f.identity(0), state.ID)
	if err != nil || detail.Result.Profiles[1-state.You].Kind != "anonymous" {
		t.Fatal(detail, err)
	}
	f.ledger()
}
