package ledger

import (
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

var (
	ErrInvalidAmount       = errors.New("ledger: invalid amount")
	ErrInvalidPlan         = errors.New("ledger: invalid plan")
	ErrInvalidReservation  = errors.New("ledger: invalid reservation")
	ErrNotFound            = errors.New("ledger: not found")
	ErrConflict            = errors.New("ledger: conflict")
	ErrInsufficientBalance = errors.New("ledger: insufficient balance")
	ErrCapacityExhausted   = errors.New("ledger: capacity exhausted")
	ErrInvariant           = errors.New("ledger: invariant violation")
	ErrRetryable           = errors.New("ledger: retryable database error")
)

// Kind is the closed credit_operations.kind set.
type Kind string

const (
	KindAdminUserAdjustment  Kind = "admin_user_adjustment"
	KindAdminPoolAdjustment  Kind = "admin_pool_adjustment"
	KindAccountDeleteZero    Kind = "account_delete_zero"
	KindCheckinAward         Kind = "checkin_award"
	KindAntiAbusePenalty     Kind = "anti_abuse_penalty"
	KindWelfareClaim         Kind = "welfare_claim"
	KindThursdayContribution Kind = "thursday_contribution"
	KindThursdayPayout       Kind = "thursday_payout"
	KindForwardReserve       Kind = "forward_reserve"
	KindForwardSettle        Kind = "forward_settle"
	KindForwardRelease       Kind = "forward_release"
	KindCharityReserve       Kind = "charity_reserve"
	KindCharitySettle        Kind = "charity_settle"
	KindCharityRelease       Kind = "charity_release"
	KindDonorReward          Kind = "donor_reward"
	KindThursdayFinalize     Kind = "thursday_finalize"
	KindFishingReserve       Kind = "fishing_reserve"
	KindFishingSettle        Kind = "fishing_settle"
	KindFishingRelease       Kind = "fishing_release"
	KindLinkLinkEntry        Kind = "linklink_entry"
	KindRPSQueueReserve      Kind = "rps_queue_reserve"
	KindRPSQueueRelease      Kind = "rps_queue_release"
	KindRPSSessionStart      Kind = "rps_session_start"
	KindRPSRoundCut          Kind = "rps_round_cut"
	KindRPSTerminal          Kind = "rps_terminal"
)

type sourceType string

const (
	sourceOperation       sourceType = "operation"
	sourceLogicalRequest  sourceType = "logical_request"
	sourceDispatchClaim   sourceType = "dispatch_claim"
	sourcePeriod          sourceType = "period"
	sourceFishingBatch    sourceType = "fishing_batch"
	sourceLinkLinkSession sourceType = "linklink_session"
	sourceRPSQueue        sourceType = "rps_queue"
	sourceRPSSession      sourceType = "rps_session"
)

// AccountKind is a persisted account classification. Account creation is
// exposed only through the closed constructors in accounts.go.
type AccountKind string

const (
	AccountUser     AccountKind = "user"
	AccountPool     AccountKind = "pool"
	AccountPlatform AccountKind = "platform"
	AccountExternal AccountKind = "external"
)

// Account is an authoritative account snapshot read inside the caller's
// transaction. UserID is zero and Code is empty when not applicable.
type Account struct {
	ID        int64
	Kind      AccountKind
	UserID    int64
	Code      string
	Balance   Amount
	CreatedAt int64
	UpdatedAt int64
}

// Meta identifies one immutable ledger operation. ActorUserID zero means a
// system operation; OperationID must be a canonical op_ identifier.
type Meta struct {
	OperationID string
	ActorUserID int64
	CreatedAt   int64
}

// Entry is one immutable result line. AccountID is zero after a user account
// has been deleted and its FK was set to NULL.
type Entry struct {
	LineNo       int
	AccountID    int64
	AccountKind  AccountKind
	Delta        Amount
	BalanceAfter *Amount
}

// Result is the authoritative persisted operation. A source replay may
// return an earlier operation ID than the one supplied in the replay plan.
type Result struct {
	OperationID          string
	LedgerSeq            int64
	Kind                 Kind
	SourceType           string
	SourceID             string
	SourceSeq            db.U128
	ActorUserID          int64
	DonationCreditUserID int64
	DonationCreditDelta  Amount
	DonationCreditAfter  *db.U128
	Reason               string
	CreatedAt            int64
	Entries              []Entry
}

// Capacity is the singleton future-row allocation snapshot.
type Capacity struct {
	LastLedgerSeq      int64
	ReservedFutureRows db.U128
	Revision           db.U128
}

type reservationKind uint8

const (
	reservationLogicalRequest reservationKind = iota + 1
	reservationFishingBatch
	reservationThursdayPeriod
	reservationThursdayParticipant
	reservationRPSQueue
	reservationRPSSession
)

// ReservationRef is an unforgeable reference to one frozen domain remaining
// column. Use the constructors below; the zero value is invalid.
type ReservationRef struct {
	kind     reservationKind
	id       string
	parentID string
}

func LogicalRequestReservation(id string) (ReservationRef, error) {
	return opaqueReservation(reservationLogicalRequest, id, "req_")
}

func FishingReservation(id string) (ReservationRef, error) {
	return opaqueReservation(reservationFishingBatch, id, "fb_")
}

func ThursdayPeriodReservation(id string) (ReservationRef, error) {
	return opaqueReservation(reservationThursdayPeriod, id, "thu_")
}

func ThursdayParticipantReservation(periodID, participantID string) (ReservationRef, error) {
	if !db.ValidateOpaqueID(periodID, "thu_") || !db.ValidateOpaqueID(participantID, "thp_") {
		return ReservationRef{}, ErrInvalidReservation
	}
	return ReservationRef{kind: reservationThursdayParticipant, id: participantID, parentID: periodID}, nil
}

func RPSQueueReservation(id string) (ReservationRef, error) {
	return opaqueReservation(reservationRPSQueue, id, "rpsq_")
}

func RPSSessionReservation(id string) (ReservationRef, error) {
	return opaqueReservation(reservationRPSSession, id, "rps_")
}

func opaqueReservation(kind reservationKind, id, prefix string) (ReservationRef, error) {
	if !db.ValidateOpaqueID(id, prefix) {
		return ReservationRef{}, ErrInvalidReservation
	}
	return ReservationRef{kind: kind, id: id}, nil
}

func (r ReservationRef) equal(other ReservationRef) bool {
	return r.kind == other.kind && r.id == other.id && r.parentID == other.parentID
}
