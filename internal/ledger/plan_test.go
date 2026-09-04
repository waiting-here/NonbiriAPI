package ledger

import (
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestEveryKindHasClosedConstructorAndTemplate(t *testing.T) {
	destination, err := UserSettlementDestination(8)
	if err != nil {
		t.Fatal(err)
	}
	requestID := mustLedgerID(t, "req_")
	batchID := mustLedgerID(t, "fb_")
	periodID := mustLedgerID(t, "thu_")
	participantID := mustLedgerID(t, "thp_")
	queueID := mustLedgerID(t, "rpsq_")
	sessionID := mustLedgerID(t, "rps_")
	claimID := mustLedgerID(t, "clm_")
	linkID := mustLedgerID(t, "ll_")
	one := AmountFromMilli(1)
	cutSeq := mustU128(t, "1")

	meta := func() Meta {
		return Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: 1, CreatedAt: ledgerTestNow}
	}
	builds := []struct {
		kind       Kind
		sourceType sourceType
		entries    int
		build      func() (Plan, error)
	}{
		{KindAdminUserAdjustment, sourceOperation, 2, func() (Plan, error) { return NewAdminUserAdjustment(meta(), 1, 2, one, 1, Amount{}, "reason") }},
		{KindAdminPoolAdjustment, sourceOperation, 2, func() (Plan, error) { return NewAdminPoolAdjustment(meta(), 3, 2, one, "reason") }},
		{KindAccountDeleteZero, sourceOperation, 2, func() (Plan, error) { return NewAccountDeleteZero(meta(), 1, 2) }},
		{KindCheckinAward, sourceOperation, 2, func() (Plan, error) { return NewCheckinAward(meta(), 1, 2, one) }},
		{KindAntiAbusePenalty, sourceOperation, 2, func() (Plan, error) { return NewAntiAbusePenalty(meta(), 1, 2, one, "reason") }},
		{KindWelfareClaim, sourceOperation, 2, func() (Plan, error) { return NewWelfareClaim(meta(), 3, 1, one) }},
		{KindThursdayContribution, sourceOperation, 2, func() (Plan, error) { return NewThursdayContribution(meta(), 1, 3, one) }},
		{KindThursdayPayout, sourceOperation, 2, func() (Plan, error) { return NewThursdayPayout(meta(), periodID, participantID, 3, 1, one) }},
		{KindForwardReserve, sourceLogicalRequest, 2, func() (Plan, error) { return NewForwardReserve(meta(), requestID, 1, 4, one) }},
		{KindForwardSettle, sourceLogicalRequest, 3, func() (Plan, error) { return NewForwardSettle(meta(), requestID, 4, 5, destination, one, one) }},
		{KindForwardRelease, sourceLogicalRequest, 2, func() (Plan, error) { return NewForwardRelease(meta(), requestID, 4, 1, one) }},
		{KindCharityReserve, sourceLogicalRequest, 2, func() (Plan, error) { return NewCharityReserve(meta(), requestID, 1, 6, one) }},
		{KindCharitySettle, sourceLogicalRequest, 3, func() (Plan, error) { return NewCharitySettle(meta(), requestID, 6, 5, destination, one, one) }},
		{KindCharityRelease, sourceLogicalRequest, 2, func() (Plan, error) { return NewCharityRelease(meta(), requestID, 6, 1, one) }},
		{KindDonorReward, sourceDispatchClaim, 2, func() (Plan, error) { return NewDonorReward(meta(), requestID, claimID, 2, 1, 1, one) }},
		{KindThursdayFinalize, sourcePeriod, 4, func() (Plan, error) {
			return NewThursdayFinalize(meta(), periodID, 3, 5, 6, 7, ThursdayFinalizeAmounts{Total: one, Platform: one})
		}},
		{KindFishingReserve, sourceFishingBatch, 2, func() (Plan, error) { return NewFishingReserve(meta(), batchID, 1, 4, one) }},
		{KindFishingSettle, sourceFishingBatch, 4, func() (Plan, error) { return NewFishingSettle(meta(), batchID, 4, 5, 2, 1, one, one) }},
		{KindFishingRelease, sourceFishingBatch, 2, func() (Plan, error) { return NewFishingRelease(meta(), batchID, 4, 1, one) }},
		{KindLinkLinkEntry, sourceLinkLinkSession, 2, func() (Plan, error) { return NewLinkLinkEntry(meta(), linkID, 1, 5, one) }},
		{KindRPSQueueReserve, sourceRPSQueue, 2, func() (Plan, error) { return NewRPSQueueReserve(meta(), queueID, 1, 4, one) }},
		{KindRPSQueueRelease, sourceRPSQueue, 2, func() (Plan, error) { return NewRPSQueueRelease(meta(), queueID, 4, 1, one) }},
		{KindRPSSessionStart, sourceRPSSession, 4, func() (Plan, error) {
			return NewRPSSessionStart(meta(), sessionID, 9, mustU128(t, "9"), [3]RPSQueueInput{
				{QueueID: mustLedgerID(t, "rpsq_"), AccountID: 4, Amount: one},
				{QueueID: mustLedgerID(t, "rpsq_"), AccountID: 5, Amount: one},
				{QueueID: mustLedgerID(t, "rpsq_"), AccountID: 6, Amount: one},
			})
		}},
		{KindRPSRoundCut, sourceRPSSession, 4, func() (Plan, error) {
			return NewRPSRoundCut(meta(), sessionID, cutSeq, 9, 5, 6, 7, RPSCutAmounts{Platform: one})
		}},
		{KindRPSTerminal, sourceRPSSession, 3, func() (Plan, error) {
			return NewRPSTerminal(meta(), sessionID, 9, 2, 6, []RPSTerminalPayout{{UserAccountID: 1, Amount: one}}, Amount{}, Amount{})
		}},
	}

	seen := make(map[Kind]bool, len(builds))
	for _, test := range builds {
		t.Run(string(test.kind), func(t *testing.T) {
			plan, err := test.build()
			if err != nil {
				t.Fatalf("constructor: %v", err)
			}
			if err := validatePlan(plan); err != nil {
				t.Fatalf("validate plan: %v", err)
			}
			if plan.spec.kind != test.kind || plan.spec.sourceType != test.sourceType || len(plan.spec.entries) != test.entries {
				t.Fatalf("shape = kind %s source %s entries %d", plan.spec.kind, plan.spec.sourceType, len(plan.spec.entries))
			}
			wantZeroAfter := map[Kind]int{
				KindThursdayFinalize: 1,
				KindRPSQueueRelease:  1,
				KindRPSSessionStart:  3,
				KindRPSTerminal:      1,
			}[test.kind]
			if len(plan.spec.requireZeroAfter) != wantZeroAfter {
				t.Fatalf("zero-after accounts = %d, want %d", len(plan.spec.requireZeroAfter), wantZeroAfter)
			}
			if got := plan.spec.capacity.consumeAll; got != (test.kind == KindRPSTerminal) {
				t.Fatalf("consume-all = %v", got)
			}
			total := new(big.Int)
			for _, entry := range plan.spec.entries {
				total.Add(total, entry.delta.Big())
			}
			if total.Sign() != 0 {
				t.Fatalf("template does not conserve: %s", total)
			}
			wantRoles := map[Kind][]string{
				KindAdminUserAdjustment:  {"X", "U"},
				KindAdminPoolAdjustment:  {"X", "POOL"},
				KindAccountDeleteZero:    {"U", "X"},
				KindCheckinAward:         {"X", "U"},
				KindAntiAbusePenalty:     {"U", "X"},
				KindWelfareClaim:         {"POOL", "U"},
				KindThursdayContribution: {"U", "POOL"},
				KindThursdayPayout:       {"POOL", "U"},
				KindForwardReserve:       {"U", "Hf"},
				KindForwardSettle:        {"Hf", "U", "P"},
				KindForwardRelease:       {"Hf", "U"},
				KindCharityReserve:       {"U", "Hc"},
				KindCharitySettle:        {"Hc", "U", "P"},
				KindCharityRelease:       {"Hc", "U"},
				KindDonorReward:          {"X", "U"},
				KindThursdayFinalize:     {"POOL", "P", "POOL", "POOL"},
				KindFishingReserve:       {"U", "F"},
				KindFishingSettle:        {"F", "P", "X", "U"},
				KindFishingRelease:       {"F", "U"},
				KindLinkLinkEntry:        {"U", "P"},
				KindRPSQueueReserve:      {"U", "Qi"},
				KindRPSQueueRelease:      {"Qi", "U"},
				KindRPSSessionStart:      {"Qi", "Qi", "Qi", "S"},
				KindRPSRoundCut:          {"S", "P", "POOL", "POOL"},
				KindRPSTerminal:          {"S", "U", "POOL"},
			}[test.kind]
			gotRoles := make([]string, len(plan.spec.entries))
			for i, entry := range plan.spec.entries {
				gotRoles[i] = testRole(entry.role)
			}
			if !reflect.DeepEqual(gotRoles, wantRoles) {
				t.Fatalf("account template roles = %v, want %v", gotRoles, wantRoles)
			}
			wantDeltas := map[Kind][]string{
				KindAdminUserAdjustment:  {"-1", "1"},
				KindAdminPoolAdjustment:  {"-1", "1"},
				KindAccountDeleteZero:    {"0", "0"},
				KindCheckinAward:         {"-1", "1"},
				KindAntiAbusePenalty:     {"-1", "1"},
				KindWelfareClaim:         {"-1", "1"},
				KindThursdayContribution: {"-1", "1"},
				KindThursdayPayout:       {"-1", "1"},
				KindForwardReserve:       {"-1", "1"},
				KindForwardSettle:        {"-1", "0", "1"},
				KindForwardRelease:       {"-1", "1"},
				KindCharityReserve:       {"-1", "1"},
				KindCharitySettle:        {"-1", "0", "1"},
				KindCharityRelease:       {"-1", "1"},
				KindDonorReward:          {"-1", "1"},
				KindThursdayFinalize:     {"-1", "1", "0", "0"},
				KindFishingReserve:       {"-1", "1"},
				KindFishingSettle:        {"-1", "1", "-1", "1"},
				KindFishingRelease:       {"-1", "1"},
				KindLinkLinkEntry:        {"-1", "1"},
				KindRPSQueueReserve:      {"-1", "1"},
				KindRPSQueueRelease:      {"-1", "1"},
				KindRPSSessionStart:      {"-1", "-1", "-1", "3"},
				KindRPSRoundCut:          {"-1", "1", "0", "0"},
				KindRPSTerminal:          {"-1", "1", "0"},
			}[test.kind]
			gotDeltas := make([]string, len(plan.spec.entries))
			for i, entry := range plan.spec.entries {
				gotDeltas[i] = entry.delta.Decimal()
			}
			if !reflect.DeepEqual(gotDeltas, wantDeltas) {
				t.Fatalf("account template deltas = %v, want %v", gotDeltas, wantDeltas)
			}
			seen[test.kind] = true
		})
	}
	if len(seen) != 25 {
		t.Fatalf("covered %d kinds, want 25", len(seen))
	}
}

func testRole(role accountRole) string {
	switch role.kind {
	case AccountUser:
		return "U"
	case AccountExternal:
		return "X"
	case AccountPool:
		return "POOL"
	case AccountPlatform:
		switch role.code {
		case "platform":
			return "P"
		case "forward_reserve":
			return "Hf"
		case "charity_reserve":
			return "Hc"
		case "game_fishing_reserve":
			return "F"
		default:
			if strings.HasPrefix(role.code, "rps-queue:") {
				return "Qi"
			}
			if strings.HasPrefix(role.code, "rps-session:") {
				return "S"
			}
		}
	}
	return "?"
}

func TestAmountSM128BoundariesAndCanonicalParsing(t *testing.T) {
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	for _, value := range []*big.Int{new(big.Int), big.NewInt(1), big.NewInt(-1), max, new(big.Int).Neg(max)} {
		amount, err := AmountFromBig(value)
		if err != nil || amount.Big().Cmp(value) != 0 {
			t.Fatalf("AmountFromBig(%s) = (%s,%v)", value, amount.Decimal(), err)
		}
	}
	tooWide := new(big.Int).Lsh(big.NewInt(1), 127)
	if _, err := AmountFromBig(tooWide); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("2^127 error = %v", err)
	}
	for _, value := range []string{"", "+1", "01", "-0", " 1", "1e3"} {
		if _, err := ParseAmount(value); !errors.Is(err, ErrInvalidAmount) {
			t.Fatalf("ParseAmount(%q) error = %v", value, err)
		}
	}
	if _, err := db.NewSM128(1, append([]byte{0x80}, make([]byte, 15)...)); err == nil {
		t.Fatal("shared codec accepted reserved SM128 high bit")
	}
}

func TestAmountArithmeticConservationProperty(t *testing.T) {
	property := func(leftRaw, rightRaw int64) bool {
		left := AmountFromMilli(leftRaw)
		right := AmountFromMilli(rightRaw)
		sum, err := addAmounts(left, right)
		if err != nil {
			return false
		}
		restored, err := subtractAmounts(sum, right)
		if err != nil || restored.Big().Cmp(left.Big()) != 0 {
			return false
		}
		zero, err := addAmounts(left, negate(left))
		return err == nil && zero.IsZero()
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 10_000}); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRejectsOpenEndedInputs(t *testing.T) {
	if err := validatePlan(Plan{}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("zero plan = %v", err)
	}
	meta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: 1, CreatedAt: ledgerTestNow}
	tooLarge := mustAmount(t, "9000000000000001")
	if _, err := NewCheckinAward(meta, 1, 2, tooLarge); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("oversized primitive = %v", err)
	}
	if _, err := NewAdminPoolAdjustment(meta, 1, 2, Amount{}, "reason"); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("zero pool adjustment = %v", err)
	}
	if _, err := NewAdminUserAdjustment(meta, 1, 2, Amount{}, 0, AmountFromMilli(1), "reason"); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("unbound donation change = %v", err)
	}
}

func TestRPSAggregateConstructorsAcceptAbovePrimitiveLimit(t *testing.T) {
	meta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: 1, CreatedAt: ledgerTestNow}
	aggregate := mustAmount(t, "9000000000000001")
	queueID := mustLedgerID(t, "rpsq_")
	if plan, err := NewRPSQueueReserve(meta, queueID, 1, 2, aggregate); err != nil {
		t.Fatalf("aggregate queue reserve: %v", err)
	} else if err := validatePlan(plan); err != nil {
		t.Fatalf("validate aggregate queue reserve: %v", err)
	}
	if plan, err := NewRPSQueueRelease(meta, queueID, 2, 1, aggregate); err != nil {
		t.Fatalf("aggregate queue release: %v", err)
	} else if err := validatePlan(plan); err != nil {
		t.Fatalf("validate aggregate queue release: %v", err)
	}
	inputs := [3]RPSQueueInput{}
	for i := range inputs {
		inputs[i] = RPSQueueInput{
			QueueID: mustLedgerID(t, "rpsq_"), AccountID: int64(i + 3), Amount: aggregate,
		}
	}
	plan, err := NewRPSSessionStart(meta, mustLedgerID(t, "rps_"), 6, mustU128(t, "1"), inputs)
	if err != nil {
		t.Fatalf("aggregate session start: %v", err)
	}
	if err := validatePlan(plan); err != nil {
		t.Fatalf("validate aggregate session start: %v", err)
	}
	if got := plan.spec.entries[len(plan.spec.entries)-1].delta.Decimal(); got != "27000000000000003" {
		t.Fatalf("aggregate session amount = %s", got)
	}
}
