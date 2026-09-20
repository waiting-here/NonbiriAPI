package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	fishruntime "github.com/waiting-here/NonbiriAPI/internal/game/fishing/runtime"
	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func readRandomProof(f *duelWireFixture, player int, game, id string) randomness.Proof {
	f.t.Helper()
	r := f.call(player, "GET", "/api/games/"+game+"/randomness/"+id, nil, false)
	var envelope struct {
		Proof *randomness.Proof `json:"proof"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &envelope) != nil || envelope.Proof == nil {
		f.t.Fatalf("proof read failed: %d %s", r.Code, r.Body.String())
	}
	if r.Header().Get("Cache-Control") != "no-store" {
		f.t.Fatal("proof response can be cached")
	}
	return *envelope.Proof
}

func assertPrivateProof(f *duelWireFixture, game, id string, p randomness.Proof) {
	f.t.Helper()
	if p.Algorithm != randomness.Algorithm || len(p.Commitment) != 64 || p.Seed != "" || len(p.Streams) != 0 {
		f.t.Fatal("active response contains private random data")
	}
	var body string
	if err := f.store.DB().QueryRow(`SELECT private_json FROM game_random_proofs WHERE resource_id=?`, id).Scan(&body); err != nil {
		f.t.Fatal(err)
	}
	var private randomness.Proof
	if json.Unmarshal([]byte(body), &private) != nil || len(private.Seed) != 64 {
		f.t.Fatal("missing private seed")
	}
	for _, path := range []string{"/api/games", "/api/games/" + game + "/state", "/api/games/" + game + "/randomness/" + id} {
		for player := range 2 {
			r := f.call(player, "GET", path, nil, false)
			if r.Code != 200 || strings.Contains(r.Body.String(), private.Seed) || strings.Contains(r.Body.String(), `"private_json"`) {
				f.t.Fatal("private seed exposed through user projection", path, r.Code)
			}
		}
	}
}

func TestRandomnessBlackjackDisclosureAndShoeReplay(t *testing.T) {
	f := newBlackjackHTTPFixture(t)
	joinBlackjackHTTP(f, 0)
	h := readBlackjackHTTP(f, 0)
	id, start := h.Table.ID, h.Table.StartedAt
	opening := readRandomProof(f, 0, "blackjack", id)
	assertPrivateProof(f, "blackjack", id, opening)
	assertRandomnessExport(f, id, false)
	if observer := readRandomProof(f, 1, "blackjack", id); observer.Commitment != opening.Commitment {
		t.Fatal("spectator received another commitment")
	}
	for _, path := range []string{"/api/games/likes/randomness/" + id, "/api/games/blackjack/randomness/" + id + "?terminal=true", "/api/games/blackjack/randomness/" + id + "?seed=0"} {
		r := f.call(0, "GET", path, nil, false)
		if r.Code != 400 && r.Code != 404 {
			t.Fatal("client-controlled disclosure accepted", path, r.Code)
		}
	}
	f.clock.Store(start + 5)
	h = readBlackjackHTTP(f, 0)
	if h.Phase == "decision" {
		assertPrivateProof(f, "blackjack", id, readRandomProof(f, 0, "blackjack", id))
		var raw string
		if err := f.store.DB().QueryRow(`SELECT state_json FROM game_blackjack_sessions WHERE id=?`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var state engine.State
		if json.Unmarshal([]byte(raw), &state) != nil {
			t.Fatal("invalid private state")
		}
		var secretRaw string
		if err := f.store.DB().QueryRow(`SELECT private_json FROM game_random_proofs WHERE resource_id=?`, id).Scan(&secretRaw); err != nil {
			t.Fatal(err)
		}
		var proof randomness.Proof
		if json.Unmarshal([]byte(secretRaw), &proof) != nil {
			t.Fatal("invalid private proof")
		}
		seed, _ := hex.DecodeString(proof.Seed)
		replay, err := randomness.New(proof.Game, proof.ResourceID, proof.Rules, bytes.NewReader(seed))
		if err != nil {
			t.Fatal(err)
		}
		stream, _ := replay.Stream("shoe")
		deck, err := engine.Shuffle(stream)
		if err != nil || deck != state.Deck {
			t.Fatal("seed does not reproduce the real shoe")
		}
		if h.Table.Fact.Cards == nil || !h.Table.Fact.Cards.HoleHidden || len(h.Table.Fact.Cards.Dealer) != 1 {
			t.Fatal("dealer hole leaked")
		}
	}
	f.clock.Store(start + 25)
	h = readBlackjackHTTP(f, 0)
	if h.Phase != "result" {
		t.Fatal("table did not settle")
	}
	for player := range 2 {
		final := readRandomProof(f, player, "blackjack", id)
		if final.Commitment != opening.Commitment || randomness.Verify(final) != nil || len(final.Streams) != 1 {
			t.Fatal("terminal proof invalid")
		}
		for range 3 {
			if repeated := readRandomProof(f, player, "blackjack", id); wireBody(t, repeated) != wireBody(t, final) {
				t.Fatal("polling changed terminal randomness")
			}
		}
	}
	assertRandomnessExport(f, id, true)
	f.clock.Store(start + 31)
	if r := f.call(1, "GET", "/api/games/blackjack/randomness/"+id, nil, false); r.Code != 404 {
		t.Fatal("spectator accessed private history", r.Code)
	}
	if randomness.Verify(readRandomProof(f, 0, "blackjack", id)) != nil {
		t.Fatal("owner lost terminal proof")
	}
	f.checkLedger()
}

func assertRandomnessExport(f *duelWireFixture, id string, terminal bool) {
	f.t.Helper()
	r := f.call(0, "POST", "/api/account/export", nil, true)
	var doc lifecycle.ExportDocument
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &doc) != nil {
		f.t.Fatal("random export failed", r.Code, r.Body.String())
	}
	for _, p := range doc.Randomness {
		if p.ResourceID != id {
			continue
		}
		if (p.Seed != "") != terminal || !terminal && len(p.Streams) != 0 {
			f.t.Fatal("export disclosed random data at wrong phase")
		}
		return
	}
	f.t.Fatal("personal proof missing from export")
}

func TestRandomnessOlderSinglePlayerHTTP(t *testing.T) {
	f := newDuelWireFixture(t)
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key IN ('game_fishing_enabled','game_linklink_enabled','game_linklink_10x10_enabled')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1000' WHERE key='game_linklink_10x10_price_milli'`); err != nil {
		t.Fatal(err)
	}
	r := f.call(0, "POST", "/api/games/fishing/batches", map[string]any{"bait": "worm", "count": 10}, false)
	var fish fishruntime.FishingBatchResult
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &fish) != nil {
		t.Fatal("fishing start", r.Code, r.Body.String())
	}
	proof := readRandomProof(f, 0, "fishing", fish.BatchID)
	if randomness.Verify(proof) != nil || len(proof.Streams) != 1 || len(fish.Outcomes) != 10 {
		t.Fatal("real catch proof cannot replay")
	}
	assertRandomnessExport(f, fish.BatchID, true)
	if r := f.call(1, "GET", "/api/games/fishing/randomness/"+fish.BatchID, nil, false); r.Code != 404 {
		t.Fatal("another user's catch proof readable", r.Code)
	}
	r = f.call(0, "POST", "/api/games/linklink/sessions", map[string]any{"spec": "10x10"}, false)
	var board linklink.State
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &board) != nil {
		t.Fatal("linklink start", r.Code, r.Body.String())
	}
	opening := readRandomProof(f, 0, "linklink", board.SessionID)
	if opening.Seed != "" || len(opening.Streams) != 0 {
		t.Fatal("future board randomness disclosed")
	}
	assertRandomnessExport(f, board.SessionID, false)
	if r := f.call(1, "GET", "/api/games/linklink/randomness/"+board.SessionID, nil, false); r.Code != 404 {
		t.Fatal("another user's board proof readable", r.Code)
	}
	r = f.call(0, "POST", "/api/games/linklink/sessions/"+board.SessionID+"/abandon", map[string]any{"expected_revision": board.Revision, "confirmation": true}, false)
	if r.Code != 200 {
		t.Fatal("abandon", r.Code, r.Body.String())
	}
	final := readRandomProof(f, 0, "linklink", board.SessionID)
	if final.Commitment != opening.Commitment || randomness.Verify(final) != nil {
		t.Fatal("board proof lost while moving to summary")
	}
	assertRandomnessExport(f, board.SessionID, true)
	r = f.call(0, "POST", "/api/account/delete", map[string]string{"confirm": "DELETE"}, true)
	if r.Code != 204 {
		t.Fatal("delete", r.Code, r.Body.String())
	}
	var remaining int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM game_random_proofs WHERE resource_id IN (?,?)`, fish.BatchID, board.SessionID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("private proofs survived parent deletion", remaining, err)
	}
	f.checkLedger()
}

func TestRandomnessFailedSettlementRollsBackEveryDraw(t *testing.T) {
	f := newBlackjackHTTPFixture(t)
	joinBlackjackHTTP(f, 0)
	h := readBlackjackHTTP(f, 0)
	id := h.Table.ID
	opening := readRandomProof(f, 0, "blackjack", id)
	var before string
	if err := f.store.DB().QueryRow(`SELECT private_json FROM game_random_proofs WHERE resource_id=?`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`CREATE TRIGGER reject_random_settlement BEFORE UPDATE OF phase ON game_blackjack_sessions WHEN NEW.phase='result' BEGIN SELECT RAISE(ABORT,'injected settlement failure'); END`); err != nil {
		t.Fatal(err)
	}
	f.clock.Store(h.Table.StartedAt + 45)
	if r := f.call(0, "GET", "/api/games/blackjack/state", nil, false); r.Code != 500 {
		t.Fatal("injected failure ignored", r.Code)
	}
	var after string
	if err := f.store.DB().QueryRow(`SELECT private_json FROM game_random_proofs WHERE resource_id=?`, id).Scan(&after); err != nil || after != before {
		t.Fatal("rolled-back settlement changed seed or draws", err)
	}
	if active := readRandomProof(f, 0, "blackjack", id); active.Seed != "" || active.Commitment != opening.Commitment {
		t.Fatal("failed settlement disclosed or changed the seed")
	}
	if _, err := f.store.DB().Exec(`DROP TRIGGER reject_random_settlement`); err != nil {
		t.Fatal(err)
	}
	readBlackjackHTTP(f, 0)
	final := readRandomProof(f, 0, "blackjack", id)
	if final.Commitment != opening.Commitment || randomness.Verify(final) != nil {
		t.Fatal("retry changed committed randomness")
	}
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE game_random_proofs SET private_json=json_set(private_json,'$.seed',?) WHERE resource_id=?`, strings.Repeat("0", 64), id); err == nil {
		t.Fatal("immutable seed changed")
	}
	tx.Rollback()
	f.checkLedger()
}

func TestRandomnessDuelWaitsForWholeMatch(t *testing.T) {
	f := newDuelWireFixture(t)
	bid := f.matched("bidding", "tier1")
	opening := readRandomProof(f, 0, "bidding", bid.ID)
	for range 30 {
		h := f.read(0, "bidding")
		if h.Current == nil {
			break
		}
		assertPrivateProof(f, "bidding", bid.ID, readRandomProof(f, 0, "bidding", bid.ID))
		f.clock.Store(*h.Current.Deadline)
	}
	if h := f.read(0, "bidding"); h.Current != nil || h.LatestResult == nil {
		t.Fatal("bidding match did not complete")
	}
	final := readRandomProof(f, 0, "bidding", bid.ID)
	if final.Commitment != opening.Commitment || randomness.Verify(final) != nil {
		t.Fatal("bidding proof invalid")
	}
	likes := f.settlement()
	assertPrivateProof(f, "likes", likes.ID, readRandomProof(f, 0, "likes", likes.ID))
	f.clock.Add(5)
	likes = *f.read(0, "likes").Current
	assertPrivateProof(f, "likes", likes.ID, readRandomProof(f, 1, "likes", likes.ID))
	r := f.call(0, "POST", "/api/games/likes/sessions/"+likes.ID+"/surrender", map[string]string{"phase_seq": likes.PhaseSeq}, false)
	if r.Code != 200 {
		t.Fatal("surrender failed", r.Code)
	}
	if randomness.Verify(readRandomProof(f, 1, "likes", likes.ID)) != nil {
		t.Fatal("likes final proof invalid")
	}
	f.checkLedger()
}

func TestRandomnessBlackjackRestartDisclosesOriginalCancelledSeed(t *testing.T) {
	f := newBlackjackHTTPFixture(t)
	joinBlackjackHTTP(f, 0)
	h := readBlackjackHTTP(f, 0)
	id := h.Table.ID
	opening := readRandomProof(f, 0, "blackjack", id)
	f.clock.Store(h.Table.StartedAt + 15)
	prior := readBlackjackHTTP(f, 0).Phase
	if err := f.app.Close(); err != nil {
		t.Fatal(err)
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	f.app, err = buildApplicationWithGameClock(auditConfig(), f.store, vault, func() time.Time { return time.Unix(f.clock.Load(), 0) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.app.Close() })
	final := readRandomProof(f, 0, "blackjack", id)
	if final.Commitment != opening.Commitment || randomness.Verify(final) != nil {
		t.Fatal("restart lost committed seed")
	}
	var phase string
	want := "cancelled"
	if prior == "result" {
		want = "result"
	}
	if err := f.store.DB().QueryRow(`SELECT phase FROM game_blackjack_sessions WHERE id=?`, id).Scan(&phase); err != nil || phase != want {
		t.Fatal("restart did not cancel", phase, err)
	}
	f.checkLedger()
}
