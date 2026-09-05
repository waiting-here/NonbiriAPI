package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// ReleaseUndispatched terminalizes a claim that never crossed the dispatch
// marker. It releases only this claim's charity reservation/capacity and never
// creates a request_attempt row.
func (s *Service) ReleaseUndispatched(ctx context.Context, handle Handle) (Attempt, error) {
	if s == nil || s.db == nil || ctx == nil || !validHandle(handle) {
		return Attempt{}, ErrInvalidInput
	}
	at, err := s.nowUnix()
	if err != nil {
		return Attempt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Attempt{}, fmt.Errorf("claim: begin undispatched release: %w", err)
	}
	defer tx.Rollback()
	record, err := loadClaimTx(ctx, tx, handle.claimID)
	if err != nil {
		return Attempt{}, err
	}
	if err := verifyHandle(record, handle); err != nil {
		return Attempt{}, err
	}
	if record.state == StateReleased {
		return releasedAttempt(record), nil
	}
	if record.state == StateCommitted {
		return Attempt{}, ErrTerminal
	}
	if record.state == StateDispatched {
		return Attempt{}, ErrAlreadyDispatched
	}
	result, err := s.releaseClaimTx(ctx, tx, record, at)
	if err != nil {
		return Attempt{}, err
	}
	if err := tx.Commit(); err != nil {
		return Attempt{}, fmt.Errorf("claim: commit undispatched release: %w", err)
	}
	return result, nil
}

// CompleteAttempt writes the connector-neutral actual attempt projection and
// terminalizes one dispatched claim. ProtocolSuccess is explicit: an HTTP 200
// or clean EOF with ProtocolSuccess=false remains a failed attempt fact.
func (s *Service) CompleteAttempt(ctx context.Context, handle Handle, outcome AttemptOutcome) (Attempt, error) {
	if s == nil || s.db == nil || ctx == nil || !validHandle(handle) || !validAttemptOutcome(outcome) {
		return Attempt{}, ErrInvalidInput
	}
	at, err := s.nowUnix()
	if err != nil {
		return Attempt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Attempt{}, fmt.Errorf("claim: begin attempt completion: %w", err)
	}
	defer tx.Rollback()
	record, err := loadClaimTx(ctx, tx, handle.claimID)
	if err != nil {
		return Attempt{}, err
	}
	if err := verifyHandle(record, handle); err != nil {
		return Attempt{}, err
	}
	switch record.state {
	case StateCommitted:
		return readAttemptTx(ctx, tx, record.claimID)
	case StateReleased:
		return Attempt{}, ErrTerminal
	case StateClaimed:
		return Attempt{}, ErrNotDispatched
	case StateDispatched:
	default:
		return Attempt{}, ErrInvariant
	}
	result, err := s.completeAttemptTx(ctx, tx, record, candidateSnapshot{
		endpointID:    sql.NullInt64{Int64: handle.candidate.EndpointID, Valid: true},
		endpointKeyID: sql.NullInt64{Int64: handle.candidate.EndpointKeyID, Valid: true},
		connectorType: handle.candidate.ConnectorType,
		baseURL:       handle.candidate.CanonicalBaseURL,
		upstreamModel: handle.candidate.UpstreamModelID,
	}, outcome, at)
	if err != nil {
		return Attempt{}, err
	}
	if err := tx.Commit(); err != nil {
		return Attempt{}, fmt.Errorf("claim: commit attempt completion: %w", err)
	}
	return result, nil
}

// CompleteRequest writes only the caller-visible terminal result. Attempt
// status/usage is intentionally independent and must already be terminal.
func (s *Service) CompleteRequest(ctx context.Context, input CompleteRequestInput) (Request, error) {
	if s == nil || s.db == nil || ctx == nil || !validCompleteRequestInput(input) {
		return Request{}, ErrInvalidInput
	}
	at, err := s.nowUnix()
	if err != nil {
		return Request{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Request{}, fmt.Errorf("claim: begin request completion: %w", err)
	}
	defer tx.Rollback()
	request, err := loadRequestTx(ctx, tx, input.RequestID)
	if err != nil {
		return Request{}, err
	}
	if request.State == RequestTerminal {
		if request.CallerStatus != input.Caller.Status || request.ResultClass != input.Caller.Class ||
			request.CallerErrorCode != input.Caller.ErrorCode || request.AccountingDisposition != input.Disposition {
			return Request{}, ErrConflict
		}
		return request, nil
	}
	result, err := s.completeRequestTx(ctx, tx, request, input, at)
	if err != nil {
		return Request{}, err
	}
	if err := tx.Commit(); err != nil {
		return Request{}, fmt.Errorf("claim: commit request completion: %w", err)
	}
	return result, nil
}

type claimRecord struct {
	claimID         string
	requestID       string
	attemptSeq      int
	purpose         Purpose
	state           ClaimState
	endpointKeyID   sql.NullInt64
	secretRefID     sql.NullInt64
	donationKeyID   sql.NullInt64
	receiverUserID  sql.NullInt64
	dispatchedAt    sql.NullInt64
	terminalAt      sql.NullInt64
	rewardActual    sql.NullInt64
	rewardState     RewardState
	requestLogID    int64
	connectorType   connectorcontract.Type
	baseURL         string
	upstreamModel   string
	currentEndpoint sql.NullInt64
}

type candidateSnapshot struct {
	endpointID    sql.NullInt64
	endpointKeyID sql.NullInt64
	connectorType connectorcontract.Type
	baseURL       string
	upstreamModel string
}

func loadClaimTx(ctx context.Context, tx *sql.Tx, claimID string) (claimRecord, error) {
	var record claimRecord
	var purposeText, stateText, rewardText string
	var connectorText sql.NullString
	var baseURL, upstreamModel sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT
c.id,c.logical_request_id,c.attempt_seq,c.purpose,c.state,c.endpoint_key_id,c.secret_ref_id,
c.donation_key_id,c.receiver_user_id,c.dispatched_at,c.terminal_at,c.donor_reward_actual_milli,
c.donor_reward_state,l.id,s.connector_type,s.canonical_base_url,l.upstream_model_id,e.id
FROM dispatch_claims c
JOIN request_logs l ON l.logical_request_id=c.logical_request_id
LEFT JOIN endpoint_key_secrets s ON s.id=c.secret_ref_id
LEFT JOIN endpoint_keys k ON k.id=c.endpoint_key_id
LEFT JOIN endpoints e ON e.id=k.endpoint_id
WHERE c.id=?`, claimID).Scan(
		&record.claimID, &record.requestID, &record.attemptSeq, &purposeText, &stateText,
		&record.endpointKeyID, &record.secretRefID, &record.donationKeyID, &record.receiverUserID,
		&record.dispatchedAt, &record.terminalAt, &record.rewardActual, &rewardText,
		&record.requestLogID, &connectorText, &baseURL, &upstreamModel, &record.currentEndpoint)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return claimRecord{}, ErrNotFound
		}
		return claimRecord{}, fmt.Errorf("claim: read claim state: %w", err)
	}
	record.purpose = Purpose(purposeText)
	record.state = ClaimState(stateText)
	record.rewardState = RewardState(rewardText)
	if connectorText.Valid {
		record.connectorType = connectorcontract.Type(connectorText.String)
	}
	if baseURL.Valid {
		record.baseURL = baseURL.String
	}
	if upstreamModel.Valid {
		record.upstreamModel = upstreamModel.String
	}
	if !validPersistedClaim(record) {
		return claimRecord{}, ErrInvariant
	}
	return record, nil
}

func validPersistedClaim(record claimRecord) bool {
	if !validClaimState(record.state) || !validPurpose(record.purpose) || record.requestLogID <= 0 ||
		record.attemptSeq < 1 || record.attemptSeq > MaxAttempts {
		return false
	}
	if record.state == StateClaimed || record.state == StateDispatched {
		return record.secretRefID.Valid && validCandidateForPurpose(candidateFromRecord(record), record.purpose)
	}
	return !record.secretRefID.Valid
}

func verifyHandle(record claimRecord, handle Handle) error {
	if record.claimID != handle.claimID || record.requestID != handle.requestID ||
		record.attemptSeq != handle.attemptSeq || record.purpose != handle.purpose {
		return ErrConflict
	}
	if record.state == StateClaimed || record.state == StateDispatched {
		if record.connectorType != handle.candidate.ConnectorType ||
			record.baseURL != handle.candidate.CanonicalBaseURL ||
			record.upstreamModel != handle.candidate.UpstreamModelID {
			return ErrConflict
		}
		if record.endpointKeyID.Valid && record.endpointKeyID.Int64 != handle.candidate.EndpointKeyID {
			return ErrConflict
		}
		if record.currentEndpoint.Valid && record.currentEndpoint.Int64 != handle.candidate.EndpointID {
			return ErrConflict
		}
	}
	return nil
}

func (s *Service) releaseClaimTx(ctx context.Context, tx *sql.Tx, record claimRecord, at int64) (Attempt, error) {
	if record.state != StateClaimed || !record.secretRefID.Valid {
		return Attempt{}, ErrConflict
	}
	persist := func(callbackCtx context.Context, callbackTx *sql.Tx) error {
		if callbackCtx == nil || callbackTx == nil {
			return ErrInvariant
		}
		if record.purpose == PurposeCharity {
			if err := s.charity.ReleaseUndispatched(callbackCtx, callbackTx, CharityRelease{
				RequestID:     record.requestID,
				ClaimID:       record.claimID,
				DonationKeyID: nullableInt64(record.donationKeyID),
				ReleasedAt:    at,
			}); err != nil {
				return fmt.Errorf("claim: release charity attempt: %w", err)
			}
			if err := decrementClaimCapacityTx(callbackCtx, callbackTx, record.requestID); err != nil {
				return err
			}
		}
		result, err := callbackTx.ExecContext(callbackCtx, `UPDATE dispatch_claims
SET state='released',secret_ref_id=NULL,donor_reward_actual_milli=NULL,
donor_reward_state=CASE WHEN purpose='charity' THEN 'not_due' ELSE 'not_applicable' END,
terminal_at=?
WHERE id=? AND state='claimed'`, at, record.claimID)
		if err != nil {
			return fmt.Errorf("claim: persist undispatched release: %w", err)
		}
		if err := requireOneRow(result); err != nil {
			return err
		}
		return markSecretOrphanTx(callbackCtx, callbackTx, record.secretRefID.Int64, at)
	}
	if record.purpose == PurposeCharity {
		if s.charity == nil || s.accounting == nil {
			return Attempt{}, ErrDependencyUnavailable
		}
		if err := s.accounting.ReleaseUndispatched(ctx, tx, ClaimAccounting{
			RequestID:   record.requestID,
			ClaimID:     record.claimID,
			Purpose:     record.purpose,
			RewardState: RewardNotDue,
		}, persist); err != nil {
			return Attempt{}, fmt.Errorf("claim: release claim accounting: %w", err)
		}
	} else if err := persist(ctx, tx); err != nil {
		return Attempt{}, err
	}
	persisted, err := loadClaimTx(ctx, tx, record.claimID)
	if err != nil {
		return Attempt{}, err
	}
	if persisted.state != StateReleased || persisted.secretRefID.Valid {
		return Attempt{}, ErrInvariant
	}
	return releasedAttempt(persisted), nil
}

func (s *Service) completeAttemptTx(
	ctx context.Context,
	tx *sql.Tx,
	record claimRecord,
	snapshot candidateSnapshot,
	outcome AttemptOutcome,
	at int64,
) (Attempt, error) {
	if record.state != StateDispatched || !record.secretRefID.Valid || !record.dispatchedAt.Valid {
		return Attempt{}, ErrConflict
	}
	if at < record.dispatchedAt.Int64 {
		at = record.dispatchedAt.Int64
	}
	if snapshot.connectorType != record.connectorType || snapshot.baseURL != record.baseURL ||
		snapshot.upstreamModel != record.upstreamModel || !validCandidateSnapshot(snapshot, record.purpose) {
		return Attempt{}, ErrInvariant
	}

	rewardActual := sql.NullInt64{}
	rewardState := RewardNotApplicable
	var receiver *int64
	var charityInput CharityAttemptInput
	var charityActual CharityActual
	if record.purpose == PurposeCharity {
		if s.charity == nil || s.accounting == nil {
			return Attempt{}, ErrDependencyUnavailable
		}
		isAdmin := false
		if record.receiverUserID.Valid {
			var admin int
			if err := tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id=?`, record.receiverUserID.Int64).Scan(&admin); err != nil {
				if !errors.Is(err, sql.ErrNoRows) {
					return Attempt{}, fmt.Errorf("claim: revalidate reward receiver: %w", err)
				}
				record.receiverUserID.Valid = false
			} else {
				isAdmin = admin == 1
			}
		}
		suppressReward := !outcome.Usage.Present || isAdmin
		charityInput = CharityAttemptInput{
			RequestID:       record.requestID,
			ClaimID:         record.claimID,
			DonationKeyID:   nullableInt64(record.donationKeyID),
			ReceiverUserID:  nullableInt64(record.receiverUserID),
			SuppressReward:  suppressReward,
			Usage:           outcome.Usage,
			ProtocolSuccess: outcome.ProtocolSuccess,
			ResponseStarted: outcome.ResponseStarted,
			UsageUnknown:    !outcome.Usage.Present,
			CompletedAt:     at,
		}
		actual, err := s.charity.PrepareAttempt(ctx, tx, charityInput)
		if err != nil {
			return Attempt{}, fmt.Errorf("claim: prepare charity attempt: %w", err)
		}
		if !validMoney(actual.PriceMilli) || !validMoney(actual.RewardMilli) {
			return Attempt{}, ErrInvariant
		}
		if suppressReward && actual.RewardMilli != 0 {
			return Attempt{}, ErrInvariant
		}
		rewardActual = sql.NullInt64{Int64: actual.RewardMilli, Valid: true}
		switch {
		case actual.RewardMilli == 0:
			rewardState = RewardZero
		case !record.receiverUserID.Valid:
			rewardState = RewardReceiverDeleted
		default:
			rewardState = RewardPosted
			receiverID := record.receiverUserID.Int64
			receiver = &receiverID
		}
		charityActual = actual
	}

	status := nullableStatus(outcome.UpstreamStatus)
	code := nullableText(safeUpstreamCode(outcome.UpstreamCode))
	diagText := diagnostic.Bound(outcome.Diagnostic)
	diag := nullableText(diagText)
	usageUnknown := boolInt(!outcome.Usage.Present)
	persist := func(callbackCtx context.Context, callbackTx *sql.Tx) error {
		if callbackCtx == nil || callbackTx == nil {
			return ErrInvariant
		}
		if record.purpose == PurposeCharity {
			if err := decrementClaimCapacityTx(callbackCtx, callbackTx, record.requestID); err != nil {
				return err
			}
			if err := s.charity.CompleteAttempt(callbackCtx, callbackTx, CharityAttemptCompletion{
				Attempt:     charityInput,
				Actual:      charityActual,
				RewardState: rewardState,
			}); err != nil {
				return fmt.Errorf("claim: persist charity attempt: %w", err)
			}
		}
		_, err := callbackTx.ExecContext(callbackCtx, `INSERT INTO request_attempts(
claim_id,request_log_id,attempt_seq,endpoint_id_snapshot,endpoint_key_id_snapshot,
connector_type,canonical_base_url,upstream_model_id,result_kind,upstream_status,upstream_code,diag,
input_tokens,cache_write_input_tokens,cache_read_input_tokens,output_tokens,usage_unknown,started_at,completed_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			record.claimID, record.requestLogID, record.attemptSeq,
			nullableSQLInt64(snapshot.endpointID), nullableSQLInt64(snapshot.endpointKeyID),
			snapshot.connectorType, snapshot.baseURL, snapshot.upstreamModel, outcome.Kind,
			status, code, diag, outcome.Usage.UncachedInputTokens, outcome.Usage.CacheWriteInputTokens,
			outcome.Usage.CacheReadInputTokens, outcome.Usage.OutputTokens, usageUnknown,
			record.dispatchedAt.Int64, at)
		if err != nil {
			return fmt.Errorf("claim: persist request attempt: %w", err)
		}
		result, err := callbackTx.ExecContext(callbackCtx, `UPDATE dispatch_claims
SET state='committed',secret_ref_id=NULL,donor_reward_actual_milli=?,donor_reward_state=?,terminal_at=?
WHERE id=? AND state='dispatched'`, nullableSQLInt64(rewardActual), rewardState, at, record.claimID)
		if err != nil {
			return fmt.Errorf("claim: persist attempt terminal state: %w", err)
		}
		if err := requireOneRow(result); err != nil {
			return err
		}
		if err := updateRequestLogAttemptAggregateTx(callbackCtx, callbackTx, record.requestLogID, outcome, diagText); err != nil {
			return err
		}
		return markSecretOrphanTx(callbackCtx, callbackTx, record.secretRefID.Int64, at)
	}
	if record.purpose == PurposeCharity {
		if err := s.accounting.CompleteAttempt(ctx, tx, ClaimAccounting{
			RequestID:         record.requestID,
			ClaimID:           record.claimID,
			Purpose:           record.purpose,
			RewardActualMilli: charityActual.RewardMilli,
			RewardState:       rewardState,
			ReceiverUserID:    receiver,
		}, persist); err != nil {
			return Attempt{}, fmt.Errorf("claim: complete claim accounting: %w", err)
		}
	} else if err := persist(ctx, tx); err != nil {
		return Attempt{}, err
	}
	persisted, err := readAttemptTx(ctx, tx, record.claimID)
	if err != nil {
		return Attempt{}, err
	}
	if persisted.State != StateCommitted || persisted.RewardState != rewardState {
		return Attempt{}, ErrInvariant
	}
	return persisted, nil
}

func (s *Service) completeRequestTx(
	ctx context.Context,
	tx *sql.Tx,
	request Request,
	input CompleteRequestInput,
	at int64,
) (Request, error) {
	var nonterminalClaims, dispatchedClaims int
	if err := tx.QueryRowContext(ctx, `SELECT
COUNT(*) FILTER (WHERE state IN ('claimed','dispatched')),
COUNT(*) FILTER (WHERE dispatched_at IS NOT NULL)
FROM dispatch_claims WHERE logical_request_id=?`, request.ID).Scan(&nonterminalClaims, &dispatchedClaims); err != nil {
		return Request{}, fmt.Errorf("claim: inspect request attempts: %w", err)
	}
	if nonterminalClaims != 0 {
		return Request{}, ErrConflict
	}
	if input.Disposition == AccountingRelease && dispatchedClaims != 0 {
		return Request{}, ErrConflict
	}
	if input.Disposition == AccountingCommit && dispatchedClaims == 0 {
		return Request{}, ErrConflict
	}

	remaining, err := readRequestCapacityTx(ctx, tx, request.ID)
	if err != nil {
		return Request{}, err
	}
	if at < request.CreatedAt {
		at = request.CreatedAt
	}
	if request.Route == RouteDiscovery {
		if input.Disposition != AccountingNone || input.ActualChargeMilli != 0 || remaining != 0 {
			return Request{}, ErrConflict
		}
	} else {
		if input.Disposition != AccountingCommit && input.Disposition != AccountingRelease {
			return Request{}, ErrConflict
		}
		if s.accounting == nil {
			return Request{}, ErrDependencyUnavailable
		}
		if request.AccountingDisposition != AccountingReserved || remaining < 1 {
			return Request{}, ErrInvariant
		}
		if request.Route == RouteCharityChat && s.charity == nil {
			return Request{}, ErrDependencyUnavailable
		}
	}

	status := nullableStatus(input.Caller.Status)
	errorCode := nullableText(input.Caller.ErrorCode)
	durationMillis := (at - request.CreatedAt) * 1000
	if durationMillis > 86_400_000 {
		durationMillis = 86_400_000
	}
	errorSource := "platform"
	if input.Caller.ErrorCode == httperr.CodeUpstream {
		errorSource = "upstream"
	}
	terminal := func(callbackCtx context.Context, callbackTx *sql.Tx) error {
		if callbackCtx == nil || callbackTx == nil {
			return ErrInvariant
		}
		if request.Route != RouteDiscovery {
			if err := setRequestCapacityTx(callbackCtx, callbackTx, request.ID, 1, 0); err != nil {
				return err
			}
			if request.Route == RouteCharityChat {
				if err := s.charity.CompleteRequest(callbackCtx, callbackTx, CharityRequestCompletion{
					RequestID:   request.ID,
					Caller:      input.Caller,
					Disposition: input.Disposition,
					CompletedAt: at,
				}); err != nil {
					return fmt.Errorf("claim: complete charity request: %w", err)
				}
			}
		}
		result, err := callbackTx.ExecContext(callbackCtx, `UPDATE logical_requests SET
state='terminal',caller_result_class=?,caller_status=?,caller_error_code=?,accounting_state=?,
account_reserved_milli=0,ledger_rows_remaining=?,terminal_at=?
WHERE id=? AND state IN ('accepted','running') AND ledger_rows_remaining=?`,
			input.Caller.Class, status, errorCode, input.Disposition, u128Small(0), at,
			request.ID, u128Small(0))
		if err != nil {
			return fmt.Errorf("claim: persist request terminal state: %w", err)
		}
		if err := requireOneRow(result); err != nil {
			return err
		}
		logResult, err := callbackTx.ExecContext(callbackCtx, `UPDATE request_logs SET
caller_result_class=?,caller_status=?,caller_error_code=?,status_code=?,error_code=?,
completed_at=?,duration_ms=?,error_source=?
WHERE logical_request_id=?
  AND caller_result_class IS ? AND caller_status IS ? AND caller_error_code IS ?
  AND status_code=? AND error_code=? AND completed_at IS ?`,
			input.Caller.Class, status, errorCode, input.Caller.Status, input.Caller.ErrorCode,
			at, durationMillis, errorSource, request.ID,
			input.Caller.Class, status, errorCode, input.Caller.Status, input.Caller.ErrorCode, at)
		if err != nil {
			return fmt.Errorf("claim: finalize request log: %w", err)
		}
		if err := requireOneRow(logResult); err != nil {
			return fmt.Errorf("claim: verify request log terminal mirror: %w", err)
		}
		return addRequestUsageTx(callbackCtx, callbackTx, request.ID, request.UserID, at)
	}

	if request.Route == RouteDiscovery {
		if err := terminal(ctx, tx); err != nil {
			return Request{}, err
		}
	} else {
		var releaseUnused DomainPersistence
		if remaining > 1 {
			releaseUnused = func(callbackCtx context.Context, callbackTx *sql.Tx) error {
				return setRequestCapacityTx(callbackCtx, callbackTx, request.ID, remaining, 1)
			}
		}
		if err := s.accounting.CompleteRequest(ctx, tx, RequestAccounting{
			RequestID:     request.ID,
			UserID:        request.UserID,
			Route:         request.Route,
			ReservedMilli: request.ReservedMilli,
			ActualMilli:   input.ActualChargeMilli,
			RemainingRows: uint16(remaining),
			Destination:   request.Destination,
			Disposition:   input.Disposition,
		}, releaseUnused, terminal); err != nil {
			return Request{}, fmt.Errorf("claim: complete request accounting: %w", err)
		}
	}
	persisted, err := loadRequestTx(ctx, tx, request.ID)
	if err != nil {
		return Request{}, err
	}
	if persisted.State != RequestTerminal || persisted.AccountingDisposition != input.Disposition {
		return Request{}, ErrInvariant
	}
	return persisted, nil
}

func loadRequestTx(ctx context.Context, tx *sql.Tx, requestID string) (Request, error) {
	var request Request
	var userID sql.NullInt64
	var routeText, stateText, resultText, accountingText, destinationText string
	var resultClass, callerCode sql.NullString
	var status sql.NullInt64
	var terminal sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id,user_id,route_kind,model_snapshot,state,attempt_limit,
caller_result_class,caller_status,caller_error_code,accounting_state,account_reserved_milli,
settlement_destination,created_at,terminal_at
FROM logical_requests WHERE id=?`, requestID).Scan(
		&request.ID, &userID, &routeText, &request.ModelSnapshot, &stateText, &request.AttemptLimit,
		&resultClass, &status, &callerCode, &accountingText, &request.ReservedMilli,
		&destinationText, &request.CreatedAt, &terminal)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Request{}, ErrNotFound
		}
		return Request{}, fmt.Errorf("claim: read logical request: %w", err)
	}
	if userID.Valid {
		value := userID.Int64
		request.UserID = &value
	}
	request.Route = RouteKind(routeText)
	request.State = RequestState(stateText)
	if resultClass.Valid {
		resultText = resultClass.String
		request.ResultClass = ResultClass(resultText)
	}
	if status.Valid {
		request.CallerStatus = int(status.Int64)
	}
	if callerCode.Valid {
		request.CallerErrorCode = callerCode.String
	}
	request.AccountingDisposition = AccountingDisposition(accountingText)
	request.Destination = SettlementDestination(destinationText)
	if terminal.Valid {
		request.TerminalAt = terminal.Int64
	}
	if !validPersistedRequest(request) {
		return Request{}, ErrInvariant
	}
	return request, nil
}

func readAttemptTx(ctx context.Context, tx *sql.Tx, claimID string) (Attempt, error) {
	var attempt Attempt
	var stateText, kindText, rewardText string
	var code, diag sql.NullString
	var upstreamStatus sql.NullInt64
	var usageUnknown int
	var rewardActual sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT a.claim_id,c.logical_request_id,a.attempt_seq,c.state,a.result_kind,
a.upstream_status,a.upstream_code,a.diag,a.input_tokens,a.cache_write_input_tokens,
a.cache_read_input_tokens,a.output_tokens,a.usage_unknown,a.started_at,a.completed_at,
c.donor_reward_actual_milli,c.donor_reward_state
FROM request_attempts a JOIN dispatch_claims c ON c.id=a.claim_id WHERE a.claim_id=?`, claimID).Scan(
		&attempt.ClaimID, &attempt.RequestID, &attempt.AttemptSeq, &stateText, &kindText,
		&upstreamStatus, &code, &diag, &attempt.Usage.UncachedInputTokens,
		&attempt.Usage.CacheWriteInputTokens, &attempt.Usage.CacheReadInputTokens,
		&attempt.Usage.OutputTokens, &usageUnknown, &attempt.StartedAt, &attempt.CompletedAt,
		&rewardActual, &rewardText)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Attempt{}, ErrInvariant
		}
		return Attempt{}, fmt.Errorf("claim: read terminal attempt: %w", err)
	}
	attempt.State = ClaimState(stateText)
	attempt.Kind = ResultKind(kindText)
	if upstreamStatus.Valid {
		attempt.UpstreamStatus = int(upstreamStatus.Int64)
	}
	if code.Valid {
		attempt.UpstreamCode = code.String
	}
	if diag.Valid {
		attempt.Diagnostic = diag.String
	}
	attempt.Usage.Present = usageUnknown == 0
	if rewardActual.Valid {
		attempt.RewardActualMilli = rewardActual.Int64
	}
	attempt.RewardState = RewardState(rewardText)
	return attempt, nil
}

func releasedAttempt(record claimRecord) Attempt {
	completedAt := int64(0)
	if record.terminalAt.Valid {
		completedAt = record.terminalAt.Int64
	}
	return Attempt{
		ClaimID:     record.claimID,
		RequestID:   record.requestID,
		AttemptSeq:  record.attemptSeq,
		State:       StateReleased,
		CompletedAt: completedAt,
		RewardState: record.rewardState,
	}
}

func decrementClaimCapacityTx(ctx context.Context, tx *sql.Tx, requestID string) error {
	remaining, err := readRequestCapacityTx(ctx, tx, requestID)
	if err != nil {
		return err
	}
	if remaining < 2 {
		return ErrInvariant
	}
	return setRequestCapacityTx(ctx, tx, requestID, remaining, remaining-1)
}

func readRequestCapacityTx(ctx context.Context, tx *sql.Tx, requestID string) (uint64, error) {
	var encoded []byte
	if err := tx.QueryRowContext(ctx, `SELECT ledger_rows_remaining FROM logical_requests WHERE id=?`, requestID).Scan(&encoded); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("claim: read request capacity: %w", err)
	}
	remaining, ok := decodeSmallU128(encoded)
	clear(encoded)
	if !ok || remaining > MaxAttempts+1 {
		return 0, ErrInvariant
	}
	return remaining, nil
}

func setRequestCapacityTx(ctx context.Context, tx *sql.Tx, requestID string, before, after uint64) error {
	if before > MaxAttempts+1 || after > MaxAttempts+1 || before <= after {
		return ErrInvariant
	}
	result, err := tx.ExecContext(ctx, `UPDATE logical_requests SET ledger_rows_remaining=?
WHERE id=? AND ledger_rows_remaining=?`, u128Small(after), requestID, u128Small(before))
	if err != nil {
		return fmt.Errorf("claim: update request capacity: %w", err)
	}
	return requireOneRow(result)
}

func decodeSmallU128(encoded []byte) (uint64, bool) {
	if len(encoded) != 16 {
		return 0, false
	}
	for _, value := range encoded[:8] {
		if value != 0 {
			return 0, false
		}
	}
	var out uint64
	for _, value := range encoded[8:] {
		out = out<<8 | uint64(value)
	}
	return out, true
}

func markSecretOrphanTx(ctx context.Context, tx *sql.Tx, secretRefID, at int64) error {
	if secretRefID <= 0 {
		return ErrInvariant
	}
	if _, err := tx.ExecContext(ctx, `UPDATE endpoint_key_secrets SET orphaned_at=?
WHERE id=? AND orphaned_at IS NULL
AND NOT EXISTS(SELECT 1 FROM endpoint_keys k WHERE k.secret_ref_id=endpoint_key_secrets.id)
AND NOT EXISTS(SELECT 1 FROM dispatch_claims c WHERE c.secret_ref_id=endpoint_key_secrets.id
               AND c.state IN ('claimed','dispatched'))`, at, secretRefID); err != nil {
		return fmt.Errorf("claim: mark orphan credential: %w", err)
	}
	return nil
}

func updateRequestLogAttemptAggregateTx(
	ctx context.Context,
	tx *sql.Tx,
	requestLogID int64,
	outcome AttemptOutcome,
	diag string,
) error {
	var attempts, unknown int
	var uncached, cacheWrite, cacheRead, output int64
	if err := tx.QueryRowContext(ctx, `SELECT attempt_count,uncached_input_tokens,
cache_write_input_tokens,cache_read_input_tokens,output_tokens,usage_unknown
FROM request_logs WHERE id=?`, requestLogID).Scan(
		&attempts, &uncached, &cacheWrite, &cacheRead, &output, &unknown); err != nil {
		return fmt.Errorf("claim: read request attempt aggregate: %w", err)
	}
	if attempts >= MaxAttempts {
		return ErrInvariant
	}
	errorSource := "upstream"
	if outcome.ProtocolSuccess {
		errorSource = "platform"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE request_logs SET
attempt_count=?,uncached_input_tokens=?,cache_write_input_tokens=?,
cache_read_input_tokens=?,output_tokens=?,usage_unknown=?,error_source=?,error_diag=?
WHERE id=?`, attempts+1,
		saturatingAddNonnegative(uncached, outcome.Usage.UncachedInputTokens),
		saturatingAddNonnegative(cacheWrite, outcome.Usage.CacheWriteInputTokens),
		saturatingAddNonnegative(cacheRead, outcome.Usage.CacheReadInputTokens),
		saturatingAddNonnegative(output, outcome.Usage.OutputTokens),
		boolInt(unknown != 0 || !outcome.Usage.Present), errorSource, diag, requestLogID); err != nil {
		return fmt.Errorf("claim: update request attempt aggregate: %w", err)
	}
	return nil
}

func saturatingAddNonnegative(left, right int64) int64 {
	if left < 0 || right < 0 {
		return 0
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	if right > maxInt64-left {
		return maxInt64
	}
	return left + right
}

func candidateFromRecord(record claimRecord) Candidate {
	endpointID := int64(1)
	if record.currentEndpoint.Valid {
		endpointID = record.currentEndpoint.Int64
	}
	endpointKeyID := int64(1)
	if record.endpointKeyID.Valid {
		endpointKeyID = record.endpointKeyID.Int64
	}
	return Candidate{
		EndpointID:       endpointID,
		EndpointKeyID:    endpointKeyID,
		ConnectorType:    record.connectorType,
		CanonicalBaseURL: record.baseURL,
		UpstreamModelID:  record.upstreamModel,
	}
}

func validCandidateSnapshot(snapshot candidateSnapshot, purpose Purpose) bool {
	return validCandidateForPurpose(Candidate{
		EndpointID:       1,
		EndpointKeyID:    1,
		ConnectorType:    snapshot.connectorType,
		CanonicalBaseURL: snapshot.baseURL,
		UpstreamModelID:  snapshot.upstreamModel,
	}, purpose)
}

func validCandidateForPurpose(candidate Candidate, purpose Purpose) bool {
	if purpose == PurposeDiscovery {
		return validDiscoveryCandidate(candidate)
	}
	return validCandidate(candidate)
}

func validPersistedRequest(request Request) bool {
	if !validRequestState(request.State) || request.AttemptLimit < 1 || request.AttemptLimit > MaxAttempts ||
		!validMoney(request.ReservedMilli) {
		return false
	}
	switch request.Route {
	case RouteOpenAIChat, RouteCharityChat:
	case RouteDiscovery:
		if request.AttemptLimit != 1 {
			return false
		}
	default:
		return false
	}
	switch request.AccountingDisposition {
	case AccountingNone, AccountingReserved, AccountingCommit, AccountingRelease:
	default:
		return false
	}
	return request.Destination == DestinationUser || request.Destination == DestinationExternal
}

func validCompleteRequestInput(input CompleteRequestInput) bool {
	if !validMoney(input.ActualChargeMilli) || !validCallerResult(input.Caller) {
		return false
	}
	switch input.Disposition {
	case AccountingCommit:
		return true
	case AccountingRelease, AccountingNone:
		return input.ActualChargeMilli == 0
	default:
		return false
	}
}

func validCallerResult(result CallerResult) bool {
	switch result.Class {
	case ResultSuccess:
		return result.Status >= 200 && result.Status <= 399 && result.ErrorCode == ""
	case ResultFailed:
		return result.Status >= 400 && result.Status <= 599 && httperr.IsStableCode(result.ErrorCode)
	case ResultCancelled:
		return result.Status == 0 && result.ErrorCode == ""
	default:
		return false
	}
}

func validAttemptOutcome(outcome AttemptOutcome) bool {
	if outcome.Kind != ResultResponse && outcome.Kind != ResultSynthetic {
		return false
	}
	if outcome.UpstreamStatus != 0 && (outcome.UpstreamStatus < 100 || outcome.UpstreamStatus > 599) {
		return false
	}
	if outcome.Kind == ResultResponse && outcome.UpstreamStatus == 0 {
		return false
	}
	if outcome.ProtocolSuccess && (outcome.Kind != ResultResponse || !outcome.ResponseStarted ||
		outcome.UpstreamStatus < 200 || outcome.UpstreamStatus > 399) {
		return false
	}
	usage := outcome.Usage
	if usage.UncachedInputTokens < 0 || usage.CacheWriteInputTokens < 0 ||
		usage.CacheReadInputTokens < 0 || usage.OutputTokens < 0 {
		return false
	}
	return usage.Present || (usage.UncachedInputTokens == 0 && usage.CacheWriteInputTokens == 0 &&
		usage.CacheReadInputTokens == 0 && usage.OutputTokens == 0)
}

func safeUpstreamCode(value string) string {
	if value == "" || len(value) > 64 || !utf8.ValidString(value) {
		return ""
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x20 || value[index] > 0x7e {
			return ""
		}
	}
	return value
}

func validClaimState(state ClaimState) bool {
	return state == StateClaimed || state == StateDispatched || state == StateCommitted || state == StateReleased
}

func validRequestState(state RequestState) bool {
	return state == RequestAccepted || state == RequestRunning || state == RequestTerminal
}

func validPurpose(purpose Purpose) bool {
	return purpose == PurposeSelf || purpose == PurposeCharity || purpose == PurposeDebugLive || purpose == PurposeDiscovery
}

func nullableStatus(status int) any {
	if status == 0 {
		return nil
	}
	return status
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	out := value.Int64
	return &out
}

func nullableSQLInt64(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func requireOneRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("claim: verify state transition: %w", err)
	}
	if affected != 1 {
		return ErrConflict
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
