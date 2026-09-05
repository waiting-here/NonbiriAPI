package ledger

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

var ErrInvalidHistory = errors.New("ledger: invalid history query")

type HistoryFilter struct {
	Page                        int64
	PageSize                    int
	From, To                    *int64
	Category, Direction, Anchor string
}

type HistoryEntry struct {
	OperationID string  `json:"operation_id"`
	Line        int     `json:"line"`
	Kind        Kind    `json:"kind"`
	Delta       string  `json:"delta"`
	CreatedAt   int64   `json:"created_at"`
	RequestID   *string `json:"request_id"`
}

type HistoryPage struct {
	Data           []HistoryEntry `json:"data"`
	Page           string         `json:"page"`
	PageSize       int            `json:"page_size"`
	Total          string         `json:"total"`
	TotalPages     string         `json:"total_pages"`
	Anchor         *string        `json:"anchor"`
	CurrentBalance string         `json:"current_balance"`
	ServerNow      int64          `json:"server_now"`
}

var historyCategories = map[string][]Kind{
	"checkin":  {KindCheckinAward},
	"welfare":  {KindWelfareClaim},
	"thursday": {KindThursdayContribution, KindThursdayPayout, KindThursdayFinalize},
	"fishing":  {KindFishingReserve, KindFishingSettle, KindFishingRelease},
	"linklink": {KindLinkLinkEntry},
	"rps":      {KindRPSQueueReserve, KindRPSQueueRelease, KindRPSSessionStart, KindRPSRoundCut, KindRPSTerminal},
	"api":      {KindForwardReserve, KindForwardSettle, KindForwardRelease},
	"charity":  {KindCharityReserve, KindCharitySettle, KindCharityRelease},
	"donation": {KindDonorReward},
	"admin":    {KindAdminUserAdjustment},
	"penalty":  {KindAntiAbusePenalty},
}

func ValidateHistoryFilter(filter HistoryFilter) error {
	if filter.Page < 1 || (filter.PageSize != 20 && filter.PageSize != 50 && filter.PageSize != 100) ||
		(filter.From != nil && !validUnix(*filter.From)) || (filter.To != nil && !validUnix(*filter.To)) ||
		(filter.From != nil && filter.To != nil && *filter.From >= *filter.To) ||
		(filter.Direction != "" && filter.Direction != "income" && filter.Direction != "expense") ||
		(filter.Anchor != "" && !db.ValidateOpaqueID(filter.Anchor, "op_")) {
		return ErrInvalidHistory
	}
	if _, ok := historyCategories[filter.Category]; filter.Category != "" && !ok {
		return ErrInvalidHistory
	}
	return nil
}

// UserHistory reads a bounded owner projection in the caller's read transaction.
// The anchor belongs to this wallet; no global sequence or other account facts
// are returned. CurrentBalance is the current snapshot, not a historical balance.
func UserHistory(ctx context.Context, tx *sql.Tx, userID, now int64, filter HistoryFilter) (HistoryPage, error) {
	if ctx == nil || tx == nil || userID <= 0 || !validUnix(now) || ValidateHistoryFilter(filter) != nil {
		return HistoryPage{}, ErrInvalidHistory
	}
	wallet, err := UserAccount(ctx, tx, userID)
	if err != nil {
		return HistoryPage{}, err
	}
	page := HistoryPage{Data: []HistoryEntry{}, Page: "1", PageSize: filter.PageSize, Total: "0", TotalPages: "1",
		CurrentBalance: formatDisplayCredits(wallet.Balance.Big()), ServerNow: now}
	anchorQuery := `SELECT o.id,o.ledger_seq FROM credit_entries e JOIN credit_operations o ON o.id=e.operation_id
WHERE e.account_id=? AND e.account_kind_snapshot='user' AND e.delta_sign<>0`
	anchorArgs := []any{wallet.ID}
	if filter.Anchor != "" {
		anchorQuery += ` AND o.id=?`
		anchorArgs = append(anchorArgs, filter.Anchor)
	}
	anchorQuery += ` ORDER BY o.ledger_seq DESC LIMIT 1`
	var anchor string
	var sequence int64
	err = tx.QueryRowContext(ctx, anchorQuery, anchorArgs...).Scan(&anchor, &sequence)
	if errors.Is(err, sql.ErrNoRows) {
		if filter.Anchor != "" {
			return HistoryPage{}, ErrInvalidHistory
		}
		return page, nil
	}
	if err != nil {
		return HistoryPage{}, classifySQLError("history anchor", err)
	}
	page.Anchor = &anchor
	where := ` FROM credit_entries e JOIN credit_operations o ON o.id=e.operation_id
WHERE e.account_id=? AND e.account_kind_snapshot='user' AND e.delta_sign<>0 AND o.ledger_seq<=?`
	args := []any{wallet.ID, sequence}
	if filter.From != nil {
		where += ` AND o.created_at>=?`
		args = append(args, *filter.From)
	}
	if filter.To != nil {
		where += ` AND o.created_at<?`
		args = append(args, *filter.To)
	}
	if filter.Direction != "" {
		sign := 1
		if filter.Direction == "expense" {
			sign = -1
		}
		where += ` AND e.delta_sign=?`
		args = append(args, sign)
	}
	if kinds := historyCategories[filter.Category]; len(kinds) != 0 {
		where += ` AND o.kind IN (` + strings.TrimRight(strings.Repeat("?,", len(kinds)), ",") + `)`
		for _, kind := range kinds {
			args = append(args, string(kind))
		}
	}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+where, args...).Scan(&count); err != nil {
		return HistoryPage{}, classifySQLError("history count", err)
	}
	pages := int64(1)
	if count > 0 {
		pages = (count-1)/int64(filter.PageSize) + 1
	}
	current := min(filter.Page, pages)
	page.Page, page.Total, page.TotalPages = strconv.FormatInt(current, 10), strconv.FormatInt(count, 10), strconv.FormatInt(pages, 10)
	// A donor reward never enters the request lookup, even for a self-donation.
	query := `SELECT o.id,e.line_no,o.kind,o.source_type,o.source_id,o.source_seq,o.created_at,e.delta_sign,e.delta_mag,
CASE WHEN o.source_type='logical_request' AND o.kind IN ('forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release')
THEN (SELECT l.logical_request_id FROM request_logs l WHERE l.logical_request_id=o.source_id AND l.user_id=?
AND (l.completed_at IS NULL OR l.completed_at>?) AND (l.route_kind<>'charity_chat_completions' OR l.completed_at IS NOT NULL) LIMIT 1)
WHEN o.source_type='operation' AND o.kind='anti_abuse_penalty'
THEN (SELECT l.logical_request_id FROM request_logs l WHERE l.logical_request_id='req_'||substr(o.id,4) AND l.user_id=?
AND l.route_kind='charity_chat_completions' AND l.caller_error_code='content_too_short' AND l.completed_at>? LIMIT 1)
ELSE NULL END` + where + ` ORDER BY o.ledger_seq DESC,e.line_no DESC LIMIT ? OFFSET ?`
	queryArgs := append([]any{userID, now - 30*24*60*60, userID, now - 30*24*60*60}, args...)
	queryArgs = append(queryArgs, filter.PageSize, (current-1)*int64(filter.PageSize))
	rows, err := tx.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return HistoryPage{}, classifySQLError("history entries", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry HistoryEntry
		var sourceType, sourceID string
		var sourceSeq, magnitude []byte
		var sign int
		var requestID sql.NullString
		if err := rows.Scan(&entry.OperationID, &entry.Line, &entry.Kind, &sourceType, &sourceID, &sourceSeq,
			&entry.CreatedAt, &sign, &magnitude, &requestID); err != nil {
			return HistoryPage{}, classifySQLError("history row", err)
		}
		valid := validExportOperation(UserExportEntry{OperationID: entry.OperationID, Kind: entry.Kind,
			SourceType: sourceType, SourceID: sourceID, CreatedAt: entry.CreatedAt}, sourceSeq)
		amount, amountErr := amountFromParts(sign, magnitude)
		clear(sourceSeq)
		clear(magnitude)
		if !valid || amountErr != nil || entry.Line < 0 || entry.Line > 255 {
			return HistoryPage{}, ErrInvariant
		}
		entry.Delta = formatDisplayCredits(amount.Big())
		if requestID.Valid {
			if !db.ValidateOpaqueID(requestID.String, "req_") {
				return HistoryPage{}, ErrInvariant
			}
			entry.RequestID = &requestID.String
		}
		page.Data = append(page.Data, entry)
	}
	if err := rows.Err(); err != nil {
		return HistoryPage{}, classifySQLError("history rows", err)
	}
	return page, nil
}
