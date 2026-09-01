package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"
	"unicode"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const maxUnixSecond = int64(253402300799)

type Service struct {
	db         *sql.DB
	secrets    secret.GenerationTwoContextCodec
	accounting Accounting
	charity    Charity
	acceptance AcceptanceGate
	now        func() time.Time
}

func New(dependencies Dependencies) (*Service, error) {
	if dependencies.DB == nil || nilInterface(dependencies.Secrets) {
		return nil, ErrDependencyUnavailable
	}
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	if nilInterface(dependencies.Accounting) {
		dependencies.Accounting = nil
	}
	if nilInterface(dependencies.Charity) {
		dependencies.Charity = nil
	}
	return &Service{
		db:         dependencies.DB,
		secrets:    dependencies.Secrets,
		accounting: dependencies.Accounting,
		charity:    dependencies.Charity,
		acceptance: dependencies.Acceptance,
		now:        dependencies.Now,
	}, nil
}

// Accept freezes one economic logical request and its maximum future ledger
// row count in the same transaction as the domain and accounting adapters.
func (s *Service) Accept(ctx context.Context, input AcceptInput) (Request, error) {
	if s == nil || s.db == nil || ctx == nil || !validAcceptInput(input) {
		return Request{}, ErrInvalidInput
	}
	if s.accounting == nil || s.acceptance == nil || (input.Route == RouteCharityChat && s.charity == nil) {
		return Request{}, ErrDependencyUnavailable
	}
	requestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		return Request{}, fmt.Errorf("claim: generate request identity: %w", err)
	}
	at, err := s.nowUnix()
	if err != nil {
		return Request{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Request{}, fmt.Errorf("claim: begin request acceptance: %w", err)
	}
	defer tx.Rollback()
	if err := requireActiveUser(ctx, tx, input.UserID, at, input.Route == RouteCharityChat); err != nil {
		return Request{}, err
	}
	if err := s.acceptance.AuthorizeChatAcceptance(ctx, tx, input.UserID, at); err != nil {
		return Request{}, fmt.Errorf("claim: authorize chat acceptance: %w", err)
	}
	futureRows := 1
	if input.Route == RouteCharityChat {
		futureRows += input.AttemptLimit
	}
	rows := u128Small(uint64(futureRows))
	persist := func(callbackCtx context.Context, callbackTx *sql.Tx) error {
		if callbackCtx == nil || callbackTx == nil {
			return ErrInvariant
		}
		if _, err := callbackTx.ExecContext(callbackCtx, `INSERT INTO logical_requests(
id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,
account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at)
VALUES(?,?,?,?,'accepted',?,'reserved',?,'user',?,?)`,
			requestID, input.UserID, input.Route, input.ModelSnapshot, input.AttemptLimit,
			input.ReservedMilli, rows, at); err != nil {
			return fmt.Errorf("claim: persist request acceptance: %w", err)
		}
		if err := ensureRequestLogTx(callbackCtx, callbackTx, requestID); err != nil {
			return err
		}
		if input.Route == RouteCharityChat {
			if err := s.charity.AcceptRequest(callbackCtx, callbackTx, CharityAcceptance{
				RequestID:      requestID,
				UserID:         input.UserID,
				CharityModelID: input.CharityModelID,
				ModelSnapshot:  input.ModelSnapshot,
				ReservedMilli:  input.ReservedMilli,
				AttemptLimit:   input.AttemptLimit,
				AcceptedAt:     at,
			}); err != nil {
				return fmt.Errorf("claim: accept charity request: %w", err)
			}
		}
		return nil
	}
	if err := s.accounting.ReserveRequest(ctx, tx, RequestReservation{
		RequestID:     requestID,
		UserID:        input.UserID,
		Route:         input.Route,
		ReservedMilli: input.ReservedMilli,
		FutureRows:    uint16(futureRows),
	}, persist); err != nil {
		return Request{}, fmt.Errorf("claim: reserve request accounting: %w", err)
	}
	var persisted int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM logical_requests WHERE id=?)`, requestID).Scan(&persisted); err != nil {
		return Request{}, fmt.Errorf("claim: verify request acceptance: %w", err)
	}
	if persisted == 0 {
		return Request{}, ErrInvariant
	}
	if err := tx.Commit(); err != nil {
		return Request{}, fmt.Errorf("claim: commit request acceptance: %w", err)
	}
	userID := input.UserID
	return Request{
		ID:                    requestID,
		UserID:                &userID,
		Route:                 input.Route,
		ModelSnapshot:         input.ModelSnapshot,
		State:                 RequestAccepted,
		AttemptLimit:          input.AttemptLimit,
		AccountingDisposition: AccountingReserved,
		ReservedMilli:         input.ReservedMilli,
		Destination:           DestinationUser,
		CreatedAt:             at,
	}, nil
}

// Claim atomically wins one attempt sequence and freezes the physical secret
// reference. It returns no credential bytes.
func (s *Service) Claim(ctx context.Context, input ClaimInput) (Handle, error) {
	if s == nil || s.db == nil || ctx == nil || !validClaimInput(input) {
		return Handle{}, ErrInvalidInput
	}
	claimID, err := db.GenerateOpaqueID("clm_")
	if err != nil {
		return Handle{}, fmt.Errorf("claim: generate claim identity: %w", err)
	}
	at, err := s.nowUnix()
	if err != nil {
		return Handle{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Handle{}, fmt.Errorf("claim: begin claim: %w", err)
	}
	defer tx.Rollback()
	handle, err := s.claimTx(ctx, tx, claimID, at, input)
	if err != nil {
		return Handle{}, err
	}
	if err := tx.Commit(); err != nil {
		return Handle{}, fmt.Errorf("claim: commit claim: %w", err)
	}
	return handle, nil
}

// ClaimDiscovery creates a zero-reservation logical request and its sole
// discovery claim in one transaction. The returned handle is the only path to
// the credential and can be consumed by a registered discoverer.
func (s *Service) ClaimDiscovery(ctx context.Context, input DiscoveryClaimInput) (Request, Handle, error) {
	if s == nil || s.db == nil || ctx == nil || input.ActorUserID <= 0 || !validDiscoveryCandidate(input.Candidate) {
		return Request{}, Handle{}, ErrInvalidInput
	}
	requestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		return Request{}, Handle{}, fmt.Errorf("claim: generate discovery request identity: %w", err)
	}
	claimID, err := db.GenerateOpaqueID("clm_")
	if err != nil {
		return Request{}, Handle{}, fmt.Errorf("claim: generate discovery claim identity: %w", err)
	}
	at, err := s.nowUnix()
	if err != nil {
		return Request{}, Handle{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Request{}, Handle{}, fmt.Errorf("claim: begin discovery claim: %w", err)
	}
	defer tx.Rollback()
	if err := requireActiveUser(ctx, tx, input.ActorUserID, at, false); err != nil {
		return Request{}, Handle{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO logical_requests(
id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,
account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at)
VALUES(?,?,? ,?,'accepted',1,'none',0,'user',?,?)`,
		requestID, input.ActorUserID, RouteDiscovery, "", u128Small(0), at); err != nil {
		return Request{}, Handle{}, fmt.Errorf("claim: persist discovery request: %w", err)
	}
	if err := ensureRequestLogTx(ctx, tx, requestID); err != nil {
		return Request{}, Handle{}, err
	}
	handle, err := s.claimTx(ctx, tx, claimID, at, ClaimInput{
		RequestID:   requestID,
		ActorUserID: input.ActorUserID,
		AttemptSeq:  1,
		Purpose:     PurposeDiscovery,
		Candidate:   input.Candidate,
	})
	if err != nil {
		return Request{}, Handle{}, err
	}
	if err := tx.Commit(); err != nil {
		return Request{}, Handle{}, fmt.Errorf("claim: commit discovery claim: %w", err)
	}
	userID := input.ActorUserID
	request := Request{
		ID:                    requestID,
		UserID:                &userID,
		Route:                 RouteDiscovery,
		ModelSnapshot:         "",
		State:                 RequestRunning,
		AttemptLimit:          1,
		AccountingDisposition: AccountingNone,
		Destination:           DestinationUser,
		CreatedAt:             at,
	}
	return request, handle, nil
}

func (s *Service) claimTx(ctx context.Context, tx *sql.Tx, claimID string, at int64, input ClaimInput) (Handle, error) {
	var (
		requestUserID sql.NullInt64
		routeText     string
		stateText     string
		attemptLimit  int
	)
	if err := tx.QueryRowContext(ctx, `SELECT user_id,route_kind,state,attempt_limit
FROM logical_requests WHERE id=?`, input.RequestID).Scan(&requestUserID, &routeText, &stateText, &attemptLimit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Handle{}, ErrNotFound
		}
		return Handle{}, fmt.Errorf("claim: read logical request: %w", err)
	}
	if !requestUserID.Valid || requestUserID.Int64 != input.ActorUserID {
		return Handle{}, ErrNotFound
	}
	if RequestState(stateText) == RequestTerminal {
		return Handle{}, ErrTerminal
	}
	if input.AttemptSeq > attemptLimit || !purposeMatchesRoute(input.Purpose, RouteKind(routeText)) {
		return Handle{}, ErrConflict
	}
	if err := requireActiveUser(ctx, tx, input.ActorUserID, at, input.Purpose == PurposeCharity); err != nil {
		return Handle{}, err
	}
	var duplicate int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM dispatch_claims WHERE logical_request_id=? AND attempt_seq=?)`,
		input.RequestID, input.AttemptSeq).Scan(&duplicate); err != nil {
		return Handle{}, fmt.Errorf("claim: check attempt identity: %w", err)
	}
	if duplicate != 0 {
		return Handle{}, ErrConflict
	}

	var target targetRow
	var orphanedAt sql.NullInt64
	var suspended int
	if err := tx.QueryRowContext(ctx, `SELECT e.id,e.user_id,e.connector_type,e.base_url,e.enabled,
k.secret_ref_id,k.enabled,k.force_store_false,s.connector_type,s.canonical_base_url,s.orphaned_at,
EXISTS(SELECT 1 FROM endpoint_key_suspensions x WHERE x.endpoint_key_id=k.id)
FROM endpoint_keys k
JOIN endpoints e ON e.id=k.endpoint_id
JOIN endpoint_key_secrets s ON s.id=k.secret_ref_id
WHERE k.id=?`, input.Candidate.EndpointKeyID).Scan(
		&target.endpointID, &target.ownerUserID, &target.endpointConnector, &target.endpointBaseURL,
		&target.endpointEnabled, &target.secretRefID, &target.keyEnabled, &target.forceStoreFalse,
		&target.secretConnector, &target.secretBaseURL, &orphanedAt, &suspended); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Handle{}, ErrNotFound
		}
		return Handle{}, fmt.Errorf("claim: revalidate endpoint key: %w", err)
	}
	if target.endpointID != input.Candidate.EndpointID || target.endpointEnabled != 1 || target.keyEnabled != 1 ||
		orphanedAt.Valid || suspended != 0 || target.endpointConnector != target.secretConnector ||
		target.endpointBaseURL != target.secretBaseURL || target.secretConnector != string(input.Candidate.ConnectorType) ||
		target.secretBaseURL != input.Candidate.CanonicalBaseURL {
		return Handle{}, ErrNotFound
	}
	if input.Purpose != PurposeCharity && target.ownerUserID != input.ActorUserID {
		return Handle{}, ErrNotFound
	}

	reservation := CharityReservation{}
	donationKey := any(nil)
	receiverUser := any(nil)
	streakGeneration := any(nil)
	donorState := RewardNotApplicable
	if input.Purpose == PurposeCharity {
		if s.charity == nil {
			return Handle{}, ErrDependencyUnavailable
		}
		var err error
		reservation, err = s.charity.Claim(ctx, tx, CharityClaimInput{
			RequestID:       input.RequestID,
			ClaimID:         claimID,
			ActorUserID:     input.ActorUserID,
			AttemptSeq:      input.AttemptSeq,
			DonationKeyID:   input.DonationKeyID,
			EndpointID:      target.endpointID,
			EndpointKeyID:   input.Candidate.EndpointKeyID,
			UpstreamModelID: input.Candidate.UpstreamModelID,
			ClaimedAt:       at,
		})
		if err != nil {
			return Handle{}, fmt.Errorf("claim: reserve charity attempt: %w", err)
		}
		if !validCharityReservation(reservation, input.DonationKeyID) {
			return Handle{}, ErrInvariant
		}
		donationKey = reservation.DonationKeyID
		receiverUser = reservation.ReceiverUserID
		streakGeneration = reservation.StreakGeneration
		donorState = RewardPending
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO dispatch_claims(
id,logical_request_id,attempt_seq,purpose,endpoint_key_id,secret_ref_id,donation_key_id,
streak_generation,claim_now,state,frozen_price_milli,frozen_reward_milli,receiver_user_id,
reserved_price_milli,reserved_calls,reserved_tokens,donor_reward_state)
VALUES(?,?,?,?,?,?,?,?,?,'claimed',?,?,?,?,?,?,?)`,
		claimID, input.RequestID, input.AttemptSeq, input.Purpose, input.Candidate.EndpointKeyID,
		target.secretRefID, donationKey, streakGeneration, at, reservation.FrozenPriceMilli,
		reservation.FrozenRewardMilli, receiverUser, reservation.ReservedPriceMilli,
		reservation.ReservedCalls, reservation.ReservedTokens, donorState); err != nil {
		return Handle{}, fmt.Errorf("claim: persist dispatch claim: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE logical_requests SET state='running'
WHERE id=? AND state='accepted'`, input.RequestID); err != nil {
		return Handle{}, fmt.Errorf("claim: mark request running: %w", err)
	}
	if err := ensureRequestLogTx(ctx, tx, input.RequestID); err != nil {
		return Handle{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE request_logs
SET endpoint_key_id=?,upstream_model_id=?,endpoint_base_url=?
WHERE logical_request_id=?`, input.Candidate.EndpointKeyID, input.Candidate.UpstreamModelID,
		target.secretBaseURL, input.RequestID); err != nil {
		return Handle{}, fmt.Errorf("claim: freeze request log target: %w", err)
	}
	candidate := input.Candidate
	candidate.Policy.ForceStoreFalse = target.forceStoreFalse == 1
	return Handle{
		claimID:    claimID,
		requestID:  input.RequestID,
		attemptSeq: input.AttemptSeq,
		purpose:    input.Purpose,
		candidate:  candidate,
	}, nil
}

// TakeForDispatch commits claimed -> dispatched before it decrypts the
// credential. Consequently, no connector can receive a usable credential
// while the database still describes an undispatched attempt.
func (s *Service) TakeForDispatch(ctx context.Context, handle Handle) (*Dispatch, error) {
	if s == nil || s.db == nil || ctx == nil || !validHandle(handle) {
		return nil, ErrInvalidInput
	}
	at, err := s.nowUnix()
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("claim: begin dispatch: %w", err)
	}
	defer tx.Rollback()
	var (
		stateText     string
		requestID     string
		attemptSeq    int
		purposeText   string
		contextID     []byte
		encrypted     []byte
		connectorText string
		baseURL       string
		loggedModel   string
	)
	if err := tx.QueryRowContext(ctx, `SELECT c.state,c.logical_request_id,c.attempt_seq,c.purpose,
s.context_id,s.encrypted_secret,s.connector_type,s.canonical_base_url,
COALESCE(l.upstream_model_id,'')
FROM dispatch_claims c
JOIN endpoint_key_secrets s ON s.id=c.secret_ref_id
LEFT JOIN request_logs l ON l.logical_request_id=c.logical_request_id
WHERE c.id=?`, handle.claimID).Scan(&stateText, &requestID, &attemptSeq, &purposeText,
		&contextID, &encrypted, &connectorText, &baseURL, &loggedModel); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("claim: read dispatch claim: %w", err)
	}
	if requestID != handle.requestID || attemptSeq != handle.attemptSeq || Purpose(purposeText) != handle.purpose ||
		connectorcontract.Type(connectorText) != handle.candidate.ConnectorType ||
		baseURL != handle.candidate.CanonicalBaseURL || loggedModel != handle.candidate.UpstreamModelID {
		clear(contextID)
		clear(encrypted)
		return nil, ErrConflict
	}
	switch ClaimState(stateText) {
	case StateClaimed:
	case StateDispatched:
		clear(contextID)
		clear(encrypted)
		return nil, ErrAlreadyDispatched
	case StateCommitted, StateReleased:
		clear(contextID)
		clear(encrypted)
		return nil, ErrTerminal
	default:
		clear(contextID)
		clear(encrypted)
		return nil, ErrInvariant
	}
	result, err := tx.ExecContext(ctx, `UPDATE dispatch_claims
SET state='dispatched',dispatched_at=? WHERE id=? AND state='claimed'`, at, handle.claimID)
	if err != nil {
		clear(contextID)
		clear(encrypted)
		return nil, fmt.Errorf("claim: persist dispatch marker: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		clear(contextID)
		clear(encrypted)
		if err != nil {
			return nil, fmt.Errorf("claim: verify dispatch marker: %w", err)
		}
		return nil, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		clear(contextID)
		clear(encrypted)
		return nil, fmt.Errorf("claim: commit dispatch marker: %w", err)
	}
	if err := ctx.Err(); err != nil {
		clear(contextID)
		clear(encrypted)
		return nil, fmt.Errorf("%w: %w", ErrCredentialUnavailable, err)
	}
	credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
	clear(contextID)
	if err != nil {
		clear(encrypted)
		return nil, ErrCredentialUnavailable
	}
	plaintext, err := s.secrets.OpenForGenerationTwoContext(string(encrypted), credentialContext)
	if err != nil {
		clear(encrypted)
		clear(plaintext)
		return nil, ErrCredentialUnavailable
	}
	credential := connectorcontract.NewShortLivedSecret(plaintext, encrypted)
	return &Dispatch{
		handle:       handle,
		dispatchedAt: at,
		credential:   credential,
	}, nil
}

type targetRow struct {
	endpointID        int64
	ownerUserID       int64
	endpointConnector string
	endpointBaseURL   string
	endpointEnabled   int
	secretRefID       int64
	keyEnabled        int
	forceStoreFalse   int
	secretConnector   string
	secretBaseURL     string
}

func ensureRequestLogTx(ctx context.Context, tx *sql.Tx, requestID string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO request_logs(
logical_request_id,user_id,model,route_kind,started_at)
SELECT id,user_id,model_snapshot,route_kind,created_at FROM logical_requests WHERE id=?
ON CONFLICT(logical_request_id) DO NOTHING`, requestID); err != nil {
		return fmt.Errorf("claim: ensure request log: %w", err)
	}
	return nil
}

func requireActiveUser(ctx context.Context, tx *sql.Tx, userID, at int64, charity bool) error {
	var active int
	query := `SELECT EXISTS(SELECT 1 FROM users
WHERE id=? AND (is_banned=0 OR (banned_until IS NOT NULL AND banned_until<=?)))`
	args := []any{userID, at}
	if charity {
		query = `SELECT EXISTS(SELECT 1 FROM users
WHERE id=? AND (is_banned=0 OR (banned_until IS NOT NULL AND banned_until<=?))
AND (charity_suspended_until IS NULL OR charity_suspended_until<=?))`
		args = append(args, at)
	}
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&active); err != nil {
		return fmt.Errorf("claim: revalidate account: %w", err)
	}
	if active == 0 {
		return ErrNotFound
	}
	return nil
}

func validAcceptInput(input AcceptInput) bool {
	if input.UserID <= 0 || input.AttemptLimit < 1 || input.AttemptLimit > MaxAttempts ||
		input.ReservedMilli < 0 || input.ReservedMilli > MaxMoneyMilli ||
		!validBoundedText(input.ModelSnapshot, MaxModelSnapshotBytes, true) {
		return false
	}
	switch input.Route {
	case RouteOpenAIChat:
		return input.CharityModelID == 0
	case RouteCharityChat:
		return input.CharityModelID > 0
	default:
		return false
	}
}

func validClaimInput(input ClaimInput) bool {
	if !db.ValidateOpaqueID(input.RequestID, "req_") || input.ActorUserID <= 0 ||
		input.AttemptSeq < 1 || input.AttemptSeq > MaxAttempts || !validCandidate(input.Candidate) {
		return false
	}
	switch input.Purpose {
	case PurposeSelf, PurposeDebugLive:
		return input.DonationKeyID == 0
	case PurposeCharity:
		return input.DonationKeyID > 0
	default:
		return false
	}
}

func validCandidate(candidate Candidate) bool {
	return validCandidateIdentity(candidate) && validModelID(candidate.UpstreamModelID)
}

func validDiscoveryCandidate(candidate Candidate) bool {
	return validCandidateIdentity(candidate) && candidate.UpstreamModelID == ""
}

func validCandidateIdentity(candidate Candidate) bool {
	if candidate.EndpointID <= 0 || candidate.EndpointKeyID <= 0 ||
		len(candidate.CanonicalBaseURL) < 1 || len(candidate.CanonicalBaseURL) > MaxBaseURLBytes {
		return false
	}
	switch candidate.ConnectorType {
	case connectorcontract.TypeOpenAICompatible, connectorcontract.TypeAnthropicCompatible:
		return true
	default:
		return false
	}
}

func validHandle(handle Handle) bool {
	if !db.ValidateOpaqueID(handle.claimID, "clm_") || !db.ValidateOpaqueID(handle.requestID, "req_") ||
		handle.attemptSeq < 1 || handle.attemptSeq > MaxAttempts {
		return false
	}
	if handle.purpose == PurposeDiscovery {
		return validDiscoveryCandidate(handle.candidate)
	}
	return (handle.purpose == PurposeSelf || handle.purpose == PurposeCharity || handle.purpose == PurposeDebugLive) &&
		validCandidate(handle.candidate)
}

func purposeMatchesRoute(purpose Purpose, route RouteKind) bool {
	switch purpose {
	case PurposeSelf, PurposeDebugLive:
		return route == RouteOpenAIChat
	case PurposeCharity:
		return route == RouteCharityChat
	case PurposeDiscovery:
		return route == RouteDiscovery
	default:
		return false
	}
}

func validCharityReservation(value CharityReservation, expectedDonationKeyID int64) bool {
	return value.DonationKeyID == expectedDonationKeyID && value.DonationKeyID > 0 &&
		value.StreakGeneration > 0 && value.ReceiverUserID > 0 &&
		validMoney(value.FrozenPriceMilli) && validMoney(value.FrozenRewardMilli) &&
		validMoney(value.ReservedPriceMilli) && value.ReservedCalls >= 0 && value.ReservedCalls <= 1 &&
		value.ReservedTokens >= 0 && value.ReservedTokens <= 2147483647
}

func validMoney(value int64) bool { return value >= 0 && value <= MaxMoneyMilli }

func validModelID(value string) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > MaxUpstreamModelRunes {
		return false
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x7f {
			return false
		}
	}
	return true
}

func validBoundedText(value string, maxBytes int, allowEmpty bool) bool {
	return utf8.ValidString(value) && len(value) <= maxBytes && (allowEmpty || value != "")
}

func u128Small(value uint64) []byte {
	out := make([]byte, 16)
	for index := len(out) - 1; index >= 0 && value != 0; index-- {
		out[index] = byte(value)
		value >>= 8
	}
	return out
}

func (s *Service) nowUnix() (int64, error) {
	if s == nil || s.now == nil {
		return 0, ErrDependencyUnavailable
	}
	at := s.now().Unix()
	if at < 0 || at > maxUnixSecond {
		return 0, ErrInvalidInput
	}
	return at, nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
