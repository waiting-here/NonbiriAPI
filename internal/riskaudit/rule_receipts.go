package riskaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type RuleRevision struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

// RuleBanReceipt is the bounded management-only explanation for one live
// request denial. The ordinary caller never receives rule identifiers.
type RuleBanReceipt struct {
	RequestID   string         `json:"request_id"`
	UserID      int64          `json:"user_id,string"`
	Source      string         `json:"source"`
	Rules       []RuleRevision `json:"rules"`
	BannedUntil *int64         `json:"banned_until"`
	CreatedAt   int64          `json:"created_at"`
	ExpiresAt   int64          `json:"expires_at"`
}

func (r *Repository) RuleBanReceipt(ctx context.Context, actor Actor, requestID string) (RuleBanReceipt, error) {
	if !actor.Admin {
		return RuleBanReceipt{}, ErrForbidden
	}
	if !db.ValidateOpaqueID(requestID, "req_") {
		return RuleBanReceipt{}, ErrInvalid
	}
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return RuleBanReceipt{}, err
	}
	defer tx.Rollback()
	var receipt RuleBanReceipt
	var raw string
	var until sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT user_id,rules_json,banned_until,created_at,expires_at FROM client_rule_ban_receipts WHERE request_id=? AND expires_at>?`, requestID, r.now().Unix()).Scan(&receipt.UserID, &raw, &until, &receipt.CreatedAt, &receipt.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RuleBanReceipt{}, ErrNotFound
	}
	if err != nil || len(raw) > 16384 || json.Unmarshal([]byte(raw), &receipt.Rules) != nil || len(receipt.Rules) < 1 || len(receipt.Rules) > MaxAutoBanRules {
		return RuleBanReceipt{}, ErrUnavailable
	}
	for _, ref := range receipt.Rules {
		if !db.ValidateOpaqueID(ref.ID, "rsk_") || ref.Revision < 1 {
			return RuleBanReceipt{}, ErrUnavailable
		}
	}
	receipt.RequestID = requestID
	receipt.Source = "client_rule"
	if until.Valid {
		receipt.BannedUntil = &until.Int64
	}
	return receipt, nil
}
