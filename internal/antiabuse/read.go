package antiabuse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

var ErrNotFound = errors.New("penalty history unavailable")
var ErrInvalid = errors.New("invalid penalty query")

type Page[T any] struct {
	Data       []T                 `json:"data"`
	Pagination pagination.Metadata `json:"pagination"`
}
type Case struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	ReasonCode string `json:"reason_code"`
	StartedAt  int64  `json:"started_at"`
	EndsAt     *int64 `json:"ends_at"`
	EndedAt    *int64 `json:"ended_at"`
	State      string `json:"state"`
	Result     string `json:"result"`
}
type CasePage struct {
	Page[Case]
	LegacyDetailsUnavailable bool `json:"legacy_details_unavailable"`
}
type Action struct {
	ID                  string  `json:"id"`
	Action              string  `json:"action"`
	OccurredAt          int64   `json:"occurred_at"`
	RequestID           *string `json:"request_id"`
	RequestLogAvailable bool    `json:"request_log_available"`
	OperationID         *string `json:"operation_id"`
	ActorUserID         *string `json:"actor_user_id"`
	PreviousEndsAt      *int64  `json:"previous_ends_at"`
	EndsAt              *int64  `json:"ends_at"`
	ReasonCode          string  `json:"reason_code"`
	EvidenceCount       int     `json:"evidence_count"`
}
type CaseDetail struct {
	Case    Case         `json:"case"`
	Actions Page[Action] `json:"actions"`
}
type EvidenceMember struct {
	OccurredAt          int64  `json:"occurred_at"`
	RequestID           string `json:"request_id"`
	Kind                string `json:"violation_kind"`
	ContentChars        *int   `json:"content_chars"`
	RequestLogAvailable bool   `json:"request_log_available"`
}
type EvidencePage struct {
	Rules      json.RawMessage      `json:"rules"`
	Statistics json.RawMessage      `json:"statistics"`
	Members    Page[EvidenceMember] `json:"members"`
}

// Retention predicates are applied in every authorized snapshot, independently
// of physical cleanup. Natural expiry uses the actual end, never cleanup time.
const liveCase = `(c.state='active' AND (c.ends_at IS NULL OR c.ends_at>?))`
const retainedCase = `(c.state='active' AND (c.ends_at IS NULL OR c.ends_at>?) OR c.state='ended' AND c.ended_at>? OR c.state='active' AND c.ends_at>?)`
const caseColumns = `c.id,c.kind,c.reason_code,c.started_at,c.ends_at,CASE WHEN c.state='active' AND c.ends_at<=? THEN c.ends_at ELSE c.ended_at END,CASE WHEN c.state='active' AND c.ends_at<=? THEN 'ended' ELSE c.state END,CASE WHEN c.state='active' AND c.ends_at<=? THEN 'expired' ELSE c.result END`

type scanner interface{ Scan(...any) error }

func scanCase(row scanner) (Case, error) {
	var c Case
	err := row.Scan(&c.ID, &c.Kind, &c.ReasonCode, &c.StartedAt, &c.EndsAt, &c.EndedAt, &c.State, &c.Result)
	return c, err
}
func retentionArgs(now int64) []any {
	return []any{now, now - RetentionSeconds, now - RetentionSeconds}
}
func requireOwner(ctx context.Context, tx *sql.Tx, user int64) error {
	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0)`, user).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// ReadCasesTx requires the caller's management authorization in the same tx.
func ReadCasesTx(ctx context.Context, tx *sql.Tx, user, now int64, page pagination.Request, kind, state string) (CasePage, error) {
	result := CasePage{Page: Page[Case]{Data: []Case{}}}
	if !page.Valid() || user <= 0 || now < 0 || kind != "" && kind != "deduction" && kind != "ban" && kind != "charity_suspend" || state != "" && state != "active" && state != "ended" {
		return result, ErrInvalid
	}
	if err := requireOwner(ctx, tx, user); err != nil {
		return result, err
	}
	where := `c.user_id=? AND ` + retainedCase
	args := append([]any{user}, retentionArgs(now)...)
	if kind != "" {
		where += ` AND c.kind=?`
		args = append(args, kind)
	}
	if state != "" {
		if state == "active" {
			where += ` AND ` + liveCase
		} else {
			where += ` AND NOT ` + liveCase
		}
		args = append(args, now)
	}
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM abuse_cases c WHERE `+where, args...).Scan(&total); err != nil {
		return result, err
	}
	meta, offset, err := page.Window(total)
	if err != nil {
		return result, err
	}
	result.Pagination = meta
	selectArgs := append([]any{now, now, now}, args...)
	selectArgs = append(selectArgs, page.Size, offset)
	rows, err := tx.QueryContext(ctx, `SELECT `+caseColumns+` FROM abuse_cases c WHERE `+where+` ORDER BY c.started_at DESC,(SELECT max(seq) FROM abuse_actions a WHERE a.case_id=c.id) DESC LIMIT ? OFFSET ?`, selectArgs...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			rows.Close()
			return result, err
		}
		result.Data = append(result.Data, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	err = tx.QueryRowContext(ctx, `SELECT u.auto_banned=1 AND u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) AND NOT EXISTS(SELECT 1 FROM abuse_cases WHERE user_id=u.id AND kind='ban') FROM users u WHERE u.id=?`, now, user).Scan(&result.LegacyDetailsUnavailable)
	return result, err
}
func ReadCaseTx(ctx context.Context, tx *sql.Tx, user, now int64, id string, page pagination.Request) (CaseDetail, error) {
	result := CaseDetail{Actions: Page[Action]{Data: []Action{}}}
	if !page.Valid() || !db.ValidateOpaqueID(id, "abc_") {
		return result, ErrNotFound
	}
	args := append([]any{now, now, now, user, id}, retentionArgs(now)...)
	c, err := scanCase(tx.QueryRowContext(ctx, `SELECT `+caseColumns+` FROM abuse_cases c JOIN users u ON u.id=c.user_id AND u.is_admin=0 WHERE c.user_id=? AND c.id=? AND `+retainedCase, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	result.Case = c
	var total int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM abuse_actions WHERE case_id=?`, id).Scan(&total); err != nil {
		return result, err
	}
	meta, offset, err := page.Window(total)
	if err != nil {
		return result, err
	}
	result.Actions.Pagination = meta
	rows, err := tx.QueryContext(ctx, `SELECT a.seq,a.action,a.occurred_at,a.request_id,EXISTS(SELECT 1 FROM request_logs l WHERE l.logical_request_id=a.request_id AND l.user_id=? AND l.started_at>?),a.operation_id,a.actor_user_id,a.previous_ends_at,a.ends_at,a.reason_code,a.evidence_count FROM abuse_actions a WHERE a.case_id=? ORDER BY a.seq DESC LIMIT ? OFFSET ?`, user, now-30*24*60*60, id, page.Size, offset)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Action
		var seq int64
		var actor sql.NullInt64
		if err := rows.Scan(&seq, &a.Action, &a.OccurredAt, &a.RequestID, &a.RequestLogAvailable, &a.OperationID, &actor, &a.PreviousEndsAt, &a.EndsAt, &a.ReasonCode, &a.EvidenceCount); err != nil {
			return result, err
		}
		a.ID = strconv.FormatInt(seq, 10)
		if actor.Valid {
			a.ActorUserID = new(strconv.FormatInt(actor.Int64, 10))
		}
		result.Actions.Data = append(result.Actions.Data, a)
	}
	return result, rows.Err()
}
func ReadEvidenceTx(ctx context.Context, tx *sql.Tx, user, now int64, id string, action int64, page pagination.Request) (EvidencePage, error) {
	result := EvidencePage{Members: Page[EvidenceMember]{Data: []EvidenceMember{}}}
	if !page.Valid() || !db.ValidateOpaqueID(id, "abc_") || action <= 0 {
		return result, ErrNotFound
	}
	args := append([]any{user, id, action}, retentionArgs(now)...)
	var rules, stats string
	var total int64
	err := tx.QueryRowContext(ctx, `SELECT a.rules_json,a.statistics_json,a.evidence_count FROM abuse_actions a JOIN abuse_cases c ON c.id=a.case_id JOIN users u ON u.id=c.user_id AND u.is_admin=0 WHERE c.user_id=? AND c.id=? AND a.seq=? AND `+retainedCase, args...).Scan(&rules, &stats, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	result.Rules, result.Statistics = json.RawMessage(rules), json.RawMessage(stats)
	meta, offset, err := page.Window(total)
	if err != nil {
		return result, err
	}
	result.Members.Pagination = meta
	rows, err := tx.QueryContext(ctx, `SELECT e.occurred_at,e.request_id,e.violation_kind,e.content_chars,EXISTS(SELECT 1 FROM request_logs l WHERE l.logical_request_id=e.request_id AND l.user_id=? AND l.started_at>?) FROM abuse_evidence e WHERE e.action_seq=? ORDER BY e.ordinal LIMIT ? OFFSET ?`, user, now-30*24*60*60, action, page.Size, offset)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var e EvidenceMember
		if err := rows.Scan(&e.OccurredAt, &e.RequestID, &e.Kind, &e.ContentChars, &e.RequestLogAvailable); err != nil {
			return result, err
		}
		result.Members.Data = append(result.Members.Data, e)
	}
	return result, rows.Err()
}
