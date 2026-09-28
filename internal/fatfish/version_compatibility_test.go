package fatfish

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/fatfish/engine"
)

func versionedFishFixture(t *testing.T, version int) *fishFixture {
	t.Helper()
	f := newFishFixture(t)
	level, err := engine.ParseLevel(f.level)
	if err != nil {
		t.Fatal(err)
	}
	level.EngineVersion = version
	f.level, err = engine.NormalizedLevel(level)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Replay(level, [32]byte{}, nil, engine.ReplayOptions{})
	if err != nil || !result.Passed {
		t.Fatal("fixture not passable", result, err)
	}
	f.terminalTick = result.TerminalTick
	return f
}

func TestBoundEngineVersionsSurviveRecoveryAndSettleOnce(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			f := versionedFishFixture(t, version)
			period, node := f.publishOne(t)
			ctx := context.Background()
			p, err := f.s.AdminPeriod(ctx, f.admin, period)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(6000)); err != nil {
				t.Fatal(err)
			}
			cap, hash := fishCap(2)
			prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(6001))
			if err != nil {
				t.Fatal(err)
			}
			started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(6002))
			if err != nil {
				t.Fatal(err)
			}
			seedBytes, err := hex.DecodeString(started.Seed)
			if err != nil {
				t.Fatal(err)
			}
			var seed [32]byte
			copy(seed[:], seedBytes)
			commit, err := engine.SeedCommitForVersion(started.ID, period, node, started.ContentHash, version, 1, seed)
			if err != nil || commit != started.SeedCommit || started.EngineVersion != version {
				t.Fatal("wrong bound commitment", started, err)
			}
			f.s.Close()
			f.s, err = New(f.config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(f.s.Close)
			restored, err := f.s.Challenge(ctx, f.user, started.ID, cap)
			if err != nil || !reflect.DeepEqual(started.Level, restored.Level) || restored.SeedCommit != started.SeedCommit || restored.EngineVersion != version {
				t.Fatal("recovery changed bound rules", restored, err)
			}
			f.clock.Store(*started.StartAtMS + (int64(f.terminalTick)*1000+59)/60 + 1)
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := f.s.Submit(ctx, f.user, started.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(6003)); err != nil {
					t.Fatal(err)
				}
				terminal := f.waitState(t, started.ID, "settled_pass")
				if terminal.Result == nil || terminal.Result.EngineVersion != version || terminal.Result.ScoringVersion != 1 || !terminal.Result.CommitmentVerified || terminal.Result.SeedCommit != commit || terminal.Result.Rewards != "9" {
					t.Fatal("wrong result rules or accounting", terminal)
				}
				if balance := f.balance(t); balance != "15500" {
					t.Fatal("recovery/retry paid more than once", balance)
				}
			}
		})
	}
}

func TestConvertingDraftRequiresNewPlaytestAndKeepsActiveVersion(t *testing.T) {
	f := versionedFishFixture(t, 1)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	oldNode := p.Nodes[0]
	oldVersion, err := f.s.Version(ctx, f.admin, oldNode.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	level, err := f.s.Level(ctx, f.admin, oldVersion.LevelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: oldNode.Revision}, fishKey(6100)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(3)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: oldNode.Revision, TabCapabilityHash: hash}, fishKey(6101))
	if err != nil {
		t.Fatal(err)
	}
	active, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(6102))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := engine.ParseLevel(level.Draft)
	if err != nil {
		t.Fatal(err)
	}
	draft.EngineVersion = 2
	raw, err := engine.NormalizedLevel(draft)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := f.s.ValidateLevel(ctx, f.admin, raw)
	if err != nil || validated.EngineVersion != 2 {
		t.Fatal("validation misreported rules", validated, err)
	}
	saved, err := f.s.SaveLevel(ctx, f.admin, level.ID, LevelInput{Title: level.Title, Description: level.Description, Draft: raw, ExpectedRevision: level.Revision}, fishKey(6103))
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.s.PublishVersion(ctx, f.admin, level.ID, saved.Revision, fishKey(6104))
	if err != nil || current.EngineVersion != 2 || current.ID == oldVersion.ID || current.ContentHash == oldVersion.ContentHash {
		t.Fatal("conversion reused old immutable version", current, err)
	}
	patch := NodeInput{Title: oldNode.Title, Description: oldNode.Description, VersionID: current.ID, Condition: json.RawMessage(`{}`), Amounts: *oldNode.Amounts, ExpectedRevision: oldNode.Revision, ExpectedPeriodRevision: p.Revision}
	if _, err := f.s.SaveNode(ctx, f.admin, period, node, patch, fishKey(6105)); !errors.Is(err, ErrConflict) {
		t.Fatalf("new rules accepted without their own proof: %v", err)
	}
	playCap, playHash := fishCap(4)
	play, err := f.s.PreparePlaytest(ctx, f.admin, PlaytestInput{VersionID: current.ID, TabCapabilityHash: playHash}, fishKey(6106))
	if err != nil || play.EngineVersion != 2 {
		t.Fatal(play, err)
	}
	if _, err := f.s.StartPlaytest(ctx, f.admin, play.ID, StartInput{TabCapability: playCap}, fishKey(6107)); err != nil {
		t.Fatal(err)
	}
	f.tick(3200)
	if _, err := f.s.SubmitPlaytest(ctx, f.admin, play.ID, SubmitInput{TabCapability: playCap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(6108)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	passed := false
	for time.Now().Before(deadline) {
		view, err := f.s.PlaytestChallenge(ctx, f.admin, play.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if view.State == "settled_pass" {
			passed = view.Result != nil && view.Result.EngineVersion == 2
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !passed {
		t.Fatal("new playtest did not verify the new version")
	}
	if _, err := f.s.SaveNode(ctx, f.admin, period, node, patch, fishKey(6109)); err != nil {
		t.Fatal(err)
	}
	retained, err := f.s.Version(ctx, f.admin, oldVersion.ID)
	if err != nil || !reflect.DeepEqual(oldVersion, retained) {
		t.Fatal("old published version changed", err)
	}
	recovered, err := f.s.Challenge(ctx, f.user, active.ID, cap)
	if err != nil || recovered.EngineVersion != 1 || recovered.VersionID != oldVersion.ID || recovered.SeedCommit != active.SeedCommit {
		t.Fatal("active old game silently upgraded", recovered, err)
	}
	if _, err := f.s.Submit(ctx, f.user, active.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(6110)); err != nil {
		t.Fatal(err)
	}
	terminal := f.waitState(t, active.ID, "settled_pass")
	if terminal.Result == nil || terminal.Result.EngineVersion != 1 {
		t.Fatal("old result used the current engine", terminal)
	}
}
