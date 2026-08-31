package ledger

import (
	"math/big"
	"sort"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type dynamicEntries uint8

const (
	dynamicNone dynamicEntries = iota
	dynamicAccountDelete
)

type entrySpec struct {
	role  accountRole
	delta Amount
}

type donationChange struct {
	userID    int64
	accountID int64
	delta     Amount
}

type capacityPlan struct {
	consume    *ReservationRef
	consumeAll bool
	releaseAll []ReservationRef
	reserve    *futureReservation
}

type futureReservation struct {
	ref  ReservationRef
	rows db.U128
}

type planSpec struct {
	meta               Meta
	kind               Kind
	sourceType         sourceType
	sourceID           string
	sourceSeq          db.U128
	reason             string
	entries            []entrySpec
	dynamic            dynamicEntries
	donation           *donationChange
	requireNonnegative map[int64]struct{}
	requireZeroAfter   map[int64]struct{}
	capacity           capacityPlan
}

// Plan is immutable outside this package. Its zero value is invalid, and no
// public field permits an arbitrary kind, source, reason or posting template.
type Plan struct {
	spec *planSpec
}

// SettlementDestination is a closed user-or-external destination used by
// accepted request settlement after deletion handoff.
type SettlementDestination struct {
	role accountRole
}

func UserSettlementDestination(accountID int64) (SettlementDestination, error) {
	if accountID <= 0 {
		return SettlementDestination{}, ErrInvalidPlan
	}
	return SettlementDestination{role: userRole(accountID)}, nil
}

func ExternalSettlementDestination(accountID int64) (SettlementDestination, error) {
	if accountID <= 0 {
		return SettlementDestination{}, ErrInvalidPlan
	}
	return SettlementDestination{role: externalRole(accountID)}, nil
}

func newPlan(meta Meta, kind Kind, typ sourceType, sourceID string, sourceSeq db.U128) (Plan, error) {
	if !db.ValidateOpaqueID(meta.OperationID, "op_") || meta.ActorUserID < 0 || !validUnix(meta.CreatedAt) {
		return Plan{}, ErrInvalidPlan
	}
	prefix := map[sourceType]string{
		sourceOperation:       "op_",
		sourceLogicalRequest:  "req_",
		sourceDispatchClaim:   "clm_",
		sourcePeriod:          "thu_",
		sourceFishingBatch:    "fb_",
		sourceLinkLinkSession: "ll_",
		sourceRPSQueue:        "rpsq_",
		sourceRPSSession:      "rps_",
	}[typ]
	if prefix == "" || !db.ValidateOpaqueID(sourceID, prefix) || typ == sourceOperation && sourceID != meta.OperationID {
		return Plan{}, ErrInvalidPlan
	}
	wantType, ok := sourceTypeForKind(kind)
	if !ok || wantType != typ {
		return Plan{}, ErrInvalidPlan
	}
	if kind == KindRPSRoundCut {
		if sourceSeq.Big().Sign() <= 0 {
			return Plan{}, ErrInvalidPlan
		}
	} else if sourceSeq.Big().Sign() != 0 {
		return Plan{}, ErrInvalidPlan
	}
	return Plan{spec: &planSpec{
		meta:               meta,
		kind:               kind,
		sourceType:         typ,
		sourceID:           sourceID,
		sourceSeq:          sourceSeq,
		requireNonnegative: make(map[int64]struct{}),
		requireZeroAfter:   make(map[int64]struct{}),
	}}, nil
}

func sourceTypeForKind(kind Kind) (sourceType, bool) {
	switch kind {
	case KindAdminUserAdjustment, KindAdminPoolAdjustment, KindAccountDeleteZero,
		KindCheckinAward, KindAntiAbusePenalty, KindWelfareClaim,
		KindThursdayContribution, KindThursdayPayout:
		return sourceOperation, true
	case KindForwardReserve, KindForwardSettle, KindForwardRelease,
		KindCharityReserve, KindCharitySettle, KindCharityRelease:
		return sourceLogicalRequest, true
	case KindDonorReward:
		return sourceDispatchClaim, true
	case KindThursdayFinalize:
		return sourcePeriod, true
	case KindFishingReserve, KindFishingSettle, KindFishingRelease:
		return sourceFishingBatch, true
	case KindLinkLinkEntry:
		return sourceLinkLinkSession, true
	case KindRPSQueueReserve, KindRPSQueueRelease:
		return sourceRPSQueue, true
	case KindRPSSessionStart, KindRPSRoundCut, KindRPSTerminal:
		return sourceRPSSession, true
	default:
		return "", false
	}
}

func (p Plan) add(role accountRole, delta Amount) Plan {
	p.spec.entries = append(p.spec.entries, entrySpec{role: role, delta: delta})
	return p
}

func (p Plan) requireAvailable(accountID int64) Plan {
	p.spec.requireNonnegative[accountID] = struct{}{}
	return p
}

func (p Plan) requireZero(accountID int64) Plan {
	p.spec.requireZeroAfter[accountID] = struct{}{}
	return p
}

func (p Plan) consume(ref ReservationRef) Plan {
	copyRef := ref
	p.spec.capacity.consume = &copyRef
	return p
}

func validReason(reason string) bool {
	return reason != "" && utf8.ValidString(reason) && utf8.RuneCountInString(reason) <= 1024 && len(reason) <= 4096
}

func validPrimitive(value Amount) bool {
	absolute := new(big.Int).Abs(value.Big())
	return absolute.Cmp(big.NewInt(db.MaxMoneyMilli)) <= 0
}

func validNonnegativePrimitive(value Amount) bool {
	return nonnegative(value) && validPrimitive(value)
}

func operationPlan(meta Meta, kind Kind) (Plan, error) {
	return newPlan(meta, kind, sourceOperation, meta.OperationID, db.U128{})
}

func logicalPlan(meta Meta, kind Kind, requestID string) (Plan, error) {
	return newPlan(meta, kind, sourceLogicalRequest, requestID, db.U128{})
}

func fishingPlan(meta Meta, kind Kind, batchID string) (Plan, error) {
	return newPlan(meta, kind, sourceFishingBatch, batchID, db.U128{})
}

// RPSQueueInput is one frozen queue reserve consumed by session start.
type RPSQueueInput struct {
	QueueID   string
	AccountID int64
	Amount    Amount
}

func sortedQueueInputs(inputs [3]RPSQueueInput) ([3]RPSQueueInput, error) {
	sort.Slice(inputs[:], func(i, j int) bool { return inputs[i].QueueID < inputs[j].QueueID })
	seenAccounts := make(map[int64]struct{}, 3)
	for i, input := range inputs {
		if !db.ValidateOpaqueID(input.QueueID, "rpsq_") || input.AccountID <= 0 || !positive(input.Amount) {
			return [3]RPSQueueInput{}, ErrInvalidPlan
		}
		if i > 0 && inputs[i-1].QueueID == input.QueueID {
			return [3]RPSQueueInput{}, ErrInvalidPlan
		}
		if _, exists := seenAccounts[input.AccountID]; exists {
			return [3]RPSQueueInput{}, ErrInvalidPlan
		}
		seenAccounts[input.AccountID] = struct{}{}
	}
	return inputs, nil
}
