package runtime

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestZeroSizeOutcomeCreatesSingleBestAndExplicitWireSize(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	userID := fixture.seedUser("zero-best", fixtureFunding)
	result, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(400)})
	if err != nil || pending != nil || result == nil || result.Outcomes[0].SizeCM != 0 {
		t.Fatalf("zero-size start = (%#v,%#v,%v)", result, pending, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_fishing_best WHERE user_id=? AND species_key='boot' AND size_cm=0`, userID) != 1 {
		t.Fatal("zero-size outcome did not become the account best")
	}
	board, err := fixture.service.FishingLeaderboard(context.Background(), userID, "single")
	if err != nil || len(board.Entries) != 1 || board.Entries[0].SizeCM != 0 || board.Entries[0].SpeciesKey != "boot" {
		t.Fatalf("single board = (%#v,%v)", board, err)
	}
	wire, err := json.Marshal(board.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"rank":"1","species_key":"boot","size_cm":0,"identity":{"kind":"anonymous"},"is_me":true}`
	if string(wire) != want || bytes.Contains(wire, []byte("total_credits")) {
		t.Fatalf("single row wire = %s, want %s", wire, want)
	}
	totalWire, err := json.Marshal(FishingLeaderboardRow{Rank: "1", TotalCredits: "0", Identity: Identity{Kind: "anonymous"}, IsMe: true})
	if err != nil || string(totalWire) != `{"rank":"1","total_credits":"0","identity":{"kind":"anonymous"},"is_me":true}` || bytes.Contains(totalWire, []byte("species_key")) || bytes.Contains(totalWire, []byte("size_cm")) {
		t.Fatalf("total row wire = %s, error %v", totalWire, err)
	}
	if _, err = json.Marshal(FishingLeaderboardRow{Rank: "1", SpeciesKey: "boot", TotalCredits: "0", Identity: Identity{Kind: "anonymous"}}); err == nil {
		t.Fatal("mixed leaderboard row union was serialized")
	}
}

func TestFishingRankingPrivacyExclusionAndStableOrder(t *testing.T) {
	source := &scriptedSource{values: []uint64{
		968, 0, 20,
		968, 0, 19,
		968, 0, 18,
	}}
	fixture := newGameFixture(t, source)
	ids := []int64{
		fixture.seedUser("anonymous-rank", fixtureFunding),
		fixture.seedUser("public-rank", fixtureFunding),
		fixture.seedUser("banned-rank", fixtureFunding),
	}
	for index, userID := range ids {
		result, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(410 + index)})
		if err != nil || pending != nil || result == nil {
			t.Fatalf("start %d = (%#v,%#v,%v)", index, result, pending, err)
		}
	}
	if _, err := fixture.database.Exec(`UPDATE users SET game_profile_public=1,guild_nick='Public Angler',guild_avatar_url='' WHERE id=?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`UPDATE users SET is_banned=1,banned_reason='test' WHERE id=?`, ids[2]); err != nil {
		t.Fatal(err)
	}
	board, err := fixture.service.FishingLeaderboard(context.Background(), ids[1], "single")
	if err != nil || len(board.Entries) != 2 || board.Me != nil {
		t.Fatalf("single leaderboard = (%#v,%v)", board, err)
	}
	if board.Entries[0].Rank != "1" || board.Entries[0].SizeCM != 25 || board.Entries[0].Identity.Kind != "anonymous" || board.Entries[0].IsMe {
		t.Fatalf("anonymous row = %#v", board.Entries[0])
	}
	public := board.Entries[1]
	if public.Rank != "2" || public.SizeCM != 24 || public.Identity.Kind != "public" || public.Identity.DisplayName != "Public Angler" || public.Identity.AvatarURL != nil || !public.IsMe {
		t.Fatalf("public row = %#v", public)
	}
	wire, err := json.Marshal(public)
	if err != nil || !bytes.Contains(wire, []byte(`"avatar_url":null`)) {
		t.Fatalf("public null-avatar wire = %s, %v", wire, err)
	}
}

func TestFishingLeaderboardHiddenTieIsStable(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	users := []int64{
		fixture.seedUser("tie-a", fixtureFunding),
		fixture.seedUser("tie-b", fixtureFunding),
	}
	for index, userID := range users {
		result, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(415 + index)})
		if err != nil || pending != nil || result == nil || result.Outcomes[0].SizeCM != 0 || result.PayoutTotal != "0" {
			t.Fatalf("tie start %d = (%#v,%#v,%v)", index, result, pending, err)
		}
	}

	for _, test := range []struct {
		name     string
		board    string
		tieTable string
	}{
		{name: "single", board: "single", tieTable: "game_fishing_best"},
		{name: "total", board: "total", tieTable: "game_fishing_rank_aggregates"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var ties [2][]byte
			for index, userID := range users {
				if err := fixture.database.QueryRow(`SELECT public_tie_key FROM `+test.tieTable+` WHERE user_id=?`, userID).Scan(&ties[index]); err != nil {
					t.Fatal(err)
				}
			}
			wantMeFirst := bytes.Compare(ties[0], ties[1]) < 0
			first, err := fixture.service.FishingLeaderboard(context.Background(), users[0], test.board)
			if err != nil || len(first.Entries) != 2 || first.Me != nil {
				t.Fatalf("first tie board = (%#v,%v)", first, err)
			}
			if first.Entries[0].Rank != "1" || first.Entries[1].Rank != "2" || first.Entries[0].IsMe != wantMeFirst || first.Entries[1].IsMe == wantMeFirst {
				t.Fatalf("hidden tie order = %#v, want me-first=%v", first.Entries, wantMeFirst)
			}
			second, err := fixture.service.FishingLeaderboard(context.Background(), users[0], test.board)
			if err != nil || !equalJSONValue(t, first, second) {
				t.Fatalf("hidden tie order changed: first=%#v second=%#v err=%v", first, second, err)
			}
		})
	}
}

func TestFishingRollingFactsExpiryExactOnceAndBudget(t *testing.T) {
	t.Run("nonzero boundary", func(t *testing.T) {
		fixture := newGameFixture(t, &scriptedSource{max: true})
		userID := fixture.seedUser("expiry", fixtureFunding)
		first, _, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(420)})
		if err != nil {
			t.Fatal(err)
		}
		fixture.clock.Store(fixtureNow + 1)
		second, _, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(421)})
		if err != nil {
			t.Fatal(err)
		}
		window := int64(rankWindow.Seconds())
		fixture.clock.Store(first.SettledAt + window - 1)
		before, err := fixture.service.FishingLeaderboard(context.Background(), userID, "total")
		if err != nil || len(before.Entries) != 1 || before.Entries[0].TotalCredits != "25000" || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_rank_facts`) != 2 {
			t.Fatalf("expiry -1 = (%#v,%v)", before, err)
		}
		fixture.clock.Store(first.SettledAt + window)
		equal, err := fixture.service.FishingLeaderboard(context.Background(), userID, "total")
		if err != nil || len(equal.Entries) != 1 || equal.Entries[0].TotalCredits != "12500" || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_rank_facts`) != 1 {
			t.Fatalf("expiry equal = (%#v,%v)", equal, err)
		}
		var achieved int64
		if err = fixture.database.QueryRow(`SELECT score_achieved_at FROM game_fishing_rank_aggregates WHERE user_id=?`, userID).Scan(&achieved); err != nil {
			t.Fatal(err)
		}
		if achieved != first.SettledAt+window {
			t.Fatalf("changed score achieved_at = %d", achieved)
		}
		fixture.clock.Store(second.SettledAt + window)
		after, err := fixture.service.FishingLeaderboard(context.Background(), userID, "total")
		if err != nil || len(after.Entries) != 0 || after.Me != nil || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_rank_facts`) != 0 || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_rank_aggregates WHERE user_id=?`, userID) != 0 {
			t.Fatalf("expiry +1 = (%#v,%v)", after, err)
		}
		if _, err = fixture.service.FishingLeaderboard(context.Background(), userID, "total"); err != nil {
			t.Fatalf("exact-once replay: %v", err)
		}
	})

	t.Run("zero payout does not move achieved time", func(t *testing.T) {
		fixture := newGameFixture(t, &scriptedSource{})
		userID := fixture.seedUser("zero-expiry", fixtureFunding)
		first, _, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(422)})
		if err != nil {
			t.Fatal(err)
		}
		fixture.clock.Store(fixtureNow + 10)
		if _, _, err = fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(423)}); err != nil {
			t.Fatal(err)
		}
		fixture.clock.Store(first.SettledAt + int64(rankWindow.Seconds()))
		if _, err = fixture.service.FishingLeaderboard(context.Background(), userID, "total"); err != nil {
			t.Fatal(err)
		}
		var achieved int64
		if err = fixture.database.QueryRow(`SELECT score_achieved_at FROM game_fishing_rank_aggregates WHERE user_id=?`, userID).Scan(&achieved); err != nil {
			t.Fatal(err)
		}
		if achieved != first.SettledAt {
			t.Fatalf("zero payout moved achieved_at from %d to %d", first.SettledAt, achieved)
		}
	})

	t.Run("budget exhaustion", func(t *testing.T) {
		fixture := newGameFixture(t, &scriptedSource{max: true})
		userID := fixture.seedUser("budget", fixtureFunding)
		result, _, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(424)})
		if err != nil {
			t.Fatal(err)
		}
		fixture.clock.Store(result.SettledAt + int64(rankWindow.Seconds()))
		var ticks atomic.Int64
		fixture.service.budgetNow = func() time.Time { return time.Unix(ticks.Add(3), 0) }
		if _, err = fixture.service.FishingLeaderboard(context.Background(), userID, "total"); !errors.Is(err, ErrServiceUnavailable) {
			t.Fatalf("budget error = %v", err)
		}
		if fixture.scalar(`SELECT COUNT(*) FROM game_fishing_rank_facts WHERE user_id=?`, userID) != 1 {
			t.Fatal("budget failure partially expired facts")
		}
	})
}

func TestFishingLeaderboardTop20AndMe(t *testing.T) {
	fixture := newGameFixture(t, nil)
	one, _ := db.U128FromBig(big.NewInt(1))
	var requester int64
	for index := 1; index <= 21; index++ {
		userID := fixture.seedIdentity("top-"+big.NewInt(int64(index)).String(), false)
		if index == 21 {
			requester = userID
		}
		amount, _ := db.U128FromBig(big.NewInt(int64(22-index) * 1000))
		tie := make([]byte, 32)
		binary.BigEndian.PutUint64(tie[24:], uint64(index))
		if _, err := fixture.database.Exec(`INSERT INTO game_fishing_rank_aggregates(user_id,batch_count,total_payout,score_achieved_at,public_tie_key,revision,updated_at) VALUES(?,?,?,?,?,?,?)`, userID, db.EncodeU128(one), db.EncodeU128(amount), fixtureNow, tie, db.EncodeU128(one), fixtureNow); err != nil {
			t.Fatal(err)
		}
	}
	board, err := fixture.service.FishingLeaderboard(context.Background(), requester, "total")
	if err != nil || len(board.Entries) != 20 || board.Me == nil || board.Me.Rank != "21" || !board.Me.IsMe || board.Me.TotalCredits != "1" {
		t.Fatalf("Top20+me = (%#v,%v)", board, err)
	}
	for index, row := range board.Entries {
		if row.Rank != big.NewInt(int64(index+1)).String() || row.IsMe {
			t.Fatalf("entry %d = %#v", index, row)
		}
	}
}
