package reports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type decisionCase struct {
	id              string
	fingerprint     [32]byte
	status          string
	materialVersion int64
	targetVersion   int64
	deadline        int64
	retryAttempt    int64
	nextRetry       sql.NullInt64
	lastError       sql.NullString
}

func validDecisionReason(reason string) bool {
	if reason == "" || strings.TrimSpace(reason) == "" || !utf8.ValidString(reason) ||
		len(reason) > maxNoteBytes || utf8.RuneCountInString(reason) > maxNoteRunes {
		return false
	}
	for _, r := range reason {
		if unicode.IsControl(r) || r == 0x7f {
			return false
		}
	}
	return true
}

func (repository *Repository) authorizeAdminTx(ctx context.Context, tx *sql.Tx, actor authz.Actor) (authz.Principal, error) {
	principal, err := repository.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleAdministrator})
	if err != nil {
		return authz.Principal{}, mapAuthorizationError(err)
	}
	if principal.UserID <= 0 || principal.Role != authz.RoleAdministrator {
		return authz.Principal{}, ErrInvariant
	}
	return principal, nil
}

func beginAdminMutation(
	ctx context.Context,
	tx *sql.Tx,
	principal authz.Principal,
	idempotencyKey string,
	route string,
	caseID string,
	canonicalBody any,
	now int64,
) (idempotency.Decision, error) {
	if _, err := idempotency.KeyHash(idempotencyKey); err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	body, err := idempotency.CanonicalJSON(canonicalBody)
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	actorHash, err := idempotency.ActorScopeHash("admin", strconv.FormatInt(principal.UserID, 10))
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actorHash,
		Method:         http.MethodPost,
		Route:          route,
		PathResourceIDs: []string{
			caseID,
		},
		Query: "",
		Body:  body,
	})
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope:       idempotency.ScopeControlMutation,
		ActorHash:   actorHash,
		Key:         idempotencyKey,
		RequestHash: requestHash,
		DecisionNow: now,
	})
	switch {
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return idempotency.Decision{}, ErrConflict
	case err != nil:
		return idempotency.Decision{}, fmt.Errorf("reports: accept admin mutation: %w", err)
	default:
		return decision, nil
	}
}

func replayDecision(decision idempotency.Decision) (MutationResult, error) {
	var response DecisionResponse
	if decision.HTTPStatus != http.StatusOK && decision.HTTPStatus != http.StatusAccepted {
		return MutationResult{}, ErrInvariant
	}
	if err := json.Unmarshal(decision.ResponseBody, &response); err != nil ||
		!db.ValidateOpaqueID(response.ID, "rpc_") || response.Status == "" {
		return MutationResult{}, ErrInvariant
	}
	return MutationResult{
		Value: response, Status: decision.HTTPStatus,
		Body: append([]byte(nil), decision.ResponseBody...), Replayed: true,
	}, nil
}

func finishDecisionMutation(
	ctx context.Context,
	tx *sql.Tx,
	decision idempotency.Decision,
	status int,
	response DecisionResponse,
) (MutationResult, error) {
	body, err := json.Marshal(response)
	if err != nil {
		return MutationResult{}, ErrUnavailable
	}
	if err := idempotency.Complete(ctx, tx, decision, status, body); err != nil {
		return MutationResult{}, fmt.Errorf("reports: complete admin mutation: %w", err)
	}
	return MutationResult{Value: response, Status: status, Body: body}, nil
}

func readDecisionCaseTx(ctx context.Context, tx *sql.Tx, caseID string) (decisionCase, error) {
	var row decisionCase
	var fingerprint []byte
	err := tx.QueryRowContext(ctx, `SELECT id,fingerprint,status,material_version,target_version,deadline,
retry_attempt_count,next_retry_at,last_error_class FROM report_cases WHERE id=?`, caseID).Scan(
		&row.id, &fingerprint, &row.status, &row.materialVersion, &row.targetVersion, &row.deadline,
		&row.retryAttempt, &row.nextRetry, &row.lastError)
	if errors.Is(err, sql.ErrNoRows) {
		return decisionCase{}, ErrNotFound
	}
	if err != nil {
		return decisionCase{}, fmt.Errorf("reports: read decision case: %w", err)
	}
	if len(fingerprint) != len(row.fingerprint) || !db.ValidateOpaqueID(row.id, "rpc_") ||
		row.materialVersion < 1 || row.targetVersion < 1 || row.deadline < 0 {
		clear(fingerprint)
		return decisionCase{}, ErrInvariant
	}
	copy(row.fingerprint[:], fingerprint)
	clear(fingerprint)
	return row, nil
}

func decisionResponse(row decisionCase, status string) DecisionResponse {
	return DecisionResponse{
		ID: row.id, Status: status,
		MaterialVersion: strconv.FormatInt(row.materialVersion, 10),
		TargetVersion:   strconv.FormatInt(row.targetVersion, 10),
	}
}

func (repository *Repository) Approve(
	ctx context.Context,
	actor authz.Actor,
	caseID string,
	command ApproveCommand,
) (MutationResult, error) {
	if err := repository.admit(); err != nil {
		return MutationResult{}, err
	}
	defer repository.release()
	if ctx == nil || !db.ValidateOpaqueID(caseID, "rpc_") || command.ExpectedMaterialVersion < 1 ||
		command.ExpectedTargetVersion < 1 || !command.Confirmation || !validDecisionReason(command.Reason) {
		return MutationResult{}, ErrInvalidRequest
	}
	now, err := repository.nowUnix()
	if err != nil {
		return MutationResult{}, err
	}
	tx, err := repository.beginTx(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	principal, err := repository.authorizeAdminTx(ctx, tx, actor)
	if err != nil {
		return MutationResult{}, err
	}
	canonical := struct {
		ExpectedMaterialVersion string `json:"expected_material_version"`
		ExpectedTargetVersion   string `json:"expected_target_version"`
		Reason                  string `json:"reason"`
		Confirmation            bool   `json:"confirmation"`
	}{strconv.FormatInt(command.ExpectedMaterialVersion, 10), strconv.FormatInt(command.ExpectedTargetVersion, 10), command.Reason, command.Confirmation}
	replay, err := beginAdminMutation(ctx, tx, principal, command.IdempotencyKey, adminApproveRoute, caseID, canonical, now)
	if err != nil {
		return MutationResult{}, err
	}
	if replay.Kind == idempotency.Replay {
		result, err := replayDecision(replay)
		if err != nil {
			return MutationResult{}, err
		}
		if err := commit(tx, &committed); err != nil {
			return MutationResult{}, err
		}
		return result, nil
	}
	row, err := readDecisionCaseTx(ctx, tx, caseID)
	if err != nil {
		return MutationResult{}, err
	}
	if row.status != "pending_review" || row.deadline <= now ||
		row.materialVersion != command.ExpectedMaterialVersion || row.targetVersion != command.ExpectedTargetVersion {
		return MutationResult{}, ErrConflict
	}
	operationID, err := repository.generateID("op_")
	if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
		return MutationResult{}, ErrUnavailable
	}
	updated, err := tx.ExecContext(ctx, `UPDATE report_cases SET
 status='approved_processing',progress_state='in_progress',decision_reason=?,decision_actor_user_id=?,decision_at=?,
 cursor_text=NULL,processed_target_count=(SELECT COUNT(*) FROM report_targets WHERE case_id=? AND state<>'protected'),
 retry_attempt_count=0,next_retry_at=NULL,last_error_class=NULL
WHERE id=? AND status='pending_review' AND deadline>? AND material_version=? AND target_version=?`,
		command.Reason, principal.UserID, now, caseID, caseID, now, command.ExpectedMaterialVersion, command.ExpectedTargetVersion)
	if err != nil {
		return MutationResult{}, fmt.Errorf("reports: approve case: %w", err)
	}
	changed, _ := updated.RowsAffected()
	if changed != 1 {
		return MutationResult{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO report_decisions(
case_id,material_version,target_version,actor_user_id,action,reason,created_at)
VALUES(?,?,?,?,'approve',?,?)`, caseID, row.materialVersion, row.targetVersion, principal.UserID, command.Reason, now); err != nil {
		return MutationResult{}, fmt.Errorf("reports: append approval: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO accepted_operations(
id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,last_error_class,created_at,terminal_at)
VALUES(?,'report_approved_processing',?,'admin',?,'accepted',?,NULL,?,NULL)`,
		operationID, principal.UserID, row.fingerprint[:], caseID, now); err != nil {
		return MutationResult{}, fmt.Errorf("reports: accept approval processing: %w", err)
	}
	result, err := finishDecisionMutation(ctx, tx, replay, http.StatusAccepted, decisionResponse(row, "approved_processing"))
	if err != nil {
		return MutationResult{}, err
	}
	if err := commit(tx, &committed); err != nil {
		return MutationResult{}, err
	}
	repository.notifyWorker()
	return result, nil
}

func (repository *Repository) Reject(
	ctx context.Context,
	actor authz.Actor,
	caseID string,
	command RejectCommand,
) (MutationResult, error) {
	if err := repository.admit(); err != nil {
		return MutationResult{}, err
	}
	defer repository.release()
	if ctx == nil || !db.ValidateOpaqueID(caseID, "rpc_") || command.ExpectedMaterialVersion < 1 ||
		command.ExpectedTargetVersion < 1 || !validDecisionReason(command.Reason) {
		return MutationResult{}, ErrInvalidRequest
	}
	now, err := repository.nowUnix()
	if err != nil {
		return MutationResult{}, err
	}
	tx, err := repository.beginTx(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	principal, err := repository.authorizeAdminTx(ctx, tx, actor)
	if err != nil {
		return MutationResult{}, err
	}
	canonical := struct {
		ExpectedMaterialVersion string `json:"expected_material_version"`
		ExpectedTargetVersion   string `json:"expected_target_version"`
		Reason                  string `json:"reason"`
	}{strconv.FormatInt(command.ExpectedMaterialVersion, 10), strconv.FormatInt(command.ExpectedTargetVersion, 10), command.Reason}
	replay, err := beginAdminMutation(ctx, tx, principal, command.IdempotencyKey, adminRejectRoute, caseID, canonical, now)
	if err != nil {
		return MutationResult{}, err
	}
	if replay.Kind == idempotency.Replay {
		result, err := replayDecision(replay)
		if err != nil {
			return MutationResult{}, err
		}
		if err := commit(tx, &committed); err != nil {
			return MutationResult{}, err
		}
		return result, nil
	}
	row, err := readDecisionCaseTx(ctx, tx, caseID)
	if err != nil {
		return MutationResult{}, err
	}
	if row.status != "pending_review" || row.deadline <= now ||
		row.materialVersion != command.ExpectedMaterialVersion || row.targetVersion != command.ExpectedTargetVersion {
		return MutationResult{}, ErrConflict
	}
	var protectedCount int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM report_targets WHERE case_id=? AND state='protected'`, caseID).Scan(&protectedCount); err != nil {
		return MutationResult{}, fmt.Errorf("reports: count protected targets: %w", err)
	}
	updated, err := tx.ExecContext(ctx, `UPDATE report_cases SET
 status='rejected',progress_state='complete',decision_reason=?,decision_actor_user_id=?,decision_at=?,terminal_at=?,
 cursor_text=NULL,released_target_count=released_target_count+?,retry_attempt_count=0,next_retry_at=NULL,last_error_class=NULL
WHERE id=? AND status='pending_review' AND deadline>? AND material_version=? AND target_version=?`,
		command.Reason, principal.UserID, now, now, protectedCount, caseID, now, row.materialVersion, row.targetVersion)
	if err != nil {
		return MutationResult{}, fmt.Errorf("reports: reject case: %w", err)
	}
	changed, _ := updated.RowsAffected()
	if changed != 1 {
		return MutationResult{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE report_targets SET state='released',decided_version=?,updated_at=?
WHERE case_id=? AND state='protected'`, row.targetVersion, now, caseID); err != nil {
		return MutationResult{}, fmt.Errorf("reports: release rejected targets: %w", err)
	}
	issueTargets, err := readCaseIssueTargetsTx(ctx, tx, caseID)
	if err != nil {
		return MutationResult{}, err
	}
	issueTransitions := make(reportIssueTransitions)
	if err := snapshotReportIssueTargetsTx(ctx, tx, issueTransitions, issueTargets); err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM endpoint_key_suspensions WHERE reason_type='report_case' AND report_case_id=?`, caseID); err != nil {
		return MutationResult{}, fmt.Errorf("reports: release rejected suspension: %w", err)
	}
	if err := repository.reconcileReportIssueTransitionsTx(ctx, tx, issueTransitions); err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO report_decisions(
case_id,material_version,target_version,actor_user_id,action,reason,created_at)
VALUES(?,?,?,?,'reject',?,?)`, caseID, row.materialVersion, row.targetVersion, principal.UserID, command.Reason, now); err != nil {
		return MutationResult{}, fmt.Errorf("reports: append rejection: %w", err)
	}
	result, err := finishDecisionMutation(ctx, tx, replay, http.StatusOK, decisionResponse(row, "rejected"))
	if err != nil {
		return MutationResult{}, err
	}
	if err := commit(tx, &committed); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (repository *Repository) Resume(
	ctx context.Context,
	actor authz.Actor,
	caseID string,
	command ResumeCommand,
) (MutationResult, error) {
	if err := repository.admit(); err != nil {
		return MutationResult{}, err
	}
	defer repository.release()
	if ctx == nil || !db.ValidateOpaqueID(caseID, "rpc_") || command.ExpectedTargetVersion < 1 {
		return MutationResult{}, ErrInvalidRequest
	}
	now, err := repository.nowUnix()
	if err != nil {
		return MutationResult{}, err
	}
	tx, err := repository.beginTx(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	principal, err := repository.authorizeAdminTx(ctx, tx, actor)
	if err != nil {
		return MutationResult{}, err
	}
	canonical := struct {
		ExpectedTargetVersion string `json:"expected_target_version"`
	}{strconv.FormatInt(command.ExpectedTargetVersion, 10)}
	replay, err := beginAdminMutation(ctx, tx, principal, command.IdempotencyKey, adminResumeRoute, caseID, canonical, now)
	if err != nil {
		return MutationResult{}, err
	}
	if replay.Kind == idempotency.Replay {
		result, err := replayDecision(replay)
		if err != nil {
			return MutationResult{}, err
		}
		if err := commit(tx, &committed); err != nil {
			return MutationResult{}, err
		}
		return result, nil
	}
	row, err := readDecisionCaseTx(ctx, tx, caseID)
	if err != nil {
		return MutationResult{}, err
	}
	if row.status != "approved_processing" || row.targetVersion != command.ExpectedTargetVersion ||
		row.retryAttempt < 1 || !row.nextRetry.Valid || !row.lastError.Valid {
		return MutationResult{}, ErrConflict
	}
	expectedOperationState := "failed_retryable"
	switch row.lastError.String {
	case "db_busy", "internal_retryable":
	case "invariant_violation":
		expectedOperationState = "failed_blocked"
	default:
		return MutationResult{}, ErrInvariant
	}
	var operationID, operationState string
	err = tx.QueryRowContext(ctx, `SELECT id,state FROM accepted_operations
WHERE kind='report_approved_processing' AND checkpoint=? AND state=?
ORDER BY created_at DESC,id DESC LIMIT 1`, caseID, expectedOperationState).Scan(&operationID, &operationState)
	if err != nil {
		return MutationResult{}, fmt.Errorf("reports: read approval operation: %w", err)
	}
	switch operationState {
	case "failed_retryable":
		result, err := tx.ExecContext(ctx, `UPDATE accepted_operations SET
state='accepted',last_error_class=NULL,terminal_at=NULL WHERE id=? AND state='failed_retryable'`, operationID)
		if err != nil {
			return MutationResult{}, fmt.Errorf("reports: resume retryable operation: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return MutationResult{}, ErrConflict
		}
	case "failed_blocked":
		operationID, err = repository.generateID("op_")
		if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
			return MutationResult{}, ErrUnavailable
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO accepted_operations(
id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,last_error_class,created_at,terminal_at)
VALUES(?,'report_approved_processing',?,'admin',?,'accepted',?,NULL,?,NULL)`,
			operationID, principal.UserID, row.fingerprint[:], caseID, now); err != nil {
			return MutationResult{}, fmt.Errorf("reports: accept resumed operation: %w", err)
		}
	default:
		return MutationResult{}, ErrConflict
	}
	updated, err := tx.ExecContext(ctx, `UPDATE report_cases SET
retry_attempt_count=0,next_retry_at=NULL,last_error_class=NULL
WHERE id=? AND status='approved_processing' AND target_version=? AND retry_attempt_count=?`,
		caseID, row.targetVersion, row.retryAttempt)
	if err != nil {
		return MutationResult{}, fmt.Errorf("reports: resume case: %w", err)
	}
	changed, _ := updated.RowsAffected()
	if changed != 1 {
		return MutationResult{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO report_decisions(
case_id,material_version,target_version,actor_user_id,action,reason,created_at)
VALUES(?,?,?,?,'resume_processing','',?)`, caseID, row.materialVersion, row.targetVersion, principal.UserID, now); err != nil {
		return MutationResult{}, fmt.Errorf("reports: append resume decision: %w", err)
	}
	result, err := finishDecisionMutation(ctx, tx, replay, http.StatusAccepted, decisionResponse(row, "approved_processing"))
	if err != nil {
		return MutationResult{}, err
	}
	if err := commit(tx, &committed); err != nil {
		return MutationResult{}, err
	}
	repository.notifyWorker()
	return result, nil
}
