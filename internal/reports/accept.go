package reports

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type activeCase struct {
	id              string
	connector       string
	baseURL         string
	status          string
	materialVersion int64
	targetVersion   int64
	createdAt       int64
	deadline        int64
}

func validSecret(secret []byte) bool {
	return len(secret) > 0 && len(secret) <= maxSecretBytes && utf8.Valid(secret)
}

func validNote(note string) bool {
	if !utf8.ValidString(note) || len(note) > maxNoteBytes || utf8.RuneCountInString(note) > maxNoteRunes {
		return false
	}
	for _, value := range note {
		if unicode.IsControl(value) || value == 0x7f {
			return false
		}
	}
	return true
}

func (repository *Repository) validateSubmission(submission *PublicSubmission) error {
	if repository == nil || submission == nil || submission.Reporter != nil &&
		(submission.Reporter.Kind != authz.ActorUserSession || submission.Reporter.UserID <= 0) ||
		!validSecret(submission.Secret) || !validNote(submission.Note) {
		return ErrInvalidRequest
	}
	validatedConnector, err := repository.connectors.MustValidate(connectorcontract.Type(submission.ConnectorType))
	if err != nil || string(validatedConnector) != submission.ConnectorType {
		return ErrInvalidRequest
	}
	if len(submission.CanonicalBaseURL) == 0 || len(submission.CanonicalBaseURL) > maxBaseURLBytes || !utf8.ValidString(submission.CanonicalBaseURL) {
		return ErrInvalidRequest
	}
	canonical, err := repository.baseURLs.ValidateBaseURL(submission.CanonicalBaseURL)
	if err != nil || canonical != submission.CanonicalBaseURL {
		return ErrInvalidRequest
	}
	if _, err := idempotency.KeyHash(submission.IdempotencyKey); err != nil {
		return ErrInvalidRequest
	}
	return nil
}

func (repository *Repository) acceptanceDuration() (time.Duration, error) {
	var random [2]byte
	if err := repository.keys.fillRandom(random[:]); err != nil {
		return 0, ErrUnavailable
	}
	milliseconds := 350 + int(binary.BigEndian.Uint16(random[:])%151)
	clear(random[:])
	return time.Duration(milliseconds) * time.Millisecond, nil
}

// AcceptCredentialTheft applies the dedicated public acceptance rail. The
// caller must pass the connector registry's canonical type and the shared
// endpoint canonicalizer's exact output. Secret bytes are always cleared.
func (repository *Repository) AcceptCredentialTheft(ctx context.Context, submission PublicSubmission) error {
	started := time.Now()
	defer submission.clear()
	if err := repository.admit(); err != nil {
		return err
	}
	defer repository.release()
	if ctx == nil || ctx.Err() != nil {
		return ErrUnavailable
	}
	if err := repository.validateSubmission(&submission); err != nil {
		return err
	}
	duration, err := repository.acceptanceDuration()
	if err != nil {
		return err
	}
	select {
	case repository.publicSlots <- struct{}{}:
		defer func() { <-repository.publicSlots }()
	default:
		return ErrRateLimited
	}
	now, err := repository.nowUnix()
	if err != nil {
		return err
	}
	fingerprint, err := repository.keys.fingerprintDigest(submission.ConnectorType, submission.CanonicalBaseURL, submission.Secret)
	if err != nil {
		return err
	}
	requestHash, err := repository.keys.requestDigest(submission.ConnectorType, submission.CanonicalBaseURL, submission.Secret, submission.Note)
	clear(submission.Secret)
	submission.Secret = nil
	if err != nil {
		return err
	}
	if err := repository.acceptTransaction(ctx, submission, fingerprint, requestHash, now); err != nil {
		return err
	}
	remaining := duration - time.Since(started)
	if remaining > 0 {
		if err := repository.delay(ctx, remaining); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}

type rateCheck struct {
	scope       string
	hash        [32]byte
	windowStart int64
	expiresAt   int64
	limit       int64
}

func (repository *Repository) acceptRatesTx(
	ctx context.Context,
	tx *sql.Tx,
	reporter *authz.Actor,
	sourceIP [16]byte,
	fingerprint [32]byte,
	now int64,
) error {
	checks := make([]rateCheck, 0, 4)
	add := func(scope string, value []byte, window, expiry, limit int64) error {
		digest, err := repository.keys.rateDigest(scope, value)
		if err != nil {
			return err
		}
		start := now - now%window
		checks = append(checks, rateCheck{scope: scope, hash: digest, windowStart: start, expiresAt: start + expiry, limit: limit})
		return nil
	}
	if err := add("ip", sourceIP[:], 600, 1200, 5); err != nil {
		return err
	}
	if reporter != nil {
		if err := add("account", []byte(strconv.FormatInt(reporter.UserID, 10)), 600, 1200, 10); err != nil {
			return err
		}
	}
	if err := add("fingerprint", fingerprint[:], 600, 1200, 3); err != nil {
		return err
	}
	if err := add("global", nil, 60, 120, 64); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM report_rate_buckets WHERE rowid IN (
SELECT rowid FROM report_rate_buckets WHERE expires_at<=? ORDER BY expires_at,scope LIMIT 100)`, now); err != nil {
		return fmt.Errorf("reports: clean rate buckets: %w", err)
	}
	for _, check := range checks {
		result, err := tx.ExecContext(ctx, `
INSERT INTO report_rate_buckets(scope,scope_hash,window_start,count,updated_at,expires_at)
VALUES(?,?,?,1,?,?)
ON CONFLICT(scope,scope_hash,window_start) DO UPDATE SET
 count=report_rate_buckets.count+1,updated_at=excluded.updated_at
WHERE report_rate_buckets.count<?`, check.scope, check.hash[:], check.windowStart, now, check.expiresAt, check.limit)
		if err != nil {
			return fmt.Errorf("reports: update rate bucket: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("reports: observe rate bucket: %w", err)
		}
		if changed != 1 {
			return ErrRateLimited
		}
	}
	return nil
}

func mapAuthorizationError(err error) error {
	switch {
	case errors.Is(err, authz.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, authz.ErrForbidden), errors.Is(err, authz.ErrElevatedRequired):
		return ErrForbidden
	case errors.Is(err, authz.ErrNotFound):
		return ErrNotFound
	default:
		return fmt.Errorf("reports: final authorization: %w", err)
	}
}

func (repository *Repository) acceptTransaction(ctx context.Context, submission PublicSubmission, fingerprint, requestHash [32]byte, now int64) error {
	tx, err := repository.beginTx(ctx)
	if err != nil {
		return fmt.Errorf("reports: begin acceptance transaction: %w", err)
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	var principal *authz.Principal
	if submission.Reporter != nil {
		authorized, err := repository.authorizer.Authorize(ctx, tx, *submission.Reporter, authz.Requirement{Role: authz.RoleUser})
		if err != nil {
			return mapAuthorizationError(err)
		}
		if authorized.UserID != submission.Reporter.UserID || authorized.DiscordID == "" {
			return ErrInvariant
		}
		principal = &authorized
	}
	actorHash, err := repository.keys.acceptanceActorDigest()
	if err != nil {
		return err
	}
	keyHash, err := idempotency.KeyHash(submission.IdempotencyKey)
	if err != nil {
		return ErrInvalidRequest
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM idempotency_records
WHERE scope='credential_report' AND actor_scope_hash=? AND key_hash=? AND expires_at<=?`, actorHash[:], keyHash[:], now); err != nil {
		return fmt.Errorf("reports: expire acceptance: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,state,http_status,response_body,created_at,expires_at)
VALUES('credential_report',?,?,?,?, 'accepted',0,?, ?,?)
ON CONFLICT(scope,actor_scope_hash,key_hash) DO NOTHING`, actorHash[:], keyHash[:], requestHash[:], fingerprint[:], []byte{}, now, now+replayWindowSeconds)
	if err != nil {
		return fmt.Errorf("reports: accept replay identity: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("reports: observe replay identity: %w", err)
	}
	if inserted == 0 {
		var storedRequest, storedFingerprint, body []byte
		var state string
		var status int
		if err := tx.QueryRowContext(ctx, `SELECT request_hash,lookup_fingerprint,state,http_status,response_body
FROM idempotency_records WHERE scope='credential_report' AND actor_scope_hash=? AND key_hash=?`, actorHash[:], keyHash[:]).Scan(
			&storedRequest, &storedFingerprint, &state, &status, &body); err != nil {
			return fmt.Errorf("reports: read replay identity: %w", err)
		}
		if len(storedRequest) != 32 || !hmac.Equal(storedRequest, requestHash[:]) {
			return ErrConflict
		}
		if len(storedFingerprint) != 32 || !hmac.Equal(storedFingerprint, fingerprint[:]) || state != "completed" || status != http.StatusAccepted || !hmac.Equal(body, acceptedResponseBody) {
			return ErrInvariant
		}
		return commit(tx, &committed)
	}
	if err := repository.acceptRatesTx(ctx, tx, submission.Reporter, submission.SourceIP, fingerprint, now); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	caseRow, found, err := findActiveCaseTx(ctx, tx, fingerprint)
	if err != nil {
		return err
	}
	if found && (caseRow.connector != submission.ConnectorType || caseRow.baseURL != submission.CanonicalBaseURL) {
		return ErrInvariant
	}
	workerNeeded := false
	issueTransitions := make(reportIssueTransitions)
	if found && caseRow.status != "approved_processing" && caseRow.deadline <= now {
		deferredIssueTargets, err := readCaseIssueTargetsTx(ctx, tx, caseRow.id)
		if err != nil {
			return err
		}
		if err := snapshotReportIssueTargetsTx(ctx, tx, issueTransitions, deferredIssueTargets); err != nil {
			return err
		}
		expired, err := repository.expireActiveCaseWithoutIssueProjectionTx(
			ctx, tx, caseRow.id, caseRow.status, fingerprint[:], caseRow.materialVersion,
			caseRow.targetVersion, caseRow.deadline, now,
		)
		if err != nil {
			return err
		}
		if !expired {
			return ErrConflict
		}
		found = false
		workerNeeded = true
	}
	if !found {
		matched, err := liveFingerprintMatchTx(ctx, tx, submission.ConnectorType, submission.CanonicalBaseURL, fingerprint)
		if err != nil {
			return err
		}
		if matched {
			caseRow, err = repository.createCaseTx(ctx, tx, submission, principal, fingerprint, requestHash, now)
			if err != nil {
				return err
			}
			found = true
		}
	} else if caseRow.status == "pending_indexing" || caseRow.status == "pending_review" {
		if err := repository.mergeMaterialTx(ctx, tx, caseRow, submission, principal, requestHash, now); err != nil {
			return err
		}
	}
	if found {
		workerNeeded = true
		fingerprintTargets, err := readFingerprintIssueTargetsTx(
			ctx, tx, fingerprint[:], submission.ConnectorType, submission.CanonicalBaseURL,
		)
		if err != nil {
			return err
		}
		if err := snapshotReportIssueTargetsTx(ctx, tx, issueTransitions, fingerprintTargets); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO endpoint_key_suspensions(endpoint_key_id,reason_type,report_case_id,created_at)
SELECT k.id,'report_case',?,? FROM endpoint_keys k
JOIN endpoints e ON e.id=k.endpoint_id
WHERE k.secret_fingerprint=? AND e.connector_type=? AND e.base_url=?`,
			caseRow.id, now, fingerprint[:], submission.ConnectorType, submission.CanonicalBaseURL); err != nil {
			return fmt.Errorf("reports: apply fingerprint suspension: %w", err)
		}
	}
	if err := repository.reconcileReportIssueTransitionsTx(ctx, tx, issueTransitions); err != nil {
		return err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE idempotency_records
SET state='completed',http_status=?,response_body=?
WHERE scope='credential_report' AND actor_scope_hash=? AND key_hash=? AND request_hash=? AND state='accepted'`,
		http.StatusAccepted, acceptedResponseBody, actorHash[:], keyHash[:], requestHash[:])
	if err != nil {
		return fmt.Errorf("reports: complete acceptance replay: %w", err)
	}
	count, err := updated.RowsAffected()
	if err != nil || count != 1 {
		return ErrInvariant
	}
	if err := commit(tx, &committed); err != nil {
		return fmt.Errorf("reports: commit acceptance: %w", err)
	}
	if workerNeeded {
		repository.notifyWorker()
	}
	return nil
}

func findActiveCaseTx(ctx context.Context, tx *sql.Tx, fingerprint [32]byte) (activeCase, bool, error) {
	var row activeCase
	err := tx.QueryRowContext(ctx, `SELECT id,connector_type,canonical_base_url,status,material_version,target_version,created_at,deadline
FROM report_cases WHERE fingerprint=? AND status IN ('pending_indexing','pending_review','approved_processing')`, fingerprint[:]).Scan(
		&row.id, &row.connector, &row.baseURL, &row.status, &row.materialVersion, &row.targetVersion, &row.createdAt, &row.deadline)
	if errors.Is(err, sql.ErrNoRows) {
		return activeCase{}, false, nil
	}
	if err != nil {
		return activeCase{}, false, fmt.Errorf("reports: find active case: %w", err)
	}
	if !db.ValidateOpaqueID(row.id, "rpc_") || row.materialVersion < 1 || row.targetVersion < 1 {
		return activeCase{}, false, ErrInvariant
	}
	return row, true, nil
}

func liveFingerprintMatchTx(ctx context.Context, tx *sql.Tx, connector, baseURL string, fingerprint [32]byte) (bool, error) {
	var matched int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
WHERE k.secret_fingerprint=? AND e.connector_type=? AND e.base_url=?)`, fingerprint[:], connector, baseURL).Scan(&matched); err != nil {
		return false, fmt.Errorf("reports: query credential fingerprint: %w", err)
	}
	return matched == 1, nil
}

func (repository *Repository) createCaseTx(ctx context.Context, tx *sql.Tx, submission PublicSubmission, principal *authz.Principal, fingerprint, requestHash [32]byte, now int64) (activeCase, error) {
	caseID, err := repository.generateID("rpc_")
	if err != nil || !db.ValidateOpaqueID(caseID, "rpc_") {
		return activeCase{}, ErrUnavailable
	}
	operationID, err := repository.generateID("op_")
	if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
		return activeCase{}, ErrUnavailable
	}
	var ttlText string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='report_pending_ttl_seconds'`).Scan(&ttlText); err != nil {
		return activeCase{}, fmt.Errorf("reports: read pending TTL: %w", err)
	}
	ttl, err := strconv.ParseInt(ttlText, 10, 64)
	if err != nil || ttl < 1 || ttl > 72*60*60 || now > maxUnixSecond-ttl {
		return activeCase{}, ErrInvariant
	}
	deadline := now + ttl
	if _, err := tx.ExecContext(ctx, `INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,material_version,target_version,
 deadline,cursor_text,material_count,target_count,distinct_owner_count,processed_target_count,
 deleted_target_count,released_target_count,retry_attempt_count,created_at,legal_hold_consumed)
VALUES(?,?,?,?,'pending_indexing','in_progress',1,1,?,NULL,1,0,0,0,0,0,0,?,0)`,
		caseID, fingerprint[:], submission.ConnectorType, submission.CanonicalBaseURL, deadline, now); err != nil {
		return activeCase{}, fmt.Errorf("reports: create case: %w", err)
	}
	caseRow := activeCase{id: caseID, connector: submission.ConnectorType, baseURL: submission.CanonicalBaseURL, status: "pending_indexing", materialVersion: 1, targetVersion: 1, createdAt: now, deadline: deadline}
	if err := repository.insertMaterialTx(ctx, tx, caseRow, submission, principal, requestHash, now); err != nil {
		return activeCase{}, err
	}
	var actorUser any
	actorRole := ""
	if principal != nil {
		actorUser = principal.UserID
		actorRole = "user"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO accepted_operations(
 id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,last_error_class,created_at,terminal_at)
VALUES(?,'report_indexing',?,?,?,'accepted',?,NULL,?,NULL)`, operationID, actorUser, actorRole, fingerprint[:], caseID, now); err != nil {
		return activeCase{}, fmt.Errorf("reports: accept indexing operation: %w", err)
	}
	return caseRow, nil
}

func (repository *Repository) mergeMaterialTx(ctx context.Context, tx *sql.Tx, caseRow activeCase, submission PublicSubmission, principal *authz.Principal, requestHash [32]byte, now int64) error {
	materialHash, err := repository.keys.materialDigest(caseRow.id, requestHash)
	if err != nil {
		return err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM report_materials WHERE case_id=? AND material_hash=?)`, caseRow.id, materialHash[:]).Scan(&exists); err != nil {
		return fmt.Errorf("reports: inspect material duplicate: %w", err)
	}
	if exists == 1 {
		return nil
	}
	if caseRow.materialVersion >= int64(^uint64(0)>>1) {
		return ErrInvariant
	}
	if err := repository.insertMaterialWithHashTx(ctx, tx, caseRow.id, materialHash, submission, principal, now); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE report_cases
SET material_version=material_version+1,material_count=material_count+1
WHERE id=? AND status IN ('pending_indexing','pending_review') AND material_version=?`, caseRow.id, caseRow.materialVersion)
	if err != nil {
		return fmt.Errorf("reports: advance material version: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

func (repository *Repository) insertMaterialTx(ctx context.Context, tx *sql.Tx, caseRow activeCase, submission PublicSubmission, principal *authz.Principal, requestHash [32]byte, now int64) error {
	materialHash, err := repository.keys.materialDigest(caseRow.id, requestHash)
	if err != nil {
		return err
	}
	return repository.insertMaterialWithHashTx(ctx, tx, caseRow.id, materialHash, submission, principal, now)
}

func (repository *Repository) insertMaterialWithHashTx(ctx context.Context, tx *sql.Tx, caseID string, materialHash [32]byte, submission PublicSubmission, principal *authz.Principal, now int64) error {
	envelope, err := repository.keys.sealSourceIP(caseID, materialHash, submission.SourceIP)
	if err != nil {
		return ErrUnavailable
	}
	defer clear(envelope)
	var reporterUser, reporterDiscord any
	if principal != nil {
		reporterUser = principal.UserID
		reporterDiscord = principal.DiscordID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO report_materials(
 case_id,material_hash,note_text,reporter_user_id,reporter_discord_id,source_ip_envelope,created_at)
VALUES(?,?,?,?,?,?,?)`, caseID, materialHash[:], submission.Note, reporterUser, reporterDiscord, envelope, now); err != nil {
		return fmt.Errorf("reports: insert material: %w", err)
	}
	return nil
}
