package rps

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func mustRPSU128(t *testing.T, value string) db.U128 {
	t.Helper()
	result, err := db.ParseU128Decimal(value)
	if err != nil {
		t.Fatalf("parse U128 %q: %v", value, err)
	}
	return result
}

func referenceReveal(gestures [3]string, surrendered [3]bool) (revealEvaluation, error) {
	active := make([]int, 0, 3)
	for seat := range gestures {
		if !validGesture(gestures[seat]) {
			return revealEvaluation{}, ErrInvariant
		}
		if !surrendered[seat] {
			active = append(active, seat)
		}
	}
	result := revealEvaluation{}
	switch len(active) {
	case 1:
		result.Code = RevealTwoSurrenders
		result.Weights[active[0]] = 6
		return result, nil
	case 2:
		if gestures[active[0]] == gestures[active[1]] {
			result.Code = RevealOneSurrenderTie
			result.Weights[active[0]], result.Weights[active[1]] = 3, 3
			return result, nil
		}
		result.Code = RevealOneSurrenderDecided
		winner, loser := active[0], active[1]
		if !gestureBeats(gestures[winner], gestures[loser]) {
			winner, loser = loser, winner
		}
		result.Weights[winner], result.Weights[loser] = 4, 2
		return result, nil
	case 3:
		scores := [3]int{}
		for left := 0; left < 3; left++ {
			for right := left + 1; right < 3; right++ {
				switch {
				case gestures[left] == gestures[right]:
					scores[left]++
					scores[right]++
				case gestureBeats(gestures[left], gestures[right]):
					scores[left] += 2
				default:
					scores[right] += 2
				}
			}
		}
		result.Weights = scores
		if scores == [3]int{2, 2, 2} {
			result.Tie = true
			if gestures[0] == gestures[1] {
				result.Code = RevealThreeEqual
			} else {
				result.Code = RevealAllDistinct
			}
		} else {
			zeroes := 0
			for _, score := range scores {
				if score == 0 {
					zeroes++
				}
			}
			if zeroes == 1 {
				result.Code = RevealTwoBeatOne
			} else {
				result.Code = RevealOneBeatsTwo
			}
		}
		return result, nil
	default:
		return revealEvaluation{}, ErrInvariant
	}
}

func TestEvaluateRevealExhaustiveReference(t *testing.T) {
	gestures := []string{GestureRock, GestureScissors, GesturePaper}
	for first := range gestures {
		for second := range gestures {
			for third := range gestures {
				plays := [3]string{gestures[first], gestures[second], gestures[third]}
				for mask := 0; mask < 8; mask++ {
					surrendered := [3]bool{mask&1 != 0, mask&2 != 0, mask&4 != 0}
					if mask == 7 {
						if _, err := evaluateReveal(plays, surrendered); !errors.Is(err, ErrInvariant) {
							t.Fatalf("all surrendered accepted: plays=%v err=%v", plays, err)
						}
						continue
					}
					got, err := evaluateReveal(plays, surrendered)
					want, referenceErr := referenceReveal(plays, surrendered)
					if err != nil || referenceErr != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("plays=%v surrendered=%v got=%+v err=%v want=%+v referenceErr=%v",
							plays, surrendered, got, err, want, referenceErr)
					}
					total := 0
					for _, weight := range got.Weights {
						total += weight
					}
					if total != 6 {
						t.Fatalf("weight total=%d for %+v", total, got)
					}
				}
			}
		}
	}
}

func TestPayoutAndCutConservationProperties(t *testing.T) {
	weightSets := [][3]int{{2, 2, 2}, {4, 1, 1}, {3, 3, 0}, {4, 2, 0}, {6, 0, 0}}
	for pool := int64(0); pool <= 10_000; pool++ {
		for _, weights := range weightSets {
			returned, carry, err := payouts(big.NewInt(pool), weights)
			if err != nil {
				t.Fatalf("payout pool=%d weights=%v: %v", pool, weights, err)
			}
			total := cloneBig(carry)
			for seat, value := range returned {
				want := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(pool), big.NewInt(int64(weights[seat]))), big.NewInt(6))
				if value.Cmp(want) != 0 {
					t.Fatalf("payout pool=%d weights=%v seat=%d got=%s want=%s", pool, weights, seat, value, want)
				}
				total.Add(total, value)
			}
			if total.Cmp(big.NewInt(pool)) != 0 || carry.Sign() < 0 {
				t.Fatalf("payout conservation pool=%d weights=%v total=%s carry=%s", pool, weights, total, carry)
			}
		}
	}
	pumps := []PumpsBP{
		{}, {Platform: 1}, {Welfare: 100, Thursday: 100},
		{Platform: 3333, Welfare: 3333, Thursday: 3333}, {Platform: 9997, Welfare: 1, Thursday: 1},
	}
	for input := int64(1); input <= 20_000; input++ {
		for _, rates := range pumps {
			cut, err := cutsForInput(big.NewInt(input), rates)
			if err != nil {
				t.Fatalf("cut input=%d rates=%+v: %v", input, rates, err)
			}
			total := new(big.Int).Add(cut.Platform, cut.Welfare)
			total.Add(total, cut.Thursday)
			total.Add(total, cut.Net)
			if total.Cmp(big.NewInt(input)) != 0 || cut.Net.Sign() <= 0 {
				t.Fatalf("cut conservation input=%d rates=%+v total=%s net=%s", input, rates, total, cut.Net)
			}
		}
	}
}

func TestMultiplierAndFutureRowBoundaries(t *testing.T) {
	for completed, want := range map[int64]string{0: "1", 8: "1", 9: "2", 11: "2", 12: "3", 15: "4"} {
		got, err := permanentMultiplier("deathmatch", big.NewInt(completed))
		if err != nil || got.String() != want {
			t.Fatalf("permanent n=%d got=%v err=%v want=%s", completed, got, err, want)
		}
	}
	for completed, want := range map[int64]string{0: "7", 2: "7", 3: "8", 4: "9", 10: "15"} {
		got, err := paidPlanMultiplier("deathmatch", big.NewInt(7), big.NewInt(completed))
		if err != nil || got.String() != want {
			t.Fatalf("paid c=%d got=%v err=%v want=%s", completed, got, err, want)
		}
	}
	const maximumUnixSecond int64 = 253402300799
	for _, started := range []int64{0, 1, rpsTestNow, maximumUnixSecond - 1, maximumUnixSecond} {
		got, err := sessionFutureRows(started)
		if err != nil || got.Big().Sign() <= 0 || got.Big().BitLen() > 43 {
			t.Fatalf("future rows started=%d got=%s bitlen=%d err=%v", started, got.Decimal(), got.Big().BitLen(), err)
		}
		duration := maximumUnixSecond - started
		ceil := func(value, divisor int64) int64 { return (value + divisor - 1) / divisor }
		want := new(big.Int).SetInt64(16*(90*ceil(duration, 60)+ceil(duration, 5)+91) + 1)
		if got.Big().Cmp(want) != 0 {
			t.Fatalf("future rows started=%d got=%s want=%s", started, got.Decimal(), want)
		}
	}
	for _, started := range []int64{-1, maximumUnixSecond + 1} {
		if _, err := sessionFutureRows(started); !errors.Is(err, ErrInvariant) {
			t.Fatalf("future rows accepted started=%d: %v", started, err)
		}
	}
}

func TestGestureEnvelopeGoldenAndBinding(t *testing.T) {
	key := [32]byte{}
	for index := range key {
		key[index] = byte(index)
	}
	nonce := make([]byte, 12)
	for index := range nonce {
		nonce[index] = byte(index)
	}
	phase := mustRPSU128(t, "42")
	const sessionID = "rps_AAAAAAAAAAAAAAAAAAAAAA"
	envelope, err := sealGesture(bytes.NewReader(nonce), key, sessionID, 1, phase, 1, GesturePaper)
	if err != nil {
		t.Fatal(err)
	}
	const wantEnvelopeHex = "01000102030405060708090a0b3763a67eb76b6993370dd4fe1d2e96e49b3d90e8bc"
	if got := hex.EncodeToString(envelope); got != wantEnvelopeHex {
		t.Fatalf("envelope=%s want=%s", got, wantEnvelopeHex)
	}
	opened, err := openGesture(key, sessionID, 1, phase, 1, envelope)
	if err != nil || opened != GesturePaper {
		t.Fatalf("open=(%q,%v)", opened, err)
	}
	wrongPhase := mustRPSU128(t, "43")
	for name, open := range map[string]func() error{
		"seat":  func() error { _, err := openGesture(key, sessionID, 2, phase, 1, envelope); return err },
		"phase": func() error { _, err := openGesture(key, sessionID, 1, wrongPhase, 1, envelope); return err },
		"rules": func() error { _, err := openGesture(key, sessionID, 1, phase, 2, envelope); return err },
	} {
		if err := open(); !errors.Is(err, ErrInvariant) {
			t.Fatalf("wrong %s accepted: %v", name, err)
		}
	}
	for index := range envelope {
		tampered := append([]byte(nil), envelope...)
		tampered[index] ^= 0x80
		if _, err := openGesture(key, sessionID, 1, phase, 1, tampered); !errors.Is(err, ErrInvariant) {
			t.Fatalf("tamper byte %d accepted: %v", index, err)
		}
	}
}

func TestViewerProjectionStrictUnionAndHiddenGesture(t *testing.T) {
	one := mustRPSU128(t, "1")
	two := mustRPSU128(t, "2")
	deadline := rpsTestNow + 20
	record := sessionRecord{
		ID: "rps_AAAAAAAAAAAAAAAAAAAAAA", Mode: "standard", RulesVersion: 1,
		State: StateStarted, Phase: PhaseFollowers, Revision: two, PhaseSeq: two, IdentityEpoch: one,
		BaseMilli: 1000, GestureSeconds: 20, DealerSeconds: 15, FollowerSeconds: 15,
		PermanentMultiplier: one, CurrentPlanMultiplier: &one, ReminderState: "none", PhaseDeadline: &deadline,
	}
	dealer := 0
	record.DealerSeat = &dealer
	for seat := range record.Seats {
		userID := int64(seat + 1)
		display := "player-" + string(rune('a'+seat))
		record.Seats[seat] = seatRecord{SeatNo: seat, UserID: &userID, DeletionState: "active", DisplayName: &display,
			StartingBalance: one, CurrentBalance: one, GestureEnvelope: []byte("must-never-project"), GesturePhaseSeq: &one}
	}
	call := FollowerCall
	record.Seats[1].FollowerAction = &call
	record.Seats[1].LastActionPhaseSeq = &two
	for index := 0; index < 70; index++ {
		if err := appendEvent(&record, EventPhaseChanged, phaseChangedPayload{Phase: PhaseFollowers, Deadline: &deadline}); err != nil {
			t.Fatalf("append event %d: %v", index, err)
		}
	}
	state, err := projectState(record, 2, rpsTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if !state.EventsTruncated || state.FirstAvailableSeq != "7" || len(state.RecentEvents) != maxRecentEvents {
		t.Fatalf("event projection truncated=%v first=%s len=%d", state.EventsTruncated, state.FirstAvailableSeq, len(state.RecentEvents))
	}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, forbidden := range []string{"must-never-project", "current_gesture", "gesture_phase_seq", "last_action_phase_seq"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("hidden state leaked %q: %s", forbidden, text)
		}
	}
	if state.Seats[1].FollowerAction == nil || *state.Seats[1].FollowerAction != FollowerCall || state.Seats[2].FollowerAction != nil {
		t.Fatalf("viewer follower projection=%+v", state.Seats)
	}
	deleted := record.Seats[2]
	deleted.UserID, deleted.DisplayName, deleted.DeletionState = nil, nil, "deletion_pending"
	deleted.GestureEnvelope, deleted.GesturePhaseSeq = nil, nil
	deletedBody, err := json.Marshal(Seat{SeatNo: 2, Viewer: "opponent", DeletionState: deleted.DeletionState,
		StartingBalance: "1", CurrentBalance: "1", CurrentRoundInput: "0", TotalInput: "0", TotalReturned: "0", TimeoutCount: "0"})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"display_name", "avatar_url", "fun_snapshot", "visible_gesture", "follower_action"} {
		if strings.Contains(string(deletedBody), forbidden) {
			t.Fatalf("deleted union leaked %q: %s", forbidden, deletedBody)
		}
	}
}

func TestStrictJSONAndHomeUnion(t *testing.T) {
	type request struct {
		Value string `json:"value"`
	}
	valid := request{}
	if !rpsDecodeStrictBytes([]byte(`{"value":"ok"}`), &valid) || valid.Value != "ok" {
		t.Fatalf("valid strict decode=%+v", valid)
	}
	for _, body := range []string{
		`{"value":"a","value":"b"}`,
		`{"value":"a","extra":1}`,
		`{"value":"a"} {}`,
		`{"value":null}`,
		`null`,
		`[]`,
	} {
		if rpsDecodeStrictBytes([]byte(body), &request{}) {
			t.Fatalf("invalid strict JSON accepted: %s", body)
		}
	}
	idle := HomeState{Kind: "idle", Modes: map[string]ModeConfig{"quick": {}, "standard": {}, "deathmatch": {}}}
	body, err := json.Marshal(idle)
	if err != nil || strings.Contains(string(body), `"queue"`) || strings.Contains(string(body), `"session"`) || strings.Contains(string(body), `"result"`) {
		t.Fatalf("idle union body=%s err=%v", body, err)
	}
	if _, err := json.Marshal(HomeState{Kind: "idle", Modes: map[string]ModeConfig{"quick": {}}}); !errors.Is(err, ErrInvariant) {
		t.Fatalf("invalid idle union error=%v", err)
	}
}

func TestEventPayloadRegistryIsClosed(t *testing.T) {
	valid := []RecentEvent{
		{Seq: "1", IdentityEpoch: "1", PhaseSeq: "1", Kind: EventPhaseChanged,
			SafePayload: json.RawMessage(`{"phase":"gesture","deadline":2000000020}`)},
		{Seq: "2", IdentityEpoch: "1", PhaseSeq: "1", Kind: EventActionLocked,
			SafePayload: json.RawMessage(`{"seat_no":2,"action_kind":"gesture"}`)},
		{Seq: "3", IdentityEpoch: "1", PhaseSeq: "1", Kind: EventReveal,
			SafePayload: json.RawMessage(`{"gestures":[{"seat_no":0,"gesture":"rock"},{"seat_no":1,"gesture":"scissors"},{"seat_no":2,"gesture":"scissors"}],"result_code":"one_beats_two"}`)},
		{Seq: "4", IdentityEpoch: "1", PhaseSeq: "1", Kind: EventSettlement,
			SafePayload: json.RawMessage(`{"seat_deltas":[{"seat_no":0,"delta":"2"},{"seat_no":1,"delta":"0"},{"seat_no":2,"delta":"0"}],"player_pool":"0.001","cuts":{"platform":"0","welfare":"0","thursday":"0"}}`)},
		{Seq: "5", IdentityEpoch: "1", PhaseSeq: "1", Kind: EventReminder,
			SafePayload: json.RawMessage(`{"free_tie_count":"3"}`)},
		{Seq: "6", IdentityEpoch: "2", PhaseSeq: "1", Kind: EventIdentityReset,
			SafePayload: json.RawMessage(`{"seat_no":1,"identity_epoch":"2"}`)},
		{Seq: "7", IdentityEpoch: "2", PhaseSeq: "2", Kind: EventTerminal,
			SafePayload: json.RawMessage(`{"terminal_reason":"quick_resolved"}`)},
	}
	for _, event := range valid {
		if !validEvent(event) {
			t.Fatalf("valid event rejected: %+v", event)
		}
	}
	invalid := []RecentEvent{
		{Seq: "0", IdentityEpoch: "1", PhaseSeq: "1", Kind: EventTerminal,
			SafePayload: json.RawMessage(`{"terminal_reason":"quick_resolved"}`)},
		{Seq: "1", IdentityEpoch: "0", PhaseSeq: "1", Kind: EventTerminal,
			SafePayload: json.RawMessage(`{"terminal_reason":"quick_resolved"}`)},
		{Seq: "1", IdentityEpoch: "1", PhaseSeq: "0", Kind: EventTerminal,
			SafePayload: json.RawMessage(`{"terminal_reason":"quick_resolved"}`)},
		{Seq: "1", IdentityEpoch: "1", PhaseSeq: "1", Kind: EventPhaseChanged,
			SafePayload: json.RawMessage(`{"phase":"gesture","deadline":2000000020,"extra":true}`)},
		{Seq: "1", IdentityEpoch: "1", PhaseSeq: "1", Kind: EventActionLocked,
			SafePayload: json.RawMessage(`{"seat_no":3,"action_kind":"gesture"}`)},
		{Seq: "1", IdentityEpoch: "1", PhaseSeq: "1", Kind: EventReveal,
			SafePayload: json.RawMessage(`{"gestures":[{"seat_no":0,"gesture":"rock"},{"seat_no":0,"gesture":"scissors"},{"seat_no":2,"gesture":"paper"}],"result_code":"all_distinct"}`)},
		{Seq: "1", IdentityEpoch: "1", PhaseSeq: "1", Kind: EventSettlement,
			SafePayload: json.RawMessage(`{"seat_deltas":[{"seat_no":0,"delta":"01"},{"seat_no":1,"delta":"0"},{"seat_no":2,"delta":"0"}],"player_pool":"0","cuts":{"platform":"0","welfare":"0","thursday":"0"}}`)},
		{Seq: "1", IdentityEpoch: "1", PhaseSeq: "1", Kind: EventReminder,
			SafePayload: json.RawMessage(`{"free_tie_count":"6"}`)},
		{Seq: "1", IdentityEpoch: "2", PhaseSeq: "1", Kind: EventIdentityReset,
			SafePayload: json.RawMessage(`{"seat_no":1,"identity_epoch":"3"}`)},
		{Seq: "1", IdentityEpoch: "1", PhaseSeq: "1", Kind: "future_event", SafePayload: json.RawMessage(`{}`)},
	}
	for _, event := range invalid {
		if validEvent(event) {
			t.Fatalf("invalid event accepted: %+v", event)
		}
	}
}

func FuzzEvaluateRevealReference(f *testing.F) {
	for _, seed := range []uint8{0, 1, 7, 17, 63, 127, 255} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint8) {
		gestures := []string{GestureRock, GestureScissors, GesturePaper}
		plays := [3]string{gestures[int(seed)%3], gestures[int(seed/3)%3], gestures[int(seed/9)%3]}
		mask := int(seed/27) % 7
		surrendered := [3]bool{mask&1 != 0, mask&2 != 0, mask&4 != 0}
		got, err := evaluateReveal(plays, surrendered)
		want, referenceErr := referenceReveal(plays, surrendered)
		if err != nil || referenceErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%d plays=%v surrendered=%v got=%+v err=%v want=%+v referenceErr=%v",
				seed, plays, surrendered, got, err, want, referenceErr)
		}
	})
}

func FuzzCutConservation(f *testing.F) {
	for _, seed := range []struct {
		input                       uint64
		platform, welfare, thursday uint16
	}{{1, 0, 0, 0}, {999, 100, 100, 100}, {9_000_000_000_000_000, 9997, 1, 1}} {
		f.Add(seed.input, seed.platform, seed.welfare, seed.thursday)
	}
	f.Fuzz(func(t *testing.T, rawInput uint64, platform, welfare, thursday uint16) {
		input := int64(rawInput%9_000_000_000_000_000) + 1
		p := int(platform % 10_000)
		w := int(welfare % uint16(10_000-p))
		remaining := 9_999 - p - w
		th := 0
		if remaining > 0 {
			th = int(thursday) % (remaining + 1)
		}
		cut, err := cutsForInput(big.NewInt(input), PumpsBP{Platform: p, Welfare: w, Thursday: th})
		if err != nil {
			t.Fatal(err)
		}
		total := new(big.Int).Add(cut.Platform, cut.Welfare)
		total.Add(total, cut.Thursday)
		total.Add(total, cut.Net)
		if total.Cmp(big.NewInt(input)) != 0 || cut.Net.Sign() <= 0 {
			t.Fatalf("input=%d rates=%d/%d/%d total=%s net=%s", input, p, w, th, total, cut.Net)
		}
	})
}
