package observability

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type AccessFilter struct {
	From, To, UserID int64
	Limit            int
	Cursor           string
	KeyGeneration    *int64
	PathKind         string
	StatusClass      int
}
type AccessRow struct {
	ID                  string  `json:"id"`
	UserID              string  `json:"user_id"`
	CallerKeyGeneration *string `json:"caller_key_generation"`
	PathKind            string  `json:"path_kind"`
	Method              string  `json:"method"`
	HTTPStatus          int     `json:"http_status"`
	ResponseKind        string  `json:"response_kind"`
	Source              Source  `json:"source"`
	OccurredAt          int64   `json:"occurred_at"`
}
type AccessPage struct {
	Data       []AccessRow `json:"data"`
	NextCursor *string     `json:"next_cursor"`
}
type Coverage struct {
	CaptureStartedAt int64  `json:"capture_started_at"`
	Dropped          int64  `json:"dropped"`
	LastGapAt        *int64 `json:"last_gap_at"`
}
type AccessPathSummary struct {
	PathKind      string `json:"path_kind"`
	ResponseKind  string `json:"response_kind"`
	Authenticated int64  `json:"authenticated"`
	Anonymous     int64  `json:"anonymous"`
}
type AccessSummary struct {
	AuthenticatedEvents  int64               `json:"authenticated_events"`
	AnonymousEvents      int64               `json:"anonymous_events"`
	ModelListEvents      int64               `json:"model_list_events"`
	GenerationRequests   int64               `json:"generation_requests"`
	ModelGenerationRatio *float64            `json:"model_generation_ratio"`
	Coverage             Coverage            `json:"coverage"`
	Paths                []AccessPathSummary `json:"paths"`
}
type accessCursor struct {
	At int64  `json:"at"`
	ID string `json:"id"`
}

func validateAccessFilter(filter AccessFilter) error {
	if filter.From < 0 || filter.To <= filter.From || filter.To-filter.From > RetentionSeconds || filter.UserID < 0 || filter.Limit < 1 || filter.Limit > 100 || len(filter.Cursor) > 256 {
		return ErrInvalid
	}
	if filter.KeyGeneration != nil && (*filter.KeyGeneration < 0 || filter.UserID <= 0) {
		return ErrInvalid
	}
	if filter.PathKind != "" && pathForKind(filter.PathKind) == "" {
		return ErrInvalid
	}
	if filter.StatusClass < 0 || filter.StatusClass > 5 {
		return ErrInvalid
	}
	return nil
}

func accessWhere(filter AccessFilter) (string, []any) {
	where := `occurred_at>=? AND occurred_at<?`
	args := []any{filter.From, filter.To}
	if filter.UserID > 0 {
		where += ` AND user_id=?`
		args = append(args, filter.UserID)
	}
	if filter.KeyGeneration != nil {
		where += ` AND caller_key_generation=?`
		args = append(args, *filter.KeyGeneration)
	}
	if filter.PathKind != "" {
		where += ` AND path_kind=?`
		args = append(args, filter.PathKind)
	}
	if filter.StatusClass > 0 {
		where += ` AND (http_status/100)=?`
		args = append(args, filter.StatusClass)
	}
	return where, args
}

// Access reads require authorization in the caller-owned transaction. Their
// cursor is only a position within the already authorized, bounded selection.
func ListAccessTx(ctx context.Context, tx *sql.Tx, filter AccessFilter) (AccessPage, error) {
	page := AccessPage{Data: make([]AccessRow, 0)}
	if tx == nil || validateAccessFilter(filter) != nil {
		return page, ErrInvalid
	}
	where, args := accessWhere(filter)
	if filter.Cursor != "" {
		encoded, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
		var cursor accessCursor
		if err != nil || json.Unmarshal(encoded, &cursor) != nil || cursor.At < filter.From || cursor.At >= filter.To || !db.ValidateOpaqueID(cursor.ID, "aev_") {
			return page, ErrInvalid
		}
		where += ` AND (occurred_at<? OR (occurred_at=? AND id<?))`
		args = append(args, cursor.At, cursor.At, cursor.ID)
	}
	args = append(args, filter.Limit+1)
	rows, err := tx.QueryContext(ctx, `SELECT id,user_id,caller_key_generation,path_kind,method,http_status,response_kind,source_json,occurred_at FROM audit_access_events WHERE `+where+` ORDER BY occurred_at DESC,id DESC LIMIT ?`, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item AccessRow
		var userID int64
		var generation sql.NullInt64
		var encoded string
		if err = rows.Scan(&item.ID, &userID, &generation, &item.PathKind, &item.Method, &item.HTTPStatus, &item.ResponseKind, &encoded, &item.OccurredAt); err != nil {
			return page, err
		}
		if len(page.Data) == filter.Limit {
			last := page.Data[len(page.Data)-1]
			encoded, _ := json.Marshal(accessCursor{At: last.OccurredAt, ID: last.ID})
			cursor := base64.RawURLEncoding.EncodeToString(encoded)
			page.NextCursor = &cursor
			break
		}
		item.UserID = strconv.FormatInt(userID, 10)
		if generation.Valid {
			g := strconv.FormatInt(generation.Int64, 10)
			item.CallerKeyGeneration = &g
		}
		item.Source, err = ParseSource([]byte(encoded))
		if err != nil {
			return page, err
		}
		page.Data = append(page.Data, item)
	}
	return page, rows.Err()
}

func SummarizeAccessTx(ctx context.Context, tx *sql.Tx, filter AccessFilter) (AccessSummary, error) {
	result := AccessSummary{Paths: make([]AccessPathSummary, 0)}
	if tx == nil || validateAccessFilter(filter) != nil {
		return result, ErrInvalid
	}
	var gap sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT capture_started_at,access_dropped,last_access_gap_at FROM observability_state WHERE id=1`).Scan(&result.Coverage.CaptureStartedAt, &result.Coverage.Dropped, &gap); err != nil {
		return result, err
	}
	if gap.Valid {
		result.Coverage.LastGapAt = &gap.Int64
	}
	where, args := accessWhere(filter)
	rows, err := tx.QueryContext(ctx, `SELECT path_kind,response_kind,count(*) FROM audit_access_events WHERE `+where+` GROUP BY path_kind,response_kind ORDER BY path_kind,response_kind`, args...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item AccessPathSummary
		if err = rows.Scan(&item.PathKind, &item.ResponseKind, &item.Authenticated); err != nil {
			_ = rows.Close()
			return result, err
		}
		result.AuthenticatedEvents += item.Authenticated
		if item.PathKind == "models" {
			result.ModelListEvents += item.Authenticated
		}
		result.Paths = append(result.Paths, item)
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	if filter.UserID == 0 {
		anonWhere := `minute_at>=? AND minute_at<?`
		anonArgs := []any{filter.From - filter.From%60, filter.To}
		if filter.PathKind != "" {
			anonWhere += ` AND path_kind=?`
			anonArgs = append(anonArgs, filter.PathKind)
		}
		if filter.StatusClass > 0 {
			anonWhere += ` AND status_class=?`
			anonArgs = append(anonArgs, filter.StatusClass)
		}
		rows, err = tx.QueryContext(ctx, `SELECT path_kind,SUM(count) FROM anonymous_access_minutes WHERE `+anonWhere+` GROUP BY path_kind ORDER BY path_kind`, anonArgs...)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			item := AccessPathSummary{ResponseKind: "other"}
			if err = rows.Scan(&item.PathKind, &item.Anonymous); err != nil {
				_ = rows.Close()
				return result, err
			}
			result.AnonymousEvents += item.Anonymous
			result.Paths = append(result.Paths, item)
		}
		if err = rows.Close(); err != nil {
			return result, err
		}
		if err = rows.Err(); err != nil {
			return result, err
		}
	}
	// Ordinary generation attempts are counted once by their logical source
	// row, including refusals. Discovery and auxiliary events are excluded.
	where = `occurred_at>=? AND occurred_at<?`
	args = []any{filter.From, filter.To}
	if filter.UserID > 0 {
		where += ` AND user_id=?`
		args = append(args, filter.UserID)
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM request_source_facts WHERE kind IN ('self','charity') AND `+where, args...).Scan(&result.GenerationRequests); err != nil {
		return result, err
	}
	if result.GenerationRequests > 0 {
		ratio := float64(result.ModelListEvents) / float64(result.GenerationRequests)
		result.ModelGenerationRatio = &ratio
	}
	return result, nil
}
