package fatfish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/fatfish/engine"
)

func prepareWorkspacePlaytest(t *testing.T, f *fishFixture) (ChallengeView, string) {
	t.Helper()
	ctx := context.Background()
	level, err := f.s.SaveLevel(ctx, f.admin, "", LevelInput{Title: "Workspace level", Draft: f.level}, fishKey(8010))
	if err != nil {
		t.Fatal(err)
	}
	version, err := f.s.PublishVersion(ctx, f.admin, level.ID, level.Revision, fishKey(8011))
	if err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(81)
	view, err := f.s.PreparePlaytest(ctx, f.admin, PlaytestInput{VersionID: version.ID, TabCapabilityHash: hash}, fishKey(8012))
	if err != nil {
		t.Fatal(err)
	}
	return view, cap
}
func TestCurrentPlaytestMetadataIsSafeAndOwned(t *testing.T) {
	f := newFishFixture(t)
	prepared, cap := prepareWorkspacePlaytest(t, f)
	ctx := context.Background()
	meta, err := f.s.CurrentPlaytest(ctx, f.admin)
	if err != nil || meta == nil || meta.ID != prepared.ID || meta.Revision != "1" || meta.LevelTitle != "Workspace level" {
		t.Fatal(meta, err)
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), cap) || strings.Contains(string(encoded), "seed") || strings.Contains(string(encoded), "capability") {
		t.Fatal("metadata exposed recovery secrets")
	}
	other, err := f.s.CurrentPlaytest(ctx, f.user)
	if !errors.Is(err, ErrForbidden) || other != nil {
		t.Fatal("ordinary user saw administrator playtest", other, err)
	}
	if _, err = f.s.AbandonPlaytest(ctx, f.user, prepared.ID, PlaytestAbandonInput{ExpectedRevision: "1"}, fishKey(8013)); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = f.s.Abandon(ctx, f.admin, prepared.ID, AbandonInput{TabCapability: cap}, fishKey(8014)); err == nil {
		t.Fatal("paid path accepted playtest")
	}
}
func TestAbandonPlaytestEndsPreparedAndActiveWithoutCharge(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			f := newFishFixture(t)
			prepared, cap := prepareWorkspacePlaytest(t, f)
			ctx := context.Background()
			current := prepared
			if active {
				var err error
				current, err = f.s.StartPlaytest(ctx, f.admin, prepared.ID, StartInput{TabCapability: cap}, fishKey(8020))
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.s.AbandonPlaytest(ctx, f.admin, current.ID, PlaytestAbandonInput{ExpectedRevision: "99"}, fishKey(8021)); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			terminal, err := f.s.AbandonPlaytest(ctx, f.admin, current.ID, PlaytestAbandonInput{ExpectedRevision: current.Revision}, fishKey(8022))
			if err != nil || terminal.State != "abandoned" || terminal.Result == nil || terminal.Result.Passed || terminal.Result.TicketCharge != "0" || terminal.Result.Rewards != "0" {
				t.Fatal(terminal, err)
			}
			repeated, err := f.s.AbandonPlaytest(ctx, f.admin, current.ID, PlaytestAbandonInput{ExpectedRevision: current.Revision}, fishKey(8022))
			if err != nil || repeated.Revision != terminal.Revision {
				t.Fatal(repeated, err)
			}
			if _, err = f.s.StartPlaytest(ctx, f.admin, current.ID, StartInput{TabCapability: cap}, fishKey(8023)); !errors.Is(err, ErrClosed) {
				t.Fatal("old playtest could restart", err)
			}
			if got := f.balance(t); got != "10000" {
				t.Fatal(got)
			}
			var proofs, financial int
			f.db.QueryRow("SELECT count(*) FROM fatfish_playtests").Scan(&proofs)
			f.db.QueryRow("SELECT count(*) FROM fatfish_financial_receipts").Scan(&financial)
			if proofs != 0 || financial != 0 {
				t.Fatal("abandon created proof/payment", proofs, financial)
			}
			meta, err := f.s.CurrentPlaytest(ctx, f.admin)
			if err != nil || meta != nil {
				t.Fatal(meta, err)
			}
			_, hash := fishCap(82)
			if _, err = f.s.PreparePlaytest(ctx, f.admin, PlaytestInput{VersionID: prepared.VersionID, TabCapabilityHash: hash}, fishKey(8024)); err != nil {
				t.Fatal("ended playtest still blocks prepare", err)
			}
		})
	}
}
func TestPlaytestAbandonWinsAgainstLateVerification(t *testing.T) {
	f := newFishFixture(t)
	prepared, cap := prepareWorkspacePlaytest(t, f)
	ctx := context.Background()
	started, err := f.s.StartPlaytest(ctx, f.admin, prepared.ID, StartInput{TabCapability: cap}, fishKey(8030))
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	f.s.replay = func(_ engine.Level, _ [32]byte, _ []engine.InputTuple, options engine.ReplayOptions) (engine.ReplayResult, error) {
		close(entered)
		<-options.Context.Done()
		return engine.ReplayResult{}, options.Context.Err()
	}
	f.clock.Store(*started.StartAtMS + (int64(f.terminalTick)*1000+59)/60 + 1)
	if _, err = f.s.SubmitPlaytest(ctx, f.admin, started.ID, SubmitInput{TabCapability: cap, Inputs: []byte("[]"), TerminalTick: f.terminalTick}, fishKey(8031)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	meta, err := f.s.CurrentPlaytest(ctx, f.admin)
	if err != nil || meta == nil || meta.State != "verifying" {
		t.Fatal(meta, err)
	}
	terminal, err := f.s.AbandonPlaytest(ctx, f.admin, started.ID, PlaytestAbandonInput{ExpectedRevision: meta.Revision}, fishKey(8032))
	if err != nil || terminal.State != "abandoned" {
		t.Fatal(terminal, err)
	}
	var proofs, active int
	f.db.QueryRow("SELECT count(*) FROM fatfish_playtests").Scan(&proofs)
	f.db.QueryRow("SELECT active_challenges FROM fatfish_capacity WHERE id=1").Scan(&active)
	if proofs != 0 || active != 0 || f.balance(t) != "10000" {
		t.Fatal("late worker wrote proof/money", proofs, active)
	}
}

func TestAbandonPlaytestReadsWinningVerifiedTerminal(t *testing.T) {
	f := newFishFixture(t)
	f.publishOne(t)
	ctx := context.Background()
	var id string
	if err := f.db.QueryRow("SELECT id FROM fatfish_challenges WHERE user_id=? AND playtest=1", f.admin).Scan(&id); err != nil {
		t.Fatal(err)
	}
	before, err := f.s.PlaytestChallenge(ctx, f.admin, id, "")
	if err != nil || before.State != "settled_pass" {
		t.Fatal(before, err)
	}
	after, err := f.s.AbandonPlaytest(ctx, f.admin, id, PlaytestAbandonInput{ExpectedRevision: "1"}, fishKey(8040))
	if err != nil || after.State != before.State || after.Revision != before.Revision || after.Result == nil || !after.Result.Passed {
		t.Fatal(after, err)
	}
	repeated, err := f.s.AbandonPlaytest(ctx, f.admin, id, PlaytestAbandonInput{ExpectedRevision: "1"}, fishKey(8040))
	if err != nil || repeated.State != "settled_pass" {
		t.Fatal(repeated, err)
	}
	var proofs int
	if err = f.db.QueryRow("SELECT count(*) FROM fatfish_playtests").Scan(&proofs); err != nil || proofs != 1 {
		t.Fatal("terminal read created another proof", proofs, err)
	}
}
func TestVersionNumbersAreStableAndMonotonicPerLevel(t *testing.T) {
	f := newFishFixture(t)
	ctx := context.Background()
	level, err := f.s.SaveLevel(ctx, f.admin, "", LevelInput{Title: "Numbered", Draft: f.level}, fishKey(8050))
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.s.PublishVersion(ctx, f.admin, level.ID, level.Revision, fishKey(8051))
	if err != nil || first.VersionNumber != "1" {
		t.Fatal(first, err)
	}
	repeated, err := f.s.PublishVersion(ctx, f.admin, level.ID, level.Revision, fishKey(8052))
	if err != nil || repeated.ID != first.ID || repeated.VersionNumber != "1" {
		t.Fatal(repeated, err)
	}
	var changed map[string]any
	if err = json.Unmarshal(f.level, &changed); err != nil {
		t.Fatal(err)
	}
	changed["duration_seconds"] = 60
	raw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	level, err = f.s.SaveLevel(ctx, f.admin, level.ID, LevelInput{Title: "Numbered", Draft: raw, ExpectedRevision: level.Revision}, fishKey(8053))
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.s.PublishVersion(ctx, f.admin, level.ID, level.Revision, fishKey(8054))
	if err != nil || second.VersionNumber != "2" || second.ID == first.ID {
		t.Fatal(second, err)
	}
	old, err := f.s.Version(ctx, f.admin, first.ID)
	if err != nil || old.ContentHash != first.ContentHash || old.VersionNumber != "1" {
		t.Fatal(old, err)
	}
	page, err := f.s.Versions(ctx, f.admin, level.ID, 1)
	if err != nil || len(page.Items) != 2 || page.Items[0].VersionNumber != "2" || page.Items[1].VersionNumber != "1" || page.Items[0].PlaytestSummary.Count != 0 {
		t.Fatal(page, err)
	}
}
