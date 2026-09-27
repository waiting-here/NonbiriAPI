package riskaudit

import (
	"context"
	"database/sql"
	"strconv"
)

// sourcesNumberedTx is called only after the final actor authorization and
// user existence check. COUNT, anchor and rows share that same SQL snapshot.
func sourcesNumberedTx(ctx context.Context, tx *sql.Tx, w Window, userID int64) (Page[SourceRequest], error) {
	page := Page[SourceRequest]{Items: []SourceRequest{}, From: w.From, To: w.To, Coverage: "source_page"}
	if userID <= 0 || w.Page < 1 || w.Page > 2147483647 || (w.Limit != 20 && w.Limit != 50 && w.Limit != 100) || w.After != 0 {
		return page, ErrInvalid
	}
	watermark := w.Watermark
	if !w.WatermarkSet {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(request_log_id),0) FROM request_source_facts`).Scan(&watermark); err != nil {
			return page, ErrUnavailable
		}
	}
	where := `s.user_id=? AND s.occurred_at>=? AND s.occurred_at<? AND s.request_log_id<=? AND s.kind IN ('self','charity','unclassified')`
	args := []any{userID, w.From, w.To, watermark}
	if w.Model != "" {
		where += ` AND l.model=?`
		args = append(args, w.Model)
	}
	if w.Kind != "" && w.Kind != "total" {
		where += ` AND s.kind=?`
		args = append(args, w.Kind)
	}
	from := ` FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE ` + where
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*)`+from, args...).Scan(&total); err != nil {
		return page, ErrUnavailable
	}
	pages := max(int64(1), (total+int64(w.Limit)-1)/int64(w.Limit))
	number := min(w.Page, pages)
	page.Page, page.PageSize = strconv.FormatInt(number, 10), w.Limit
	page.TotalItems, page.TotalPages = strconv.FormatInt(total, 10), strconv.FormatInt(pages, 10)
	page.Watermark, page.Changed = strconv.FormatInt(watermark, 10), w.ExpectedSet && w.ExpectedTotal != total
	if total == 0 {
		return page, nil
	}
	anchorArgs := append(append([]any{}, args...), (number-1)*int64(w.Limit))
	var at, anchorID int64
	if err := tx.QueryRowContext(ctx, `SELECT s.occurred_at,s.request_log_id`+from+` ORDER BY s.occurred_at DESC,s.request_log_id DESC LIMIT 1 OFFSET ?`, anchorArgs...).Scan(&at, &anchorID); err != nil {
		return page, ErrUnavailable
	}
	rowArgs := append(append([]any{}, args...), at, at, anchorID, w.Limit)
	rows, err := tx.QueryContext(ctx, `SELECT s.request_log_id,s.user_id,l.logical_request_id,s.kind,s.occurred_at,s.source_json,s.effective_ip,s.ip_quality,l.model,COALESCE(l.caller_result_class,'running'),EXISTS(SELECT 1 FROM dispatch_claims dc WHERE dc.logical_request_id=l.logical_request_id AND dc.dispatched_at IS NOT NULL),l.error_code,COALESCE(l.rejection_reason,''),l.duration_ms,l.completed_at`+from+` AND (s.occurred_at<? OR (s.occurred_at=? AND s.request_log_id<=?)) ORDER BY s.occurred_at DESC,s.request_log_id DESC LIMIT ?`, rowArgs...)
	if err != nil {
		return page, ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var entry SourceRequest
		var raw, ip, quality string
		var duration int64
		var completed sql.NullInt64
		if err = rows.Scan(&entry.LogID, &entry.UserID, &entry.RequestID, &entry.Kind, &entry.OccurredAt, &raw, &ip, &quality, &entry.Model, &entry.Outcome, &entry.Dispatched, &entry.ErrorCode, &entry.RejectionReason, &duration, &completed); err != nil {
			return page, ErrUnavailable
		}
		entry.Source, err = decodeSource(raw)
		if err != nil {
			entry.Source = Source{Quality: map[string]FieldQuality{"source": {Invalid: true}}}
			page.Coverage = "invalid_source"
		}
		entry.Source.EffectiveIP, entry.Source.IPQuality = ip, quality
		if completed.Valid {
			entry.DurationMillis = &duration
		}
		page.Items = append(page.Items, entry)
	}
	return page, rows.Err()
}
