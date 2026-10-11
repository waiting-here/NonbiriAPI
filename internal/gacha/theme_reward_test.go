package gacha

import (
	"errors"
	"sync"
	"testing"
)

func ownTheme(t *testing.T, f *fixture, theme string) {
	t.Helper()
	for _, card := range catalog.Cards {
		if card.Theme == theme {
			f.exec(t, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'draw')", f.user, card.ID, f.at.Load())
		}
	}
}

func TestThemeRewardsCreditTheMatchingWalletExactlyOnce(t *testing.T) {
	for _, tc := range []struct{ theme, game, general, ticket string }{
		{"a", "10000", "0", "permanent"},
		{"i", "0", "10000", "limited"},
		{"k", "0", "10000", "limited"},
		{"l", "0", "10000", "limited"},
	} {
		t.Run(tc.theme, func(t *testing.T) {
			f := newFixture(t)
			f.enter(t)
			if _, err := f.act(f.user, "incomplete", Action{Action: "claim", Theme: tc.theme}); !errors.Is(err, ErrTheme) {
				t.Fatal(err)
			}
			ownTheme(t, f, tc.theme)
			input := Action{Action: "claim", Theme: tc.theme}
			first, err := f.act(f.user, "reward", input)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := f.act(f.user, "reward", input)
			if err != nil {
				t.Fatal(err)
			}
			for _, out := range []Outcome{first, replay} {
				s := out.Snapshot
				wantTickets := int64(1)
				if tc.ticket == "permanent" {
					wantTickets = 11
				}
				if s.Credits.Game != tc.game || s.Credits.General != tc.general ||
					s.Donation != "10" || s.Tickets[tc.ticket] != wantTickets {
					t.Fatal(s)
				}
			}
			if _, err = f.act(f.user, "again", input); !errors.Is(err, ErrClaimed) {
				t.Fatal(err)
			}
			if f.scalar(t, "SELECT count(*) FROM gacha_claims WHERE user_id=? AND kind='theme'", f.user) != 1 {
				t.Fatal("duplicate claim")
			}
			other, err := f.s.State(t.Context(), f.other)
			if err != nil || other.Credits.Game != "0" || other.Credits.General != "0" || other.Donation != "0" {
				t.Fatal(other, err)
			}
		})
	}
}

func TestThemeRewardRollsBackAllAssetsAndSerializesCompetingClaims(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	ownTheme(t, f, "a")
	f.recorder.fail.Store(true)
	if _, err := f.act(f.user, "rollback", Action{Action: "claim", Theme: "a"}); err == nil {
		t.Fatal("expected rollback")
	}
	state, err := f.s.State(t.Context(), f.user)
	if err != nil || state.Credits.Game != "0" || state.Donation != "0" || state.Tickets["permanent"] != 10 ||
		f.scalar(t, "SELECT count(*) FROM gacha_claims WHERE user_id=? AND kind='theme'", f.user) != 0 {
		t.Fatal(state, err)
	}
	f.recorder.fail.Store(false)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"concurrent-a", "concurrent-b"} {
		wg.Go(func() { _, err := f.act(f.user, key, Action{Action: "claim", Theme: "a"}); results <- err })
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrClaimed) {
			t.Fatal(err)
		}
	}
	state, err = f.s.State(t.Context(), f.user)
	if err != nil || successes != 1 || state.Credits.Game != "10000" || state.Donation != "10" || state.Tickets["permanent"] != 11 {
		t.Fatal(successes, state, err)
	}
}
