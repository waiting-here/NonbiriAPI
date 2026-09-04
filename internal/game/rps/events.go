package rps

import (
	"encoding/json"
	"math/big"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type phaseChangedPayload struct {
	Phase    string `json:"phase"`
	Deadline *int64 `json:"deadline"`
}

type actionLockedPayload struct {
	SeatNo     int    `json:"seat_no"`
	ActionKind string `json:"action_kind"`
}

type revealGesturePayload struct {
	SeatNo  int    `json:"seat_no"`
	Gesture string `json:"gesture"`
}

type revealPayload struct {
	Gestures   []revealGesturePayload `json:"gestures"`
	ResultCode string                 `json:"result_code"`
}

type settlementSeatPayload struct {
	SeatNo int    `json:"seat_no"`
	Delta  string `json:"delta"`
}

type settlementPayload struct {
	SeatDeltas []settlementSeatPayload `json:"seat_deltas"`
	PlayerPool string                  `json:"player_pool"`
	Cuts       Cuts                    `json:"cuts"`
}

type reminderPayload struct {
	FreeTieCount string `json:"free_tie_count"`
}

type identityResetPayload struct {
	SeatNo        int    `json:"seat_no"`
	IdentityEpoch string `json:"identity_epoch"`
}

type terminalPayload struct {
	TerminalReason string `json:"terminal_reason"`
}

func appendEvent(record *sessionRecord, kind string, payload any) error {
	if record == nil {
		return ErrInvariant
	}
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) == 0 || !json.Valid(raw) {
		return ErrInvariant
	}
	sequence, err := incU128(record.RecentLastSeq)
	if err != nil {
		return err
	}
	event := RecentEvent{
		Seq: sequence.Decimal(), IdentityEpoch: record.IdentityEpoch.Decimal(), Kind: kind,
		PhaseSeq: record.PhaseSeq.Decimal(), SafePayload: raw,
	}
	if !validEvent(event) {
		return ErrInvariant
	}
	record.RecentEvents = append(record.RecentEvents, event)
	if len(record.RecentEvents) > maxRecentEvents {
		copy(record.RecentEvents, record.RecentEvents[len(record.RecentEvents)-maxRecentEvents:])
		record.RecentEvents = record.RecentEvents[:maxRecentEvents]
	}
	record.RecentLastSeq = sequence
	first, err := db.ParseU128Decimal(record.RecentEvents[0].Seq)
	if err != nil {
		return ErrInvariant
	}
	record.RecentFirstSeq = first
	return nil
}

func clearEventsForIdentity(record *sessionRecord, seatNo int) error {
	if record == nil || seatNo < 0 || seatNo > 2 {
		return ErrInvariant
	}
	next, err := incU128(record.IdentityEpoch)
	if err != nil {
		return err
	}
	record.IdentityEpoch = next
	record.RecentEvents = nil
	record.RecentFirstSeq = db.U128{}
	record.RecentLastSeq = db.U128{}
	return appendEvent(record, EventIdentityReset, identityResetPayload{SeatNo: seatNo, IdentityEpoch: next.Decimal()})
}

func latestReveal(events []RecentEvent) (revealPayload, bool) {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != EventReveal {
			continue
		}
		var payload revealPayload
		if !rpsDecodeStrictBytes(events[index].SafePayload, &payload) || !validRevealPayload(payload) {
			return revealPayload{}, false
		}
		return payload, true
	}
	return revealPayload{}, false
}

func validRevealPayload(payload revealPayload) bool {
	if len(payload.Gestures) != 3 || !validRevealCode(payload.ResultCode) {
		return false
	}
	seen := [3]bool{}
	for _, value := range payload.Gestures {
		if value.SeatNo < 0 || value.SeatNo > 2 || seen[value.SeatNo] || !validGesture(value.Gesture) {
			return false
		}
		seen[value.SeatNo] = true
	}
	return true
}

func validCanonicalMilli(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.Count(value, ".") > 1 {
		return false
	}
	parts := strings.Split(value, ".")
	if parts[0] == "" || len(parts[0]) > 1 && parts[0][0] == '0' {
		return false
	}
	for index := range parts[0] {
		if parts[0][index] < '0' || parts[0][index] > '9' {
			return false
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) < 1 || len(fraction) > 3 || fraction[len(fraction)-1] == '0' {
			return false
		}
		for index := range fraction {
			if fraction[index] < '0' || fraction[index] > '9' {
				return false
			}
		}
	}
	whole, ok := new(big.Int).SetString(parts[0], 10)
	if !ok {
		return false
	}
	milli := new(big.Int).Mul(whole, bigThousand)
	if fraction != "" {
		padded := fraction + strings.Repeat("0", 3-len(fraction))
		fractionMilli, ok := new(big.Int).SetString(padded, 10)
		if !ok {
			return false
		}
		milli.Add(milli, fractionMilli)
	}
	return milli.Cmp(maxU128) <= 0 && formatMilli(milli) == value
}

func validEventPayload(kind string, raw json.RawMessage) bool {
	switch kind {
	case EventPhaseChanged:
		var payload phaseChangedPayload
		if !rpsDecodeStrictBytes(raw, &payload) || payload.Deadline == nil || *payload.Deadline < 0 || *payload.Deadline > 253402300799 {
			return false
		}
		switch payload.Phase {
		case PhaseGesture, PhaseDealerRaise, PhaseFollowers, PhasePaidPoolGesture, PhaseFreePoolGesture, PhaseUltimateGesture:
			return true
		default:
			return false
		}
	case EventActionLocked:
		var payload actionLockedPayload
		if !rpsDecodeStrictBytes(raw, &payload) || payload.SeatNo < 0 || payload.SeatNo > 2 {
			return false
		}
		return payload.ActionKind == "gesture" || payload.ActionKind == "dealer_decision" || payload.ActionKind == "follower_decision"
	case EventReveal:
		var payload revealPayload
		return rpsDecodeStrictBytes(raw, &payload) && validRevealPayload(payload)
	case EventSettlement:
		var payload settlementPayload
		if !rpsDecodeStrictBytes(raw, &payload) || len(payload.SeatDeltas) != 3 || !validCanonicalMilli(payload.PlayerPool) ||
			!validCanonicalMilli(payload.Cuts.Platform) || !validCanonicalMilli(payload.Cuts.Welfare) || !validCanonicalMilli(payload.Cuts.Thursday) {
			return false
		}
		seen := [3]bool{}
		for _, value := range payload.SeatDeltas {
			if value.SeatNo < 0 || value.SeatNo > 2 || seen[value.SeatNo] || !validCanonicalMilli(value.Delta) {
				return false
			}
			seen[value.SeatNo] = true
		}
		return true
	case EventReminder:
		var payload reminderPayload
		if !rpsDecodeStrictBytes(raw, &payload) {
			return false
		}
		count, err := db.ParseU128Decimal(payload.FreeTieCount)
		return err == nil && count.Big().Cmp(big.NewInt(3)) >= 0 && count.Big().Cmp(big.NewInt(5)) <= 0
	case EventIdentityReset:
		var payload identityResetPayload
		if !rpsDecodeStrictBytes(raw, &payload) || payload.SeatNo < 0 || payload.SeatNo > 2 {
			return false
		}
		epoch, err := db.ParseU128Decimal(payload.IdentityEpoch)
		return err == nil && epoch.Big().Sign() > 0
	case EventTerminal:
		var payload terminalPayload
		return rpsDecodeStrictBytes(raw, &payload) && validTerminalReason(payload.TerminalReason)
	default:
		return false
	}
}

func validRevealCode(value string) bool {
	switch value {
	case RevealThreeEqual, RevealAllDistinct, RevealOneBeatsTwo, RevealTwoBeatOne,
		RevealOneSurrenderTie, RevealOneSurrenderDecided, RevealTwoSurrenders:
		return true
	default:
		return false
	}
}

func addCounter(value db.U128, delta int64) (db.U128, error) {
	if delta < 0 {
		return db.U128{}, ErrInvariant
	}
	return u128(new(big.Int).Add(value.Big(), big.NewInt(delta)))
}
