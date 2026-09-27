// Package clientguard applies administrator-bound client rules to new charity
// calls before claim acceptance. Historical audit scans never call this service.
package clientguard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/riskaudit"
	"github.com/waiting-here/NonbiriAPI/internal/useractivity"
)

var (
	ErrInvalid     = errors.New("invalid client guard call")
	ErrUnavailable = errors.New("client guard unavailable")
	ErrInvariant   = errors.New("client guard invariant violation")
)

const receiptLifetime = 90 * 24 * 60 * 60
const maxUnixSecond = 253402300799

type Retirement interface {
	Commit() bool
	Abort() bool
}

type RejectionRecorder interface {
	RecordRejectionTx(context.Context, *sql.Tx, int64, requestattempt.Fact, int64) error
}

type Config struct {
	Database   *sql.DB
	Rejections RejectionRecorder
	// The gate must serialize stable identity and user retirement while
	// excluding this request from cancellation and drain waits.
	BeginUserRetirement func(context.Context, int64) (Retirement, error)
	// This hook includes every game's cancellation and refund in the same TX.
	CancelUserGamesTx func(context.Context, *sql.Tx, int64, string, int64) (func(bool), error)
	OnBan             func(int64)
}

type Service struct{ config Config }

func New(config Config) (*Service, error) {
	if config.Database == nil || config.Rejections == nil || config.BeginUserRetirement == nil || config.CancelUserGamesTx == nil {
		return nil, ErrInvalid
	}
	return &Service{config: config}, nil
}

type Decision struct {
	Banned   bool
	Until    *int64
	Replayed bool
}

type ruleRef struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

type banReason struct {
	Source         string    `json:"source"`
	RequestID      string    `json:"request_id"`
	Rules          []ruleRef `json:"rules"`
	RuleCount      int       `json:"rule_count"`
	RulesTruncated bool      `json:"rules_truncated,omitempty"`
	Previous       string    `json:"previous,omitempty"`
}

func boundedReason(requestID string, refs []ruleRef, previous string) (string, error) {
	for included := len(refs); included >= 0; included-- {
		raw, err := json.Marshal(banReason{Source: "client_rule", RequestID: requestID, Rules: refs[:included], RuleCount: len(refs), RulesTruncated: included < len(refs), Previous: previous})
		if err != nil {
			return "", ErrInvariant
		}
		if len(raw) <= 4096 && utf8.RuneCount(raw) <= 1024 {
			return string(raw), nil
		}
	}
	if previous != "" {
		return previous, nil
	}
	return "", ErrInvariant
}

func sourceForRules(s observability.Source) riskaudit.Source {
	quality := make(map[string]riskaudit.FieldQuality, len(s.Quality))
	for name, q := range s.Quality {
		quality[name] = riskaudit.FieldQuality{Truncated: q.Truncated, Multiple: q.Multiple, Invalid: q.Invalid}
	}
	return riskaudit.Source{
		EffectiveIP: s.EffectiveIP, IPQuality: s.IPQuality, UserAgent: s.UserAgent,
		Origin: s.Origin, Referer: s.Referer, HTTPReferer: s.HTTPReferer,
		OpenRouterTitle: s.OpenRouterTitle, LegacyTitle: s.LegacyTitle,
		SDKLang: s.SDKLang, SDKVersion: s.SDKVersion, SDKRuntime: s.SDKRuntime,
		SDKRuntimeVersion: s.SDKRuntimeVersion, Quality: quality,
	}
}

type ruleQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func candidates(ctx context.Context, q ruleQuerier, source riskaudit.Source, now int64) ([]ruleRef, *int64, error) {
	rules, err := riskaudit.ActiveAutoBanRules(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	matches := riskaudit.MatchRules(source, rules)
	if len(matches) == 0 {
		return nil, nil, nil
	}
	byID := make(map[string]riskaudit.AutoBan, len(rules))
	for _, rule := range rules {
		if rule.AutoBan == nil {
			return nil, nil, ErrInvariant
		}
		byID[rule.ID] = *rule.AutoBan
	}
	refs := make([]ruleRef, 0, len(matches))
	var until *int64
	permanent := false
	for _, match := range matches {
		action, ok := byID[match.RuleID]
		if !ok || !action.Enabled || len(refs) >= riskaudit.MaxAutoBanRules {
			return nil, nil, ErrInvariant
		}
		refs = append(refs, ruleRef{ID: match.RuleID, Revision: match.Revision})
		if action.DurationSeconds == nil {
			permanent = true
			continue
		}
		end := now + *action.DurationSeconds
		if end < now || end > maxUnixSecond {
			return nil, nil, ErrInvalid
		}
		if until == nil || end > *until {
			until = &end
		}
	}
	if permanent {
		until = nil
	}
	return refs, until, nil
}

func readReceipt(ctx context.Context, tx *sql.Tx, requestID string, userID, now int64) (Decision, bool, error) {
	var owner int64
	var until sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT user_id,banned_until FROM client_rule_ban_receipts WHERE request_id=? AND expires_at>?`, requestID, now).Scan(&owner, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return Decision{}, false, nil
	}
	if err != nil || owner != userID {
		return Decision{}, false, ErrInvariant
	}
	decision := Decision{Banned: true, Replayed: true}
	if until.Valid {
		decision.Until = &until.Int64
	}
	return decision, true, nil
}

// CheckCharityCall requires a validated, classified real charity model call.
// The caller invokes it after routing snapshot and before claim acceptance.
// A Banned decision becomes a generic account-ban response to the caller.
func (s *Service) CheckCharityCall(parent context.Context, userID int64, model string, decisionNow int64) (Decision, error) {
	if s == nil || parent == nil || userID <= 0 || decisionNow < 0 || decisionNow > maxUnixSecond-receiptLifetime || requestattempt.Kind(parent) != "charity" {
		return Decision{}, ErrInvalid
	}
	requestID, err := requestattempt.Identity(parent, userID)
	if err != nil || !db.ValidateOpaqueID(requestID, "req_") {
		return Decision{}, ErrInvalid
	}
	source, ok := observability.SourceFromContext(parent)
	if !ok {
		return Decision{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 5e9)
	defer cancel()
	// This consistent read avoids disturbing unrelated calls. It is advisory;
	// the account and all rules are rechecked inside the effects transaction.
	readTx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Decision{}, fmt.Errorf("%w: begin precheck: %v", ErrUnavailable, err)
	}
	defer readTx.Rollback()
	var exists bool
	if err := readTx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0)`, userID).Scan(&exists); err != nil || !exists {
		return Decision{}, ErrUnavailable
	}
	pre, _, err := candidates(ctx, readTx, sourceForRules(source), decisionNow)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: read candidates: %v", ErrUnavailable, err)
	}
	if len(pre) == 0 {
		var owner int64
		var until sql.NullInt64
		err = readTx.QueryRowContext(ctx, `SELECT user_id,banned_until FROM client_rule_ban_receipts WHERE request_id=? AND expires_at>?`, requestID, decisionNow).Scan(&owner, &until)
		if errors.Is(err, sql.ErrNoRows) {
			return Decision{}, nil
		}
		if err != nil || owner != userID {
			return Decision{}, ErrInvariant
		}
		result := Decision{Banned: true, Replayed: true}
		if until.Valid {
			result.Until = &until.Int64
		}
		requestattempt.Handled(parent)
		return result, nil
	}
	if err := readTx.Rollback(); err != nil {
		return Decision{}, ErrUnavailable
	}
	retirement, err := s.config.BeginUserRetirement(ctx, userID)
	if err != nil {
		return Decision{}, err
	}
	if retirement == nil {
		return Decision{}, ErrInvariant
	}
	defer retirement.Abort()
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: begin effects: %v", ErrUnavailable, err)
	}
	defer tx.Rollback()
	if replay, exists, err := readReceipt(ctx, tx, requestID, userID, decisionNow); err != nil || exists {
		if err == nil && exists {
			requestattempt.Handled(parent)
		}
		return replay, err
	}
	var admin, banned, autoBanned int
	var priorUntil sql.NullInt64
	var priorReason, banKind string
	var revision []byte
	err = tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,banned_until,banned_reason,auto_banned,ban_kind,revision FROM users WHERE id=?`, userID).Scan(&admin, &banned, &priorUntil, &priorReason, &autoBanned, &banKind, &revision)
	if err != nil || admin != 0 {
		return Decision{}, ErrUnavailable
	}
	refs, until, err := candidates(ctx, tx, sourceForRules(source), decisionNow)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: recheck candidates: %v", ErrUnavailable, err)
	}
	if len(refs) == 0 {
		return Decision{}, nil
	}
	priorActive := banned != 0 && (!priorUntil.Valid || priorUntil.Int64 > decisionNow)
	priorDominates := priorActive && (!priorUntil.Valid || until != nil && priorUntil.Int64 >= *until)
	if priorActive {
		if !priorUntil.Valid {
			until = nil
		} else if until != nil && priorUntil.Int64 > *until {
			until = &priorUntil.Int64
		}
	}
	rulesJSON, err := json.Marshal(refs)
	if err != nil || len(rulesJSON) > 16384 {
		return Decision{}, ErrInvariant
	}
	previous := ""
	if priorActive {
		previous = priorReason
	}
	reason, err := boundedReason(requestID, refs, previous)
	if err != nil {
		return Decision{}, err
	}
	current, err := db.DecodeU128(revision)
	if err != nil {
		return Decision{}, ErrInvariant
	}
	next, err := db.U128FromBig(new(big.Int).Add(current.Big(), big.NewInt(1)))
	if err != nil {
		return Decision{}, ErrInvariant
	}
	finalize, err := s.config.CancelUserGamesTx(ctx, tx, userID, "account_unavailable", decisionNow)
	if err != nil || finalize == nil {
		return Decision{}, fmt.Errorf("%w: cancel games: %v", ErrUnavailable, err)
	}
	committed := false
	defer func() {
		if finalize != nil {
			finalize(committed)
		}
	}()
	var end any
	if until != nil {
		end = *until
	}
	if !priorDominates {
		// auto_banned is the legacy abuse-case flag; this source has its own
		// structured receipt and must not appear as a missing legacy case.
		autoBanned, banKind = 0, ""
	}
	result, err := tx.ExecContext(ctx, `UPDATE users SET is_banned=1,banned_until=?,banned_reason=?,auto_banned=?,ban_kind=?,revision=?,updated_at=MAX(updated_at,?) WHERE id=? AND is_admin=0 AND revision=?`, end, reason, autoBanned, banKind, db.EncodeU128(next), decisionNow, userID, revision)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: update user: %v", ErrUnavailable, err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return Decision{}, ErrInvariant
	}
	if err := useractivity.RescheduleTx(ctx, tx, userID); err != nil {
		return Decision{}, fmt.Errorf("%w: reschedule activity: %v", ErrUnavailable, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return Decision{}, fmt.Errorf("%w: revoke sessions: %v", ErrUnavailable, err)
	}
	result, err = tx.ExecContext(ctx, `UPDATE caller_keys SET generation=CASE WHEN key_hash IS NULL THEN generation ELSE generation+1 END,key_hash=NULL,display_head='',display_tail='',key_created_at=NULL,updated_at=? WHERE user_id=? AND (key_hash IS NULL OR generation<?)`, decisionNow, userID, int64(math.MaxInt64))
	if err != nil {
		return Decision{}, fmt.Errorf("%w: revoke caller key: %v", ErrUnavailable, err)
	}
	n, err = result.RowsAffected()
	if err != nil || n != 1 {
		return Decision{}, ErrInvariant
	}
	fact := requestattempt.Snapshot(ctx, requestID, model, "preflight", "forbidden", 403, httperr.CodeForbidden)
	if err := s.config.Rejections.RecordRejectionTx(ctx, tx, userID, fact, decisionNow); err != nil {
		return Decision{}, fmt.Errorf("%w: record rejection: %v", ErrUnavailable, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM client_rule_ban_receipts WHERE request_id=? AND expires_at<=?`, requestID, decisionNow); err != nil {
		return Decision{}, ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO client_rule_ban_receipts(request_id,user_id,rules_json,banned_until,created_at,expires_at) VALUES(?,?,?,?,?,?)`, requestID, userID, string(rulesJSON), end, decisionNow, decisionNow+receiptLifetime); err != nil {
		return Decision{}, fmt.Errorf("%w: store receipt: %v", ErrUnavailable, err)
	}
	if err := tx.Commit(); err != nil {
		return Decision{}, ErrUnavailable
	}
	committed = true
	finalize(true)
	finalize = nil
	retirement.Commit()
	requestattempt.Handled(parent)
	if s.config.OnBan != nil {
		s.config.OnBan(userID)
	}
	return Decision{Banned: true, Until: until}, nil
}

// CleanupReceiptsBatch removes expired idempotency receipts with a row budget.
func (s *Service) CleanupReceiptsBatch(ctx context.Context, now int64, limit int) (int, error) {
	if s == nil || ctx == nil || now < 0 || now > maxUnixSecond || limit < 1 || limit > 1000 {
		return 0, ErrInvalid
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return 0, ErrUnavailable
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM client_rule_ban_receipts WHERE request_id IN (SELECT request_id FROM client_rule_ban_receipts WHERE expires_at<=? ORDER BY expires_at,request_id LIMIT ?)`, now, limit)
	if err != nil {
		return 0, ErrUnavailable
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return 0, ErrUnavailable
	}
	return int(n), nil
}
