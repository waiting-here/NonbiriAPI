package adminalerts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// TargetDiagnostic exposes only fixed operational facts for an exact alert
// target. Game inputs, randomness, identities of other players and secrets are
// deliberately absent from this projection.
type TargetDiagnostic struct {
	Kind            string      `json:"kind"`
	ID              string      `json:"id"`
	Facts           []AlertFact `json:"facts"`
	RelatedIssueIDs []string    `json:"related_issue_ids"`
}

func diagnosticTarget(kind, id string) bool {
	switch kind {
	case "issue", "fishing_batch", "rps_session":
		return targetIDValid(kind, id)
	case "issue_user":
		return targetIDValid("user", id)
	default:
		return false
	}
}

func diagnosticFact(key, value string) AlertFact { return AlertFact{Key: key, Value: value} }
func diagnosticNumber(key string, value int64) AlertFact {
	return diagnosticFact(key, strconv.FormatInt(value, 10))
}

// GetTargetDiagnostic uses the same final administrator authorization as alert
// detail, in the read transaction that looks up the target. Every query names
// one fixed table and one validated opaque ID.
func (repository *Repository) GetTargetDiagnostic(ctx context.Context, adminID int64, kind, id string) (TargetDiagnostic, error) {
	if repository == nil || ctx == nil || !diagnosticTarget(kind, id) {
		return TargetDiagnostic{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := repository.beginAuthorized(ctx, adminID, true)
	if err != nil {
		return TargetDiagnostic{}, err
	}
	committed := false
	defer finishTransaction(tx, &committed)
	result := TargetDiagnostic{Kind: kind, ID: id, Facts: []AlertFact{}, RelatedIssueIDs: []string{}}
	switch kind {
	case "issue_user":
		var userID int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=?`, id).Scan(&userID)
		if err != nil {
			break
		}
		var alertExists int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM admin_alerts
WHERE kind='issue_projection_incomplete' AND subject_user_id=? LIMIT 1`, userID).Scan(&alertExists)
		if err != nil {
			break
		}
		phase := "not_recorded"
		var generation, updatedAt int64
		var incomplete int
		var cursor sql.NullString
		stateErr := tx.QueryRowContext(ctx, `SELECT projection_incomplete,rebuild_generation,rebuild_cursor,updated_at FROM user_issue_projection_state WHERE user_id=?`, userID).
			Scan(&incomplete, &generation, &cursor, &updatedAt)
		if stateErr != nil && !errors.Is(stateErr, sql.ErrNoRows) {
			err = stateErr
			break
		}
		if stateErr == nil {
			switch incomplete {
			case 0:
				phase = "complete"
			case 1:
				phase = "incomplete"
				if cursor.Valid {
					phase = "checkpointed"
				}
			default:
				return TargetDiagnostic{}, ErrInvariant
			}
		}
		now, clockErr := repository.nowUnix()
		if clockErr != nil {
			return TargetDiagnostic{}, clockErr
		}
		var retained, current int64
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN state='current' THEN 1 ELSE 0 END),0)
FROM user_issues WHERE user_id=? AND (state='current' OR retain_until>?)`, userID, now).Scan(&retained, &current)
		if err != nil {
			break
		}
		result.Facts = append(result.Facts,
			diagnosticNumber("user_id", userID), diagnosticFact("projection_phase", phase),
			diagnosticNumber("retained_issue_count", retained), diagnosticNumber("current_issue_count", current))
		if stateErr == nil {
			result.Facts = append(result.Facts, diagnosticNumber("rebuild_generation", generation), diagnosticNumber("projection_updated_at", updatedAt))
		}
		rows, queryErr := tx.QueryContext(ctx, `SELECT id FROM user_issues
WHERE user_id=? AND (state='current' OR retain_until>?)
ORDER BY last_seen_at DESC,id DESC LIMIT 20`, userID, now)
		if queryErr != nil {
			err = queryErr
			break
		}
		for rows.Next() {
			var issueID string
			if err = rows.Scan(&issueID); err != nil {
				break
			}
			if !targetIDValid("issue", issueID) {
				err = ErrInvariant
				break
			}
			result.RelatedIssueIDs = append(result.RelatedIssueIDs, issueID)
		}
		if err == nil {
			err = rows.Err()
		}
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	case "issue":
		var userID, firstAt, lastAt, count int64
		var source, resourceKind, cause, state string
		var closedAt sql.NullInt64
		err = tx.QueryRowContext(ctx, `SELECT user_id,source,resource_kind,root_cause,state,first_seen_at,last_seen_at,count,closed_at FROM user_issues WHERE id=?`, id).
			Scan(&userID, &source, &resourceKind, &cause, &state, &firstAt, &lastAt, &count, &closedAt)
		if err == nil {
			result.Facts = append(result.Facts,
				diagnosticNumber("user_id", userID), diagnosticFact("source", source), diagnosticFact("resource_kind", resourceKind),
				diagnosticFact("root_cause", cause), diagnosticFact("state", state),
				diagnosticNumber("first_seen_at", firstAt), diagnosticNumber("last_seen_at", lastAt), diagnosticNumber("count", count))
			if closedAt.Valid {
				result.Facts = append(result.Facts, diagnosticNumber("closed_at", closedAt.Int64))
			}
		}
	case "fishing_batch":
		var userID, attempts, createdAt, exhausted int64
		var state string
		var nextAt, settledAt sql.NullInt64
		var lastError sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT user_id,state,attempt_count,next_attempt_at,last_error_class,retry_exhausted,created_at,settled_at FROM game_fishing_batches WHERE id=?`, id).
			Scan(&userID, &state, &attempts, &nextAt, &lastError, &exhausted, &createdAt, &settledAt)
		if err == nil {
			result.Facts = append(result.Facts,
				diagnosticNumber("user_id", userID), diagnosticFact("state", state), diagnosticNumber("attempt_count", attempts),
				diagnosticFact("retry_exhausted", strconv.FormatBool(exhausted == 1)), diagnosticNumber("created_at", createdAt))
			if nextAt.Valid {
				result.Facts = append(result.Facts, diagnosticNumber("next_attempt_at", nextAt.Int64))
			}
			if lastError.Valid {
				result.Facts = append(result.Facts, diagnosticFact("last_error_class", lastError.String))
			}
			if settledAt.Valid {
				result.Facts = append(result.Facts, diagnosticNumber("settled_at", settledAt.Int64))
			}
		}
	case "rps_session":
		var mode, state, phase string
		var attempts []byte
		var nextAt sql.NullInt64
		var lastError, terminalReason sql.NullString
		var startedAt int64
		err = tx.QueryRowContext(ctx, `SELECT mode,state,phase,terminal_retry_attempt_count,terminal_next_retry_at,terminal_last_error_class,started_at,terminal_reason FROM game_rps_sessions WHERE id=?`, id).
			Scan(&mode, &state, &phase, &attempts, &nextAt, &lastError, &startedAt, &terminalReason)
		if errors.Is(err, sql.ErrNoRows) {
			var terminalAt int64
			var reason string
			err = tx.QueryRowContext(ctx, `SELECT mode,terminal_reason,started_at,terminal_at FROM game_rps_summaries WHERE session_id=?`, id).
				Scan(&mode, &reason, &startedAt, &terminalAt)
			if err == nil {
				result.Facts = append(result.Facts, diagnosticFact("mode", mode), diagnosticFact("state", "completed"),
					diagnosticFact("terminal_reason", reason), diagnosticNumber("started_at", startedAt), diagnosticNumber("terminal_at", terminalAt))
			}
		} else if err == nil {
			count, decodeErr := db.DecodeU128(attempts)
			if decodeErr != nil {
				return TargetDiagnostic{}, fmt.Errorf("administrator alerts: invalid RPS retry count: %w", decodeErr)
			}
			result.Facts = append(result.Facts, diagnosticFact("mode", mode), diagnosticFact("state", state), diagnosticFact("phase", phase),
				diagnosticFact("retry_attempts", count.Decimal()), diagnosticNumber("started_at", startedAt))
			if nextAt.Valid {
				result.Facts = append(result.Facts, diagnosticNumber("next_attempt_at", nextAt.Int64))
			}
			if lastError.Valid {
				result.Facts = append(result.Facts, diagnosticFact("last_error_class", lastError.String))
			}
			if terminalReason.Valid {
				result.Facts = append(result.Facts, diagnosticFact("terminal_reason", terminalReason.String))
			}
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return TargetDiagnostic{}, ErrNotFound
	}
	if err != nil {
		return TargetDiagnostic{}, fmt.Errorf("administrator alerts: read %s diagnostic: %w", kind, err)
	}
	for _, fact := range result.Facts {
		if !validFact(fact) {
			return TargetDiagnostic{}, ErrInvariant
		}
	}
	if err := commitTransaction(tx, &committed); err != nil {
		return TargetDiagnostic{}, err
	}
	return result, nil
}

func (api *httpAPI) targetDiagnostic(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	writer.Header().Set("Cache-Control", "no-store")
	kind, id := request.PathValue("kind"), request.PathValue("target_id")
	if !diagnosticTarget(kind, id) {
		writeError(writer, ErrInvalidRequest)
		return
	}
	if !requireNoBody(writer, request) || !requireEmptyQuery(writer, request) {
		return
	}
	value, err := api.repository.GetTargetDiagnostic(request.Context(), principal.UserID, kind, id)
	if err != nil {
		writeError(writer, err)
		return
	}
	httperr.WriteJSON(writer, http.StatusOK, value)
}
