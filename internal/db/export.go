// Export repository: ownership-scoped, bounded projections for the self-service
// account export rail. Every projection returns metadata only — no endpoint-key
// ciphertext, no OAuth token, no caller-key material, and no request/response
// content is ever selected. Collections are finite: a projection that crosses
// its limit fails closed (ErrExportLimit) instead of returning a partial set,
// so an export package can never grow without bound or silently drop rows.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

// ErrExportLimit reports that an export collection exceeded the finite bound.
// The export rail maps it to a stable payload_too_large response; nothing is
// ever returned partially.
var ErrExportLimit = errors.New("export collection exceeds its limit")

// ExportCollectionLimit caps every collection inside one export package. It
// is generous relative to every product cap (endpoints use the independent
// explicit endpoint hard range; models/bindings have no count cap today),
// while still keeping the assembled package and the response bounded.
const ExportCollectionLimit = 10000

// LogSummaryWindow is the retention-visible window for the export's log
// summary: aggregates over the most recent window of request metadata.
const LogSummaryWindow = 30 * 24 * time.Hour

// ExportLogSummary is the aggregated metadata summary backing the export's
// log section. It never contains individual log rows or any upstream text.
type ExportLogSummary struct {
	TotalLogs        int64
	LogsLast30Days   int64
	ErrorLogs        int64
	UsageUnknownLogs int64
	AvgDurationMs    int64
}

// ListExportEndpoints returns up to limit endpoints owned by userID in id
// order. It projects the same public metadata as the endpoint list endpoint;
// no key material is involved.
func (s *Store) ListExportEndpoints(ctx context.Context, userID int64, limit int) ([]Endpoint, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, user_id, connector_type, base_url, note, enabled, model_fetch_failed, model_fetch_failed_at, created_at, updated_at
FROM endpoints WHERE user_id=? ORDER BY id LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export endpoints: %w", err)
	}
	defer rows.Close()
	return scanExportEndpoints(rows, limit)
}

// ListExportEndpointKeys returns up to limit endpoint keys owned by userID in
// id order, joined through endpoints so ownership is enforced in SQL. Only
// metadata and display fragments are selected; encrypted_secret never enters
// the export path.
func (s *Store) ListExportEndpointKeys(ctx context.Context, userID int64, limit int) ([]EndpointKey, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT ek.id, ek.endpoint_id, ek.display_head, ek.display_tail, ek.note, ek.enabled, ek.created_at, ek.updated_at
FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE e.user_id=?
ORDER BY ek.id
LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export endpoint keys: %w", err)
	}
	defer rows.Close()
	return scanExportEndpointKeys(rows, limit)
}

// ListExportModels returns up to limit platform models owned by userID in id
// order, with their live binding counts. It is the bounded variant of the
// model list projection used by the export rail.
func (s *Store) ListExportModels(ctx context.Context, userID int64, limit int) ([]Model, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, modelListSQL+` LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export models: %w", err)
	}
	defer rows.Close()
	return scanExportModels(rows, limit)
}

// ListExportBindings returns up to limit model bindings owned by userID in
// (ord, id) order, joined through models so ownership is enforced in SQL.
func (s *Store) ListExportBindings(ctx context.Context, userID int64, limit int) ([]ModelBinding, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT b.id, b.model_id, b.endpoint_key_id, b.upstream_model_id, b.ord, b.created_at
FROM model_bindings b
JOIN models m ON b.model_id = m.id
WHERE m.user_id=?
ORDER BY b.ord, b.id
LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export bindings: %w", err)
	}
	defer rows.Close()
	return scanExportBindings(rows, limit)
}

// ActivityDailyExportRow is one site-local day of the user's own activity
// summary for the export package. Metadata only: counters and day keys, no
// model names, no request content.
type ActivityDailyExportRow struct {
	Day                   int64 `json:"day"`
	ProductActive         bool  `json:"product_active"`
	APIRequests           int64 `json:"api_requests"`
	UncachedInputTokens   int64 `json:"uncached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	CacheReadInputTokens  int64 `json:"cache_read_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	Checkins              int64 `json:"checkins"`
	ConsoleWrites         int64 `json:"console_writes"`
	GameActive            bool  `json:"game_active"`
	GameRounds            int64 `json:"game_rounds"`
}

// ListExportActivityDaily returns up to limit daily activity summary rows
// owned by userID, newest day first. Retention bounds the table to 400 days,
// so the collection is naturally finite; crossing the explicit limit still
// fails closed like every other export projection.
func (s *Store) ListExportActivityDaily(ctx context.Context, userID int64, limit int) ([]ActivityDailyExportRow, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT day, product_active, api_requests,
       uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens,
       checkins, console_writes, game_active, game_rounds
FROM user_activity_daily WHERE user_id=? ORDER BY day DESC LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export activity daily: %w", err)
	}
	defer rows.Close()
	out := make([]ActivityDailyExportRow, 0, min(limit, 64))
	for rows.Next() {
		var row ActivityDailyExportRow
		var productActive, gameActive int
		if err := rows.Scan(&row.Day, &productActive, &row.APIRequests,
			&row.UncachedInputTokens, &row.CacheWriteInputTokens, &row.CacheReadInputTokens, &row.OutputTokens,
			&row.Checkins, &row.ConsoleWrites, &gameActive, &row.GameRounds); err != nil {
			return nil, fmt.Errorf("export activity daily: %w", err)
		}
		row.ProductActive = productActive == 1
		row.GameActive = gameActive == 1
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export activity daily: %w", err)
	}
	if len(out) > limit {
		return nil, ErrExportLimit
	}
	return out, nil
}

// ExportLogSummaryForUser aggregates the user's request-log metadata into one
// bounded summary row. A user with no logs yields an all-zero summary, never
// an error; a missing user yields ErrNotFound.
func (s *Store) ExportLogSummaryForUser(ctx context.Context, userID int64) (ExportLogSummary, error) {
	if userID <= 0 {
		return ExportLogSummary{}, ErrNotFound
	}
	var summary ExportLogSummary
	var avg float64
	cutoff := time.Now().Add(-LogSummaryWindow).Unix()
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(CASE WHEN started_at >= ? THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(usage_unknown), 0),
       COALESCE(AVG(duration_ms), 0)
FROM request_logs WHERE user_id=?`, cutoff, userID).
		Scan(&summary.TotalLogs, &summary.LogsLast30Days, &summary.ErrorLogs, &summary.UsageUnknownLogs, &avg)
	if errors.Is(err, sql.ErrNoRows) {
		return ExportLogSummary{}, ErrNotFound
	}
	if err != nil {
		return ExportLogSummary{}, fmt.Errorf("export log summary: %w", err)
	}
	summary.AvgDurationMs = int64(avg)
	return summary, nil
}

func scanExportEndpoints(rows *sql.Rows, limit int) ([]Endpoint, error) {
	out := make([]Endpoint, 0, min(limit, 32))
	for rows.Next() {
		if len(out) == limit {
			return nil, ErrExportLimit
		}
		var ep Endpoint
		var enabledInt, fetchFailedInt int
		if err := rows.Scan(&ep.ID, &ep.UserID, &ep.ConnectorType, &ep.BaseURL, &ep.Note, &enabledInt, &fetchFailedInt, &ep.ModelFetchFailedAt, &ep.CreatedAt, &ep.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan export endpoint: %w", err)
		}
		ep.Enabled = enabledInt != 0
		ep.ModelFetchFailed = fetchFailedInt != 0
		out = append(out, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export endpoints: %w", err)
	}
	return out, nil
}

func scanExportEndpointKeys(rows *sql.Rows, limit int) ([]EndpointKey, error) {
	out := make([]EndpointKey, 0, min(limit, 32))
	for rows.Next() {
		if len(out) == limit {
			return nil, ErrExportLimit
		}
		var k EndpointKey
		var enabledInt int
		if err := rows.Scan(&k.ID, &k.EndpointID, &k.DisplayHead, &k.DisplayTail, &k.Note, &enabledInt, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan export endpoint key: %w", err)
		}
		k.Enabled = enabledInt != 0
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export endpoint keys: %w", err)
	}
	return out, nil
}

func scanExportModels(rows *sql.Rows, limit int) ([]Model, error) {
	out := make([]Model, 0, min(limit, 32))
	for rows.Next() {
		if len(out) == limit {
			return nil, ErrExportLimit
		}
		var m Model
		var count int
		var silentRetry int
		if err := rows.Scan(&m.ID, &m.UserID, &m.Provider, &m.Model, &m.FullName, &m.RouteStrategy,
			&silentRetry, &m.CreatedAt, &m.UpdatedAt, &count); err != nil {
			return nil, fmt.Errorf("scan export model: %w", err)
		}
		m.SilentRetry = silentRetry != 0
		m.BindingCount = count
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export models: %w", err)
	}
	return out, nil
}

func scanExportBindings(rows *sql.Rows, limit int) ([]ModelBinding, error) {
	out := make([]ModelBinding, 0, min(limit, 32))
	for rows.Next() {
		if len(out) == limit {
			return nil, ErrExportLimit
		}
		var b ModelBinding
		if err := rows.Scan(&b.ID, &b.ModelID, &b.EndpointKeyID, &b.UpstreamModelID, &b.Ord, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan export binding: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export bindings: %w", err)
	}
	return out, nil
}

// DonationKeyExportRow is one donated key of the self-service export package:
// persisted display fragments and charity-use limits only — never secret or
// ciphertext material.
type DonationKeyExportRow struct {
	ID                   int64  `json:"id"`
	EndpointKeyID        *int64 `json:"endpoint_key_id"`
	DisplayHead          string `json:"display_head"`
	DisplayTail          string `json:"display_tail"`
	MaxConcurrency       int64  `json:"max_concurrency"`
	RPMLimit             int64  `json:"rpm_limit"`
	CreditsUsageCapMilli string `json:"credits_usage_cap_milli"`
	CreditsUsedMilli     string `json:"credits_used_milli"`
	Enabled              bool   `json:"enabled"`
	CreatedAt            int64  `json:"created_at"`
}

// DonationReviewExportRow is one audit entry of the donor's own submission.
type DonationReviewExportRow struct {
	ID             int64  `json:"id"`
	ReviewerUserID *int64 `json:"reviewer_user_id"`
	ReviewerRole   string `json:"reviewer_role"`
	Action         string `json:"action"`
	Note           string `json:"note"`
	CreatedAt      int64  `json:"created_at"`
}

// DonationExportRow is one donation of the self-service export package: safe
// metadata (status, base-URL snapshot, description, expiry) plus keys and
// review history. No note/secret/ciphertext field exists anywhere on these
// rows by construction.
type DonationExportRow struct {
	ID              int64                     `json:"id"`
	EndpointID      *int64                    `json:"endpoint_id"`
	EndpointBaseURL string                    `json:"endpoint_base_url"`
	Status          string                    `json:"status"`
	Enabled         bool                      `json:"enabled"`
	Description     string                    `json:"description"`
	ReviewNote      string                    `json:"review_note"`
	ExpiresAt       *int64                    `json:"expires_at"`
	CreatedAt       int64                     `json:"created_at"`
	Keys            []DonationKeyExportRow    `json:"keys"`
	Reviews         []DonationReviewExportRow `json:"reviews"`
}

// ListExportDonations returns up to limit donations owned by userID in id
// order for the self-service export (bounded, fail closed).
func (s *Store) ListExportDonations(ctx context.Context, userID int64, limit int) ([]DonationExportRow, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, endpoint_id, endpoint_base_url, status, enabled, description,
       review_note, expires_at, created_at
FROM donations WHERE user_id=? ORDER BY id LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export donations: %w", err)
	}
	defer rows.Close()
	out := make([]DonationExportRow, 0, min(limit, 16))
	for rows.Next() {
		var (
			r                     DonationExportRow
			endpointID, expiresAt sql.NullInt64
			enabledInt            int
		)
		if err := rows.Scan(&r.ID, &endpointID, &r.EndpointBaseURL, &r.Status, &enabledInt,
			&r.Description, &r.ReviewNote, &expiresAt, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("export donations: scan: %w", err)
		}
		r.Enabled = enabledInt == 1
		r.EndpointID = nullInt64Ptr(endpointID)
		r.ExpiresAt = nullInt64Ptr(expiresAt)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export donations: iterate: %w", err)
	}
	if len(out) > limit {
		return nil, ErrExportLimit
	}
	for i := range out {
		keys, err := listDonationKeysTx(ctx, s.db, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Keys = make([]DonationKeyExportRow, 0, len(keys))
		for _, k := range keys {
			out[i].Keys = append(out[i].Keys, DonationKeyExportRow{
				ID: k.ID, EndpointKeyID: k.EndpointKeyID,
				DisplayHead: k.DisplayHead, DisplayTail: k.DisplayTail,
				MaxConcurrency: k.MaxConcurrency, RPMLimit: k.RPMLimit,
				CreditsUsageCapMilli: credits.FormatAmount(k.CreditsUsageCap),
				CreditsUsedMilli:     credits.FormatAmount(k.CreditsUsed),
				Enabled:              k.Enabled, CreatedAt: k.CreatedAt,
			})
		}
		reviews, err := s.db.QueryContext(ctx, `
SELECT id, reviewer_user_id, reviewer_role, action, note, created_at
FROM donation_reviews WHERE donation_id=? ORDER BY id`, out[i].ID)
		if err != nil {
			return nil, fmt.Errorf("export donation reviews: %w", err)
		}
		for reviews.Next() {
			var rv DonationReviewExportRow
			var reviewer sql.NullInt64
			if err := reviews.Scan(&rv.ID, &reviewer, &rv.ReviewerRole, &rv.Action, &rv.Note, &rv.CreatedAt); err != nil {
				reviews.Close()
				return nil, fmt.Errorf("export donation reviews: scan: %w", err)
			}
			rv.ReviewerUserID = nullInt64Ptr(reviewer)
			out[i].Reviews = append(out[i].Reviews, rv)
		}
		if err := reviews.Err(); err != nil {
			reviews.Close()
			return nil, fmt.Errorf("export donation reviews: iterate: %w", err)
		}
		reviews.Close()
	}
	return out, nil
}
