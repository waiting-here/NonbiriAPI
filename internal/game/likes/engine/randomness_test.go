package engine

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
)

func TestDistilledRandomCleanseReplaysFromRoundSeed(t *testing.T) {
	e, s := fixture(t, "quick", Selection{Role: "ChatGPT", Skills: []string{"PUB01", "PUB41"}}, Selection{Role: "Claude", Skills: []string{"PUB01"}})
	s.Players[0].Distill.Template = ptr("GPT41")
	s.Players[0].Distill.Level = ptr("I")
	s.Players[0].Distill.Learning = 0
	initialBuff(e, &s, 0, "B17:原版", 1)
	initialBuff(e, &s, 0, "B18:原版", 1)
	resolve := func() (State, RoundRecord, randomness.Proof) {
		secret, err := randomness.New("likes", "duel_AAAAAAAAAAAAAAAAAAAAAA", "quick/catalog", bytes.NewReader(bytes.Repeat([]byte{42}, 32)))
		if err != nil {
			t.Fatal(err)
		}
		stream, _ := secret.Stream("round/1")
		next, record, err := e.Resolve(s, [2]Plan{plan("PUB41"), EmptyPlan()}, func(n int) (int, error) { v, err := stream.Uint64n(uint64(n)); return int(v), err })
		if err != nil {
			t.Fatal(err)
		}
		return next, record, secret.Public(true)
	}
	one, record, proof := resolve()
	two, repeated, again := resolve()
	if len(record.Draws) == 0 || !reflect.DeepEqual(one, two) || !reflect.DeepEqual(record, repeated) || !reflect.DeepEqual(proof, again) || randomness.Verify(proof) != nil {
		t.Fatal("random cleanse cannot reproduce")
	}
}
