package db

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestPatchGameConfigMergesAndRejectsInvalidWholeSnapshot(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 0, 0, false)
	ctx := context.Background()
	now := trig.clock.Now()
	before, err := trig.store.GetGameConfigSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := trig.store.PatchGameConfig(ctx, trig.user.ID, map[string]string{
		game.GamesEnabledKey: "0", game.FishingStandardRTPKey: "91",
	}, now); err != nil {
		t.Fatal(err)
	}
	after, err := trig.store.GetGameConfigSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.GamesEnabled || after.Fishing.StandardRTPPercent != 91 || after.Fishing.PremiumRTPPercent != before.Fishing.PremiumRTPPercent {
		t.Fatalf("partial merge = %#v before=%#v", after, before)
	}
	if err := trig.store.PatchGameConfig(ctx, trig.user.ID, map[string]string{game.FishingStandardRTPKey: "101"}, now.Add(time.Second)); !errors.Is(err, ErrInvalidSiteConfig) {
		t.Fatalf("invalid rtp err = %v, want invalid site config", err)
	}
	unchanged, err := trig.store.GetGameConfigSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Fishing.StandardRTPPercent != 91 {
		t.Fatalf("invalid patch partially changed config: %#v", unchanged)
	}
	if err := trig.store.PatchGameConfig(ctx, trig.user.ID, map[string]string{"unknown_game_key": "1"}, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown key err = %v, want conflict", err)
	}
}

func TestPatchGameConfigConcurrentPartialUpdatesPreserveBothFields(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 0, 0, false)
	now := trig.clock.Now()
	patches := []map[string]string{
		{game.FishingStandardRTPKey: "91"},
		{game.FishingPremiumRTPKey: "87"},
	}
	errs := make(chan error, len(patches))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, changes := range patches {
		changes := changes
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- trig.store.PatchGameConfig(context.Background(), trig.user.ID, changes, now)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent partial patch: %v", err)
		}
	}
	snapshot, err := trig.store.GetGameConfigSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Fishing.StandardRTPPercent != 91 || snapshot.Fishing.PremiumRTPPercent != 87 {
		t.Fatalf("concurrent patch lost a field: standard=%d premium=%d", snapshot.Fishing.StandardRTPPercent, snapshot.Fishing.PremiumRTPPercent)
	}
}

func TestPatchGameConfigInvalidCombinationDoesNotPartiallyWrite(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 0, 0, false)
	ctx := context.Background()
	before, err := trig.store.GetGameConfigSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = trig.store.PatchGameConfig(ctx, trig.user.ID, map[string]string{
		game.FishingWormPriceMilliKey: "3000000",
		game.FishingStandardRTPKey:    "0",
	}, trig.clock.Now())
	if !errors.Is(err, ErrInvalidSiteConfig) {
		t.Fatalf("invalid combination err=%v", err)
	}
	after, err := trig.store.GetGameConfigSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Fishing.BaitPricesMilli["worm"] != before.Fishing.BaitPricesMilli["worm"] || after.Fishing.StandardRTPPercent != before.Fishing.StandardRTPPercent {
		t.Fatalf("invalid combination partially wrote: before=%#v after=%#v", before.Fishing, after.Fishing)
	}
}
