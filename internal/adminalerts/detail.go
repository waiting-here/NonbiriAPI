package adminalerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// AlertDetail keeps immutable event facts separate from live resource state.
// Resource IDs are data, never URLs supplied by an upstream or an alert ref.
type AlertDetail struct {
	Alert          AdminAlert      `json:"alert"`
	ContextVersion int             `json:"context_version"`
	OccurredFacts  []AlertFact     `json:"occurred_facts"`
	Targets        []AlertTarget   `json:"targets"`
	CurrentState   []AlertFact     `json:"current_state"`
	RelatedLogs    *AlertLogWindow `json:"related_logs"`
	ResolutionKind string          `json:"resolution_kind"`
}

type AlertFact struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type AlertTarget struct {
	Kind              string `json:"kind"`
	ID                string `json:"id"`
	Available         bool   `json:"available"`
	Status            string `json:"status"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

type AlertLogWindow struct {
	EndpointKeyID     string `json:"endpoint_key_id"`
	From              int64  `json:"from"`
	To                int64  `json:"to"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

type alertEventContext struct {
	Targets       []alertEventTarget `json:"targets"`
	OccurredFacts []AlertFact        `json:"occurred_facts"`
}

type alertEventTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

var (
	donationGenerationRef = regexp.MustCompile(`^donation-key:([1-9][0-9]{0,18}):generation:([1-9][0-9]*):fold:([1-9][0-9]*)$`)
	donationPolicyRef     = regexp.MustCompile(`^donation-key:([1-9][0-9]{0,18}):policy:([1-9][0-9]{0,18})$`)
)

func targetIDValid(kind, id string) bool {
	switch kind {
	case "deleted_account", "donation", "donation_key", "endpoint_key", "endpoint", "user":
		value, err := strconv.ParseInt(id, 10, 64)
		return err == nil && value > 0 && strconv.FormatInt(value, 10) == id
	case "report_case":
		return db.ValidateOpaqueID(id, "rpc_")
	case "issue":
		return db.ValidateOpaqueID(id, "iss_")
	case "fishing_batch":
		return db.ValidateOpaqueID(id, "fb_")
	case "rps_session":
		return db.ValidateOpaqueID(id, "rps_")
	case "request_log":
		return db.ValidateOpaqueID(id, "req_")
	case "maintenance_event":
		return db.ValidateOpaqueID(id, "op_")
	case "worker_checkpoint":
		return id == "lifecycle_recovery_v1" || id == "lifecycle_retention_v1"
	default:
		return false
	}
}

func validFact(fact AlertFact) bool {
	if len(fact.Key) == 0 || len(fact.Key) > 64 || len(fact.Value) > 1024 || !validAlertText(fact.Value, 1024) {
		return false
	}
	for _, letter := range fact.Key {
		if letter != '_' && (letter < 'a' || letter > 'z') && (letter < '0' || letter > '9') {
			return false
		}
	}
	return true
}

func decodeEventContext(version int, body string) (alertEventContext, error) {
	if version == 0 {
		return alertEventContext{Targets: []alertEventTarget{}, OccurredFacts: []AlertFact{}}, nil
	}
	if version != 1 || len(body) > 16384 {
		return alertEventContext{}, ErrInvariant
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	var event alertEventContext
	if err := decoder.Decode(&event); err != nil {
		return alertEventContext{}, ErrInvariant
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || len(event.Targets) > 8 || len(event.OccurredFacts) > 32 {
		return alertEventContext{}, ErrInvariant
	}
	seen := make(map[string]bool, len(event.Targets))
	for _, target := range event.Targets {
		key := target.Kind + ":" + target.ID
		if !targetIDValid(target.Kind, target.ID) || seen[key] {
			return alertEventContext{}, ErrInvariant
		}
		seen[key] = true
	}
	for _, fact := range event.OccurredFacts {
		if !validFact(fact) {
			return alertEventContext{}, ErrInvariant
		}
	}
	if event.Targets == nil {
		event.Targets = []alertEventTarget{}
	}
	if event.OccurredFacts == nil {
		event.OccurredFacts = []AlertFact{}
	}
	return event, nil
}

func legacyEvent(kind Kind, ref string, subject *string, alertID string) alertEventContext {
	event := alertEventContext{Targets: []alertEventTarget{}, OccurredFacts: []AlertFact{}}
	add := func(targetKind, id string) {
		if targetIDValid(targetKind, id) {
			event.Targets = append(event.Targets, alertEventTarget{Kind: targetKind, ID: id})
		}
	}
	switch kind {
	case KindAccountDeleted:
		add("deleted_account", alertID)
	case KindDonationFailureDisabled:
		if match := donationGenerationRef.FindStringSubmatch(ref); match != nil {
			add("donation_key", match[1])
			event.OccurredFacts = append(event.OccurredFacts, AlertFact{Key: "generation", Value: match[2]}, AlertFact{Key: "fold", Value: match[3]})
		} else if match := donationPolicyRef.FindStringSubmatch(ref); match != nil {
			add("donation_key", match[1])
			event.OccurredFacts = append(event.OccurredFacts, AlertFact{Key: "policy_revision", Value: match[2]})
		}
	case KindReportRetryExhausted:
		add("report_case", ref)
	case KindIssueProjectionIncomplete:
		if targetIDValid("issue", ref) {
			add("issue", ref)
		} else if subject != nil {
			add("user", *subject)
		}
	case KindMaintenanceEnabled:
		add("maintenance_event", ref)
	case KindFishingRetryExhausted:
		add("fishing_batch", ref)
	case KindRPSTerminalRetrying:
		add("rps_session", ref)
	case KindWorkerCheckpointFailed:
		add("worker_checkpoint", ref)
	case KindInvariantViolation:
		add("report_case", ref)
		add("rps_session", ref)
	case KindForwardError:
		add("request_log", ref)
	}
	return event
}

func (repository *Repository) GetDetail(ctx context.Context, adminID, alertID int64) (AlertDetail, error) {
	if repository == nil || ctx == nil || alertID <= 0 {
		return AlertDetail{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := repository.beginAuthorized(ctx, adminID, true)
	if err != nil {
		return AlertDetail{}, err
	}
	committed := false
	defer finishTransaction(tx, &committed)
	var version int
	var body, resolution string
	var row rawAlert
	err = tx.QueryRowContext(ctx, `SELECT id,kind,message,ref,subject_user_id,created_at,resolved,resolved_at,context_version,context_json,resolution_kind FROM admin_alerts WHERE id=?`, alertID).
		Scan(&row.id, &row.kind, &row.message, &row.ref, &row.subjectUserID, &row.createdAt, &row.resolved, &row.resolvedAt, &version, &body, &resolution)
	if errors.Is(err, sql.ErrNoRows) {
		return AlertDetail{}, ErrNotFound
	}
	if err != nil {
		return AlertDetail{}, fmt.Errorf("administrator alerts: read detail: %w", err)
	}
	alert, err := projectAlert(row)
	if err != nil {
		return AlertDetail{}, err
	}
	if alert.Kind == KindAccountDeleted {
		alert.AccountDeletion, err = deletionSnapshot(ctx, tx, row.id)
		if err != nil {
			return AlertDetail{}, err
		}
	}
	if resolution != "" && resolution != "manual" && resolution != "automatic_blacklist" && resolution != "worker_recovered" && resolution != "legacy" {
		return AlertDetail{}, ErrInvariant
	}
	event, err := decodeEventContext(version, body)
	if err != nil {
		return AlertDetail{}, err
	}
	if version == 0 {
		event = legacyEvent(alert.Kind, row.ref, alert.SubjectUserID, alert.ID)
	}
	detail := AlertDetail{
		Alert: alert, ContextVersion: version, OccurredFacts: event.OccurredFacts,
		Targets: make([]AlertTarget, 0, len(event.Targets)+2), CurrentState: []AlertFact{},
		ResolutionKind: resolution,
	}
	if version == 0 {
		addLegacyFacts(&detail)
	}
	for _, target := range event.Targets {
		projected, err := readTarget(ctx, tx, target)
		if err != nil {
			return AlertDetail{}, err
		}
		detail.Targets = append(detail.Targets, projected)
	}
	if alert.Kind == KindDonationFailureDisabled {
		now, err := repository.nowUnix()
		if err != nil {
			return AlertDetail{}, err
		}
		if err := enrichDonationDetail(ctx, tx, &detail, now); err != nil {
			return AlertDetail{}, err
		}
	}
	if alert.Kind == KindWorkerCheckpointFailed {
		if err := enrichWorkerDetail(ctx, tx, &detail); err != nil {
			return AlertDetail{}, err
		}
	}
	if alert.Kind == KindMaintenanceEnabled {
		var enabled int
		if err := tx.QueryRowContext(ctx, `SELECT enabled FROM maintenance_state WHERE id=1`).Scan(&enabled); err != nil {
			return AlertDetail{}, err
		}
		detail.CurrentState = append(detail.CurrentState, AlertFact{Key: "maintenance_enabled", Value: strconv.FormatBool(enabled == 1)})
	}
	if err := commitTransaction(tx, &committed); err != nil {
		return AlertDetail{}, err
	}
	return detail, nil
}

func addLegacyFacts(detail *AlertDetail) {
	if detail.Alert.Kind == KindMaintenanceEnabled {
		var value struct {
			Role       string `json:"role"`
			Reason     string `json:"reason"`
			OccurredAt int64  `json:"occurred_at"`
			Result     string `json:"result"`
		}
		if json.Unmarshal([]byte(detail.Alert.Message), &value) == nil && value.Result == "enabled" {
			detail.OccurredFacts = append(detail.OccurredFacts, AlertFact{Key: "actor_role", Value: value.Role}, AlertFact{Key: "reason", Value: value.Reason})
		}
	}
	if detail.Alert.Kind == KindReportRetryExhausted {
		parts := strings.Split(detail.Alert.Message, ":")
		if len(parts) == 3 && parts[2] == "attempt_10" && validFact(AlertFact{Key: "operation", Value: parts[0]}) && validFact(AlertFact{Key: "error_class", Value: parts[1]}) {
			detail.OccurredFacts = append(detail.OccurredFacts, AlertFact{Key: "operation", Value: parts[0]}, AlertFact{Key: "error_class", Value: parts[1]}, AlertFact{Key: "attempts", Value: "10"})
		}
	}
}

func readTarget(ctx context.Context, tx *sql.Tx, target alertEventTarget) (AlertTarget, error) {
	result := AlertTarget{Kind: target.Kind, ID: target.ID, UnavailableReason: "not_found"}
	var statement string
	switch target.Kind {
	case "deleted_account":
		statement = `SELECT 'retained' FROM admin_account_deletions WHERE alert_id=?`
	case "donation":
		statement = `SELECT status FROM donations WHERE id=?`
	case "donation_key":
		statement = `SELECT CASE WHEN ended_at IS NOT NULL THEN 'ended' WHEN failure_disabled=1 THEN 'failure_disabled' WHEN enabled=0 THEN 'disabled' ELSE 'enabled' END FROM donation_keys WHERE id=?`
	case "endpoint_key":
		statement = `SELECT CASE WHEN k.enabled=0 THEN 'disabled' WHEN e.enabled=0 THEN 'endpoint_disabled' ELSE 'enabled' END FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=?`
	case "endpoint":
		statement = `SELECT CASE WHEN enabled=1 THEN 'enabled' ELSE 'disabled' END FROM endpoints WHERE id=?`
	case "user":
		statement = `SELECT CASE WHEN is_banned=1 THEN 'banned' ELSE 'active' END FROM users WHERE id=?`
	case "report_case":
		statement = `SELECT status FROM report_cases WHERE id=?`
	case "issue":
		statement = `SELECT state FROM user_issues WHERE id=?`
	case "fishing_batch":
		statement = `SELECT state FROM game_fishing_batches WHERE id=?`
	case "rps_session":
		statement = `SELECT state FROM game_rps_sessions WHERE id=?`
	case "request_log":
		statement = `SELECT COALESCE(caller_result_class,'pending') FROM request_logs WHERE logical_request_id=?`
	case "maintenance_event":
		statement = `SELECT CASE WHEN resolved_at IS NULL THEN 'active' ELSE 'ended' END FROM maintenance_events WHERE id=?`
	case "worker_checkpoint":
		statement = `SELECT CASE WHEN attempt_count>0 THEN 'retrying' ELSE 'scheduled' END FROM worker_checkpoints WHERE worker_key=?`
	default:
		return AlertTarget{}, ErrInvariant
	}
	err := tx.QueryRowContext(ctx, statement, target.ID).Scan(&result.Status)
	if errors.Is(err, sql.ErrNoRows) && target.Kind == "rps_session" {
		err = tx.QueryRowContext(ctx, `SELECT 'completed' FROM game_rps_summaries WHERE session_id=?`, target.ID).Scan(&result.Status)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return AlertTarget{}, fmt.Errorf("administrator alerts: read target %s: %w", target.Kind, err)
	}
	result.Available = true
	result.UnavailableReason = ""
	return result, nil
}

func enrichDonationDetail(ctx context.Context, tx *sql.Tx, detail *AlertDetail, now int64) error {
	var keyID string
	for _, target := range detail.Targets {
		if target.Kind == "donation_key" {
			keyID = target.ID
			break
		}
	}
	if keyID == "" {
		return nil
	}
	var donationID, sourceKeyID int64
	var endpointKeyID sql.NullInt64
	var currentStreak []byte
	var currentThreshold string
	var currentGeneration []byte
	err := tx.QueryRowContext(ctx, `SELECT donation_id,endpoint_key_id,source_endpoint_key_id,failure_streak,failure_disable_threshold,streak_generation FROM donation_keys WHERE id=?`, keyID).
		Scan(&donationID, &endpointKeyID, &sourceKeyID, &currentStreak, &currentThreshold, &currentGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("administrator alerts: read donation key context: %w", err)
	}
	streak, err := db.DecodeU128(currentStreak)
	if err != nil {
		return ErrInvariant
	}
	generation, err := db.DecodeU128(currentGeneration)
	if err != nil {
		return ErrInvariant
	}
	detail.CurrentState = append(detail.CurrentState,
		AlertFact{Key: "current_failure_streak", Value: streak.Decimal()},
		AlertFact{Key: "current_failure_threshold", Value: currentThreshold},
		AlertFact{Key: "current_generation", Value: generation.Decimal()},
	)
	verifiedDonationID := strconv.FormatInt(donationID, 10)
	verifiedEndpointID := ""
	if endpointKeyID.Valid && endpointKeyID.Int64 == sourceKeyID {
		verifiedEndpointID = strconv.FormatInt(endpointKeyID.Int64, 10)
	}
	// A stored event describes what was believed at occurrence. Only the
	// current key association may authorize a live navigation target.
	for index := range detail.Targets {
		target := &detail.Targets[index]
		if target.Kind == "donation" && target.ID != verifiedDonationID ||
			target.Kind == "endpoint_key" && target.ID != verifiedEndpointID {
			target.Available = false
			target.Status = ""
			target.UnavailableReason = "association_changed"
		}
	}
	if !hasTarget(detail.Targets, "donation", verifiedDonationID) && len(detail.Targets) < 8 {
		donation, err := readTarget(ctx, tx, alertEventTarget{Kind: "donation", ID: strconv.FormatInt(donationID, 10)})
		if err != nil {
			return err
		}
		detail.Targets = append(detail.Targets, donation)
	}
	if len(detail.Targets) >= 8 {
		return nil
	}
	// source_endpoint_key_id is a historical marker. It is never used as a
	// live link after endpoint_key_id is cleared on deletion.
	if verifiedEndpointID == "" {
		detail.CurrentState = append(detail.CurrentState, AlertFact{Key: "endpoint_key", Value: "unavailable"})
		return nil
	}
	endpointID := verifiedEndpointID
	endpoint, err := readTarget(ctx, tx, alertEventTarget{Kind: "endpoint_key", ID: endpointID})
	if err != nil {
		return err
	}
	if !hasTarget(detail.Targets, "endpoint_key", endpointID) {
		detail.Targets = append(detail.Targets, endpoint)
	}
	if !endpoint.Available {
		return nil
	}
	from, to := detail.Alert.CreatedAt-900, detail.Alert.CreatedAt+300
	if from < 0 {
		from = 0
	}
	if to > maxUnixSecond {
		to = maxUnixSecond
	}
	window := &AlertLogWindow{EndpointKeyID: endpointID, From: from, To: to, UnavailableReason: "not_retained"}
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM request_logs l
 WHERE l.started_at>=? AND l.started_at<? AND (l.completed_at IS NULL OR l.completed_at>?)
 AND EXISTS(SELECT 1 FROM request_attempts a WHERE a.request_log_id=l.id AND a.endpoint_key_id_snapshot=?) LIMIT 1)`, from, to, now-30*24*60*60, endpointKeyID.Int64).Scan(&exists)
	if err != nil {
		return fmt.Errorf("administrator alerts: inspect related logs: %w", err)
	}
	window.Available = exists == 1
	if window.Available {
		window.UnavailableReason = ""
	}
	detail.RelatedLogs = window
	return nil
}

func hasTarget(targets []AlertTarget, kind, id string) bool {
	for _, target := range targets {
		if target.Kind == kind && target.ID == id {
			return true
		}
	}
	return false
}

func enrichWorkerDetail(ctx context.Context, tx *sql.Tx, detail *AlertDetail) error {
	for _, target := range detail.Targets {
		if target.Kind != "worker_checkpoint" || !target.Available {
			continue
		}
		var attempts int64
		var nextAt, updatedAt int64
		var lastError string
		var lastSuccess sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT attempt_count,next_attempt_at,last_error_class,updated_at,last_success_at FROM worker_checkpoints WHERE worker_key=?`, target.ID).
			Scan(&attempts, &nextAt, &lastError, &updatedAt, &lastSuccess); err != nil {
			return fmt.Errorf("administrator alerts: read worker state: %w", err)
		}
		lastSuccessText := "unknown"
		if lastSuccess.Valid {
			lastSuccessText = strconv.FormatInt(lastSuccess.Int64, 10)
		}
		detail.CurrentState = append(detail.CurrentState,
			AlertFact{Key: "attempt_count", Value: strconv.FormatInt(attempts, 10)},
			AlertFact{Key: "next_attempt_at", Value: strconv.FormatInt(nextAt, 10)},
			AlertFact{Key: "last_error_class", Value: lastError},
			AlertFact{Key: "checkpoint_updated_at", Value: strconv.FormatInt(updatedAt, 10)},
			AlertFact{Key: "last_success_at", Value: lastSuccessText},
		)
		break
	}
	return nil
}
