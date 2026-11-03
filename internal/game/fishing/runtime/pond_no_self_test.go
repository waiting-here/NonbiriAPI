package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type pondBoundSource struct {
	bounds []uint64
	miss   bool
}

func (source *pondBoundSource) Uint64n(bound uint64) (uint64, error) {
	source.bounds = append(source.bounds, bound)
	if source.miss {
		return bound - 1, nil
	}
	return 0, nil
}

func TestPondSkipsOwnQueueKeepsProbabilityAndFIFO(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		others bool
		miss   bool
	}{{"interleaved-hit", true, false}, {"interleaved-miss", true, true}, {"only-self-hit", false, false}} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newGameFixture(t, nil)
			f.service.pondRandom = &scriptedSource{max: true}
			owner := f.seedUser("queue-recipient", fixtureFunding)
			a := f.seedUser("queue-donor-a", fixtureFunding)
			b := f.seedUser("queue-donor-b", fixtureFunding)
			for _, user := range []int64{owner, a, b} {
				pondPublic(t, f, user, true)
			}
			own := pondCatch(t, f, owner, 1100, 0, 1, 4, 7, 7, 7, 7, 7, 7, 7)
			donorA := pondCatch(t, f, a, 1101, 3, 6, 7, 7, 7, 7, 7, 7, 7, 7)
			donorB := pondCatch(t, f, b, 1102, 2, 5, 7, 7, 7, 7, 7, 7, 7, 7)
			pondReturn(t, f, owner, own, 0)
			pondReturn(t, f, owner, own, 1)
			if scenario.others {
				pondReturn(t, f, b, donorB, 0)
				pondReturn(t, f, a, donorA, 0)
			}
			pondReturn(t, f, owner, own, 2)
			if scenario.others {
				pondReturn(t, f, b, donorB, 1)
				pondReturn(t, f, a, donorA, 1)
			}
			ownRows := pondQueueIDs(t, f.database, `SELECT id FROM game_fishing_pond_queue WHERE donor_user_id=? ORDER BY id`, owner)
			// Gift attribution comes from the signed return, independent of a
			// later name or public-profile preference change.
			if _, err := f.database.Exec(`UPDATE users SET username='Renamed',game_profile_public=0 WHERE id IN (?,?)`, a, b); err != nil {
				t.Fatal(err)
			}
			random := &pondBoundSource{miss: scenario.miss}
			f.service.pondRandom = random
			beforeBase := f.random.callCount()
			result := pondCatch(t, f, owner, 1103, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7)
			gifts := 0
			wantSpecies := []string{"plastic_bag", "branch", "glasses", "phone_case"}
			wantDonors := []string{"queue-donor-b", "queue-donor-a", "queue-donor-b", "queue-donor-a"}
			var expectedBounds []uint64
			remaining := uint64(3)
			if scenario.others {
				remaining += 4
			}
			for i, outcome := range result.Outcomes {
				expectedBounds = append(expectedBounds, remaining+20)
				if scenario.others && !scenario.miss && i < 4 {
					if outcome.SpeciesKey != wantSpecies[i] || outcome.Pond.Gift == nil || outcome.Pond.Gift.DisplayName != wantDonors[i] || outcome.Pond.ReturnState != "gift" {
						t.Fatalf("gift %d: %#v", i, outcome)
					}
					remaining--
					gifts++
				} else if outcome.SpeciesKey != "fry" || outcome.Pond.Gift != nil || outcome.Pond.ReturnState != "available" {
					t.Fatalf("fallback %d: %#v", i, outcome)
				}
				if outcome.Reward != "0" || outcome.NetReward != "0" {
					t.Fatal("gift changed economic result")
				}
			}
			if f.random.callCount()-beforeBase != 20 || !reflect.DeepEqual(random.bounds, expectedBounds) {
				t.Fatalf("draws base=%d pond=%v want=%v", f.random.callCount()-beforeBase, random.bounds, expectedBounds)
			}
			if got := pondQueueIDs(t, f.database, `SELECT id FROM game_fishing_pond_queue WHERE donor_user_id=? ORDER BY id`, owner); !reflect.DeepEqual(got, ownRows) {
				t.Fatalf("own queue moved: %v -> %v", ownRows, got)
			}
			if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != int64(remaining) || f.scalar(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='cleanup'`, owner) != int64(3+gifts) {
				t.Fatal("queue size or cleanup score changed incorrectly")
			}
			replay, pending, err := f.service.StartFishing(t.Context(), StartInput{UserID: owner, Bait: "worm", Count: 10, IdempotencyKey: validTestKey(1103)})
			if err != nil || pending != nil || !reflect.DeepEqual(replay.Outcomes, result.Outcomes) || !reflect.DeepEqual(random.bounds, expectedBounds) {
				t.Fatal("retry changed results or consumed randomness", err)
			}
			assertPondHeadsMatchQueue(t, f)
		})
	}
}

func pondQueueIDs(t *testing.T, database *sql.DB, query string, args ...any) []int64 {
	t.Helper()
	rows, err := database.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func assertPondHeadsMatchQueue(t *testing.T, f *gameFixture) {
	t.Helper()
	expected := `SELECT donor_user_id,MIN(id) FROM game_fishing_pond_queue GROUP BY donor_user_id`
	actual := `SELECT donor_user_id,queue_id FROM game_fishing_pond_queue_heads`
	for _, query := range []string{expected + " EXCEPT " + actual, actual + " EXCEPT " + expected} {
		var mismatch bool
		if err := f.database.QueryRow("SELECT EXISTS(" + query + ")").Scan(&mismatch); err != nil || mismatch {
			t.Fatal("queue heads drifted", mismatch, err)
		}
	}
}

func TestPondGiftCandidatesBoundDonorsAndMatchFIFO(t *testing.T) {
	f := newGameFixture(t, nil)
	users := make([]int64, 12)
	for i := range users {
		users[i] = f.seedUser(fmt.Sprintf("candidate-donor-%d", i), fixtureFunding)
	}
	// More than K donors and overlapping donor sequences exercise both
	// truncations, including a heavy donor with many early eligible items.
	for i := 0; i < 60; i++ {
		donor := users[i%len(users)]
		if i < 15 {
			donor = users[0]
		}
		if _, err := f.database.Exec(`INSERT INTO game_fishing_pond_queue(donor_user_id,source_batch_id,source_ordinal,species_key,donor_display_name,returned_at) VALUES(?,?,0,'boot','Candidate',?)`, donor, fmt.Sprintf("fb_%021dA", i+1), fixtureNow); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []int64{users[0], users[4], f.adminID} {
		for _, limit := range []int{1, 4, 10} {
			want := pondQueueIDs(t, f.database, `SELECT id FROM game_fishing_pond_queue WHERE donor_user_id<>? ORDER BY id LIMIT ?`, user, limit)
			got := pondQueueIDs(t, f.database, `SELECT id FROM (`+pondGiftCandidatesSQL+`)`, user, limit, limit, limit)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("recipient=%d limit=%d got=%v want=%v", user, limit, got, want)
			}
		}
	}
	rows, err := f.database.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+pondGiftCandidatesSQL, users[0], 10, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plans []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(plans, " | ")
	if !strings.Contains(plan, "CO-ROUTINE h") || !strings.Contains(plan, "SEARCH q USING INTEGER PRIMARY KEY") || !strings.Contains(plan, "COVERING INDEX idx_fishing_pond_queue_donor (donor_user_id=?)") || strings.Contains(plan, "SCAN q") {
		t.Fatalf("candidate query lost bounded indexed lookups: %s", plan)
	}
	t.Log(plan)
}
