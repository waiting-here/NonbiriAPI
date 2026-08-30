// Package claim owns the transactional boundary between a frozen routing
// candidate, a recoverable credential, outbound dispatch, and terminal
// request facts.
package claim

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	MaxAttempts                 = 100
	MaxModelSnapshotBytes       = 512
	MaxBaseURLBytes             = 4096
	MaxUpstreamModelRunes       = 512
	MaxDiagnosticBytes          = 4096
	MaxMoneyMilli         int64 = 9_000_000_000_000_000
	MaxRecoveryBatch            = 100
	OrphanSecretTTL             = time.Hour
)

var (
	ErrInvalidInput          = errors.New("claim: invalid input")
	ErrNotFound              = errors.New("claim: resource is unavailable")
	ErrConflict              = errors.New("claim: state conflict")
	ErrAlreadyDispatched     = errors.New("claim: attempt was already dispatched")
	ErrNotDispatched         = errors.New("claim: attempt was not dispatched")
	ErrTerminal              = errors.New("claim: resource is terminal")
	ErrCredentialUnavailable = errors.New("claim: credential is unavailable")
	ErrDependencyUnavailable = errors.New("claim: required domain adapter is unavailable")
	ErrInvariant             = errors.New("claim: persisted invariant is invalid")
)

type Purpose string

const (
	PurposeSelf      Purpose = "self"
	PurposeCharity   Purpose = "charity"
	PurposeDebugLive Purpose = "debug_live"
	PurposeDiscovery Purpose = "discovery"
)

type RouteKind string

const (
	RouteOpenAIChat  RouteKind = "openai_chat_completions"
	RouteCharityChat RouteKind = "charity_chat_completions"
	RouteDiscovery   RouteKind = "model_discovery"
)

type RequestState string

const (
	RequestAccepted RequestState = "accepted"
	RequestRunning  RequestState = "running"
	RequestTerminal RequestState = "terminal"
)

type ClaimState string

const (
	StateClaimed    ClaimState = "claimed"
	StateDispatched ClaimState = "dispatched"
	StateCommitted  ClaimState = "committed"
	StateReleased   ClaimState = "released"
)

type ResultKind string

const (
	ResultResponse  ResultKind = "response"
	ResultSynthetic ResultKind = "synthetic"
)

type ResultClass string

const (
	ResultSuccess   ResultClass = "success"
	ResultFailed    ResultClass = "failed"
	ResultCancelled ResultClass = "cancelled"
)

type AccountingDisposition string

const (
	AccountingNone     AccountingDisposition = "none"
	AccountingReserved AccountingDisposition = "reserved"
	AccountingCommit   AccountingDisposition = "committed"
	AccountingRelease  AccountingDisposition = "released"
)

type RewardState string

const (
	RewardNotApplicable   RewardState = "not_applicable"
	RewardPending         RewardState = "pending"
	RewardPosted          RewardState = "posted"
	RewardZero            RewardState = "zero"
	RewardNotDue          RewardState = "not_due"
	RewardReceiverDeleted RewardState = "receiver_deleted"
)

type SettlementDestination string

const (
	DestinationUser     SettlementDestination = "user"
	DestinationExternal SettlementDestination = "external"
)

// Candidate is a routing snapshot chosen before the claim transaction. Claim
// revalidates its physical endpoint and key identity; it never expands the
// accepted request's attempt limit or chooses a replacement candidate.
type Candidate struct {
	EndpointID       int64
	EndpointKeyID    int64
	ConnectorType    connectorcontract.Type
	CanonicalBaseURL string
	UpstreamModelID  string
	Policy           connectorcontract.AttemptPolicy
}

// AcceptInput creates one economic logical request. Model discovery uses the
// dedicated discovery rail because it has no economic reservation.
type AcceptInput struct {
	UserID         int64
	Route          RouteKind
	ModelSnapshot  string
	AttemptLimit   int
	ReservedMilli  int64
	CharityModelID int64
}

type Request struct {
	ID                    string
	UserID                *int64
	Route                 RouteKind
	ModelSnapshot         string
	State                 RequestState
	AttemptLimit          int
	ResultClass           ResultClass
	CallerStatus          int
	CallerErrorCode       string
	AccountingDisposition AccountingDisposition
	ReservedMilli         int64
	Destination           SettlementDestination
	CreatedAt             int64
	TerminalAt            int64
}

type ClaimInput struct {
	RequestID     string
	ActorUserID   int64
	AttemptSeq    int
	Purpose       Purpose
	Candidate     Candidate
	DonationKeyID int64
}

// Handle is the only value returned by Claim. Its fields are private so a
// repository cannot manufacture a credential lookup from endpoint/key IDs.
// Accessors expose only safe routing metadata needed by the forwarder.
type Handle struct {
	claimID    string
	requestID  string
	attemptSeq int
	purpose    Purpose
	candidate  Candidate
}

func (h Handle) ClaimID() string   { return h.claimID }
func (h Handle) RequestID() string { return h.requestID }
func (h Handle) AttemptSeq() int   { return h.attemptSeq }
func (h Handle) Purpose() Purpose  { return h.purpose }
func (h Handle) Target() connectorcontract.Target {
	return connectorcontract.NewTarget(h.candidate.ConnectorType, h.candidate.CanonicalBaseURL, h.candidate.UpstreamModelID)
}
func (h Handle) Policy() connectorcontract.AttemptPolicy { return h.candidate.Policy }
func (Handle) String() string                            { return "[redacted dispatch claim]" }
func (Handle) GoString() string                          { return "[redacted dispatch claim]" }
func (Handle) LogValue() slog.Value {
	return slog.StringValue("[redacted dispatch claim]")
}

// Dispatch owns the only credential handle released after the dispatched
// state commits. TakeCredential transfers that handle exactly once to one
// connector; Clear is the required backstop for every pre-connector path.
type Dispatch struct {
	handle       Handle
	dispatchedAt int64

	mu         sync.Mutex
	credential *connectorcontract.ShortLivedSecret
	taken      bool
}

func (d *Dispatch) Handle() Handle {
	if d == nil {
		return Handle{}
	}
	return d.handle
}

func (d *Dispatch) Target() connectorcontract.Target {
	if d == nil {
		return connectorcontract.Target{}
	}
	return d.handle.Target()
}

func (d *Dispatch) Policy() connectorcontract.AttemptPolicy {
	if d == nil {
		return connectorcontract.AttemptPolicy{}
	}
	return d.handle.Policy()
}

func (d *Dispatch) DispatchedAt() int64 {
	if d == nil {
		return 0
	}
	return d.dispatchedAt
}

func (d *Dispatch) TakeCredential() (*connectorcontract.ShortLivedSecret, bool) {
	if d == nil {
		return nil, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.taken || d.credential == nil {
		return nil, false
	}
	d.taken = true
	credential := d.credential
	d.credential = nil
	return credential, true
}

func (d *Dispatch) Clear() {
	if d == nil {
		return
	}
	d.mu.Lock()
	credential := d.credential
	d.credential = nil
	d.taken = true
	d.mu.Unlock()
	if credential != nil {
		credential.Clear()
	}
}

func (*Dispatch) String() string   { return "[redacted outbound dispatch]" }
func (*Dispatch) GoString() string { return "[redacted outbound dispatch]" }
func (*Dispatch) LogValue() slog.Value {
	return slog.StringValue("[redacted outbound dispatch]")
}

type AttemptOutcome struct {
	Kind            ResultKind
	UpstreamStatus  int
	UpstreamCode    string
	Diagnostic      string
	Usage           connectorcontract.Usage
	ProtocolSuccess bool
	ResponseStarted bool
}

type Attempt struct {
	ClaimID           string
	RequestID         string
	AttemptSeq        int
	State             ClaimState
	Kind              ResultKind
	UpstreamStatus    int
	UpstreamCode      string
	Diagnostic        string
	Usage             connectorcontract.Usage
	StartedAt         int64
	CompletedAt       int64
	RewardActualMilli int64
	RewardState       RewardState
}

type CallerResult struct {
	Class     ResultClass
	Status    int
	ErrorCode string
}

type CompleteRequestInput struct {
	RequestID         string
	Caller            CallerResult
	Disposition       AccountingDisposition
	ActualChargeMilli int64
}

type DiscoveryClaimInput struct {
	ActorUserID int64
	Candidate   Candidate
}

type RecoveryReport struct {
	ReleasedClaims    int
	CommittedClaims   int
	CompletedRequests int
	MarkedOrphans     int
	DeletedOrphans    int
	More              bool
}

type MaintenanceReport struct {
	Marked  int
	Deleted int
}

// Accounting is the narrow adapter to the central ledger. Every method is
// called inside the claim service's transaction and is scoped to one fixed
// request or claim source identity; it cannot request an arbitrary posting.
//
// The production adapter maps these methods to ledger.Reserve,
// ledger.ReleaseReserved, and ledger.ConsumeReserved. It must invoke each
// supplied persistence callback as the corresponding ledger reservation
// mutation, using the callback's context and transaction. In particular:
//   - ReserveRequest first calls ledger.CheckImmediateCapacity for the current
//     reserve operation plus FutureRows, then reserves FutureRows while
//     persistence creates the request. The route's closed reserve plan is
//     applied in the same outer transaction, where Apply obtains the writer
//     position and linearizes the final current-row capacity check.
//   - ReleaseUndispatched releases one row.
//   - CompleteAttempt consumes one row only for RewardPosted; every other
//     reward state releases one row without a donor-reward operation.
//   - CompleteRequest first releases RemainingRows-1 through releaseUnused
//     when non-nil, then consumes the final row through terminal using the
//     route's closed settlement or release plan.
//
// Callbacks must not be retained or run outside the supplied outer
// transaction. Any adapter or callback error aborts that transaction.
type Accounting interface {
	ReserveRequest(ctx context.Context, tx *sql.Tx, input RequestReservation, persistence DomainPersistence) error
	ReleaseUndispatched(ctx context.Context, tx *sql.Tx, input ClaimAccounting, persistence DomainPersistence) error
	CompleteAttempt(ctx context.Context, tx *sql.Tx, input ClaimAccounting, persistence DomainPersistence) error
	CompleteRequest(ctx context.Context, tx *sql.Tx, input RequestAccounting, releaseUnused, terminal DomainPersistence) error
}

// DomainPersistence has the same transaction-local shape as the ledger
// reservation mutation callback. The callback must use only the supplied
// context and transaction and must not commit or open another transaction.
type DomainPersistence func(context.Context, *sql.Tx) error

type RequestReservation struct {
	RequestID     string
	UserID        int64
	Route         RouteKind
	ReservedMilli int64
	FutureRows    uint16
}

type ClaimAccounting struct {
	RequestID         string
	ClaimID           string
	Purpose           Purpose
	RewardActualMilli int64
	RewardState       RewardState
	ReceiverUserID    *int64
}

type RequestAccounting struct {
	RequestID     string
	UserID        *int64
	Route         RouteKind
	ReservedMilli int64
	ActualMilli   int64
	RemainingRows uint16
	Destination   SettlementDestination
	Disposition   AccountingDisposition
}

// Charity owns donation membership, expiry, three-dimensional reservation,
// price/reward calculation, and streak folding. PrepareAttempt is read-only
// and freezes the actual values inside the current transaction. CompleteAttempt
// persists exactly those prepared values and is called only from the
// Accounting reservation mutation, alongside all B2 terminal facts.
type Charity interface {
	AcceptRequest(context.Context, *sql.Tx, CharityAcceptance) error
	Claim(context.Context, *sql.Tx, CharityClaimInput) (CharityReservation, error)
	ReleaseUndispatched(context.Context, *sql.Tx, CharityRelease) error
	PrepareAttempt(context.Context, *sql.Tx, CharityAttemptInput) (CharityActual, error)
	CompleteAttempt(context.Context, *sql.Tx, CharityAttemptCompletion) error
	CompleteRequest(context.Context, *sql.Tx, CharityRequestCompletion) error
}

type CharityAcceptance struct {
	RequestID      string
	UserID         int64
	CharityModelID int64
	ModelSnapshot  string
	ReservedMilli  int64
	AttemptLimit   int
	AcceptedAt     int64
}

type CharityClaimInput struct {
	RequestID     string
	ClaimID       string
	ActorUserID   int64
	AttemptSeq    int
	DonationKeyID int64
	EndpointID    int64
	EndpointKeyID int64
	ClaimedAt     int64
}

type CharityReservation struct {
	DonationKeyID      int64
	StreakGeneration   int64
	FrozenPriceMilli   int64
	FrozenRewardMilli  int64
	ReceiverUserID     int64
	ReservedPriceMilli int64
	ReservedCalls      int
	ReservedTokens     int64
}

type CharityRelease struct {
	RequestID     string
	ClaimID       string
	DonationKeyID *int64
	ReleasedAt    int64
}

type CharityAttemptInput struct {
	RequestID       string
	ClaimID         string
	DonationKeyID   *int64
	ReceiverUserID  *int64
	SuppressReward  bool
	Usage           connectorcontract.Usage
	ProtocolSuccess bool
	ResponseStarted bool
	UsageUnknown    bool
	CompletedAt     int64
}

type CharityActual struct {
	PriceMilli  int64
	RewardMilli int64
}

type CharityAttemptCompletion struct {
	Attempt     CharityAttemptInput
	Actual      CharityActual
	RewardState RewardState
}

type CharityRequestCompletion struct {
	RequestID   string
	Caller      CallerResult
	Disposition AccountingDisposition
	CompletedAt int64
}

type Dependencies struct {
	DB         *sql.DB
	Secrets    secret.GenerationTwoContextCodec
	Accounting Accounting
	Charity    Charity
	Now        func() time.Time
}
