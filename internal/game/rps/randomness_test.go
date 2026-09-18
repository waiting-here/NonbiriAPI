package rps

import (
	"context"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
)

func TestAutomaticGesturesReplayFromPublicPhaseWithoutGrowingProof(t *testing.T) {
	f := newRPSFixture(t)
	f.service.proofs = true
	users, _ := f.startThree(game.RPSModeStandard, 100_000, 1600)
	r := f.sessionForUser(users[0])
	var before string
	if err := f.database.QueryRow(`SELECT private_json FROM game_random_proofs WHERE resource_id=?`, r.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	proof, err := randomness.DecodePrivate([]byte(before))
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Store(*r.PhaseDeadline)
	if processed, err := f.service.runDeadlineOne(context.Background(), r.ID, f.clock.Load()); err != nil || !processed {
		t.Fatal(processed, err)
	}
	next, found := f.maybeSession(r.ID)
	if !found || next.Phase != PhaseDealerRaise {
		t.Fatal("expected locked automatic gestures")
	}
	for seat := range 3 {
		index, err := proof.Indexed("automatic/"+strconv.Itoa(seat)+"/"+r.PhaseSeq.Decimal(), 3)
		if err != nil {
			t.Fatal(err)
		}
		gesture, err := openGesture(f.service.keys.gesture, r.ID, seat, r.PhaseSeq, r.RulesVersion, next.Seats[seat].GestureEnvelope)
		if err != nil || gesture != []string{GestureRock, GestureScissors, GesturePaper}[index] {
			t.Fatal("automatic gesture differs from committed seed", err)
		}
	}
	var after string
	if err := f.database.QueryRow(`SELECT private_json FROM game_random_proofs WHERE resource_id=?`, r.ID).Scan(&after); err != nil || after != before {
		t.Fatal("automatic turns grow proof", err)
	}
}
