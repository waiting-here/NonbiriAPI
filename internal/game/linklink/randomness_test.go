package linklink

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
)

func TestRandomProofReproducesBoardAndSurvivesSummaryTransfer(t *testing.T) {
	f := newFixture(t)
	f.service.proofs = true
	user, binding := f.seedUser("proof", testFunding)
	input := StartInput{UserID: user, Spec: "10x10", IdempotencyKey: f.key(8600)}
	result, err := f.service.Start(context.Background(), input)
	if err != nil || result.State == nil {
		t.Fatalf("start: %v", err)
	}
	id := result.State.SessionID
	read := func() randomness.Proof {
		tx, err := f.database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		secret, err := randomness.Load(context.Background(), tx, "linklink", id)
		if err != nil || secret == nil {
			t.Fatal("load", err)
		}
		return secret.Public(true)
	}
	proof := read()
	seed, _ := hex.DecodeString(proof.Seed)
	replay, _ := randomness.New(proof.Game, proof.ResourceID, proof.Rules, bytes.NewReader(seed))
	stream, _ := replay.Stream("initial")
	definition, _ := resolveSpec("10x10")
	board, err := newBoard(definition, stream)
	if err != nil || !bytes.Equal(board.tiles, f.loadBoard(id).tiles) {
		t.Fatal("board replay", err)
	}
	if _, err := f.service.Start(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if repeated := read(); repeated.Commitment != proof.Commitment || repeated.Streams[0] != proof.Streams[0] {
		t.Fatal("idempotent start rerolled")
	}
	if _, err := f.service.Abandon(context.Background(), AbandonInput{UserID: user, SessionID: id, SessionBinding: binding, ExpectedRevision: "1", Confirmation: true, IdempotencyKey: f.key(8601)}); err != nil {
		t.Fatal(err)
	}
	if final := read(); randomness.Verify(final) != nil || final.Commitment != proof.Commitment {
		t.Fatal("summary lost proof")
	}
	// Current geometry generation plus all five one-pass hints stays below
	// the protocol budget even if every generation attempt is exhausted.
	if 8*(50*(32*2+1)+49)+5*99 > randomness.MaxSamples {
		t.Fatal("legitimate maximum generation exceeds proof budget")
	}
}
