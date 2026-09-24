package runtime

import (
	"context"
	"testing"
)

func TestFishingChanceAndRevisionAreFrozenAtAcceptance(t *testing.T) {
	f := newGameFixture(t, legendSource(2, 137, 0))
	user := f.seedUser("chance-freeze", fixtureFunding)
	ctx := context.Background()
	if _, err := f.database.Exec(`UPDATE site_config SET value='10000' WHERE key='game_fishing_blue_fish_chance_bps'`); err != nil {
		t.Fatal(err)
	}
	f.service.beforeSettlement = func(string) error { return errInjected }
	input := StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(9801)}
	_, pending, err := f.service.startLegacy(ctx, input)
	if err != nil || pending == nil {
		t.Fatal(pending, err)
	}
	var chance, revision, current int64
	if err := f.database.QueryRow(`SELECT blue_fish_chance_bps,config_revision FROM game_fishing_batches WHERE id=?`, pending.BatchID).Scan(&chance, &revision); err != nil {
		t.Fatal(err)
	}
	if err := f.database.QueryRow(`SELECT revision FROM config_revisions WHERE domain='games'`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if chance != 10000 || revision != current {
		t.Fatal("unfrozen acceptance configuration", chance, revision, current)
	}
	if _, err := f.database.Exec(`UPDATE game_fishing_batches SET blue_fish_chance_bps=0 WHERE id=?`, pending.BatchID); err == nil {
		t.Fatal("accepted probability mutable")
	}
	if _, err := f.database.Exec(`UPDATE game_fishing_batches SET config_revision=config_revision+1 WHERE id=?`, pending.BatchID); err == nil {
		t.Fatal("accepted revision mutable")
	}
	if _, err := f.database.Exec(`UPDATE site_config SET value='0' WHERE key='game_fishing_blue_fish_chance_bps'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.Exec(`UPDATE config_revisions SET revision=revision+1 WHERE domain='games'`); err != nil {
		t.Fatal(err)
	}
	calls := f.random.callCount()
	f.service.beforeSettlement = nil
	if _, err := f.service.settle(ctx, pending.BatchID, user, fixtureNow, false); err != nil {
		t.Fatal(err)
	}
	replay, _, err := f.service.startLegacy(ctx, input)
	if err != nil || replay == nil || replay.Outcomes[0].BlueFatFishLengthCM == nil || *replay.Outcomes[0].BlueFatFishLengthCM != "201" || f.random.callCount() != calls {
		t.Fatal("retry rerolled frozen catch", replay, err)
	}
	if err := f.service.AcknowledgeFishing(ctx, user, pending.BatchID); err != nil {
		t.Fatal(err)
	}
	f.random.mu.Lock()
	f.random.values = []uint64{4728, 2, 37}
	f.random.mu.Unlock()
	input.IdempotencyKey = validTestKey(9802)
	next, pending, err := f.service.startLegacy(ctx, input)
	if err != nil || pending != nil || next == nil || next.Outcomes[0].Tier != "legend" || next.Outcomes[0].BlueFatFishLengthCM != nil {
		t.Fatal("new probability not used", next, pending, err)
	}
	if err := f.database.QueryRow(`SELECT blue_fish_chance_bps,config_revision FROM game_fishing_batches WHERE id=?`, next.BatchID).Scan(&chance, &revision); err != nil || chance != 0 || revision != current+1 {
		t.Fatal(chance, revision, err)
	}
	if next.Outcomes[0].Reward != replay.Outcomes[0].Reward || next.Outcomes[0].SizeCM != replay.Outcomes[0].SizeCM {
		t.Fatal("probability changed economic outcome")
	}
}
