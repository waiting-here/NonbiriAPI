// Package lifecycle implements account self-service for the user station:
// the bounded, whitelisted self-service export of a user's own data.
//
// The export package is the privacy/portability boundary of the platform
// (DEC-005/DEC-006): it contains user metadata, endpoint metadata, platform
// models and bindings, caller-key display metadata, usage totals, and a
// request-log summary — and nothing else. Upstream secrets and their
// ciphertext, OAuth tokens, caller-key plaintext, and request/response
// content are never projected. Every collection is ownership-scoped in SQL
// and bounded; an oversized package fails closed instead of being truncated.
//
// The export requires an active user session plus a consumed elevated
// capability (the two-step elevation from the auth rail). The response is
// single-use: the capability is atomically consumed when the export proceeds,
// and the JSON body is no-store with an attachment disposition.
package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

// ErrExportTooLarge reports that the assembled package exceeded the finite
// byte bound; nothing is written and no partial package is returned.
var ErrExportTooLarge = errors.New("export package exceeds its size bound")

// MaxExportBytes bounds the marshaled export package. The projection row
// limits (db.ExportCollectionLimit) are the primary bound; this is the final
// byte-level backstop before the response is written.
const MaxExportBytes = 8 << 20

// Default export rate limits: a per-user sliding window. The elevated
// capability is single-use anyway (each export needs a fresh Discord
// re-authorization), so the limiter is a second, cheap bound against bulk
// pulls and drive-by abuse.
const (
	DefaultExportWindow       = 10 * time.Minute
	DefaultExportLimitPerUser = 3
)

// Elevation is the consumed-capability boundary supplied by the auth rail.
// A nil-safe implementation must never treat a missing capability as success.
type Elevation interface {
	// ConsumeElevated atomically validates and consumes the X-Elevated-Token
	// against the session presented in the request. Any failure is reported
	// with a single sentinel error (auth.ErrElevationRequired).
	ConsumeElevated(w http.ResponseWriter, r *http.Request, user *db.User) error
	// ClearElevatedCookie removes the browser-side elevated capability cookie
	// after a consume attempt (success or failure).
	ClearElevatedCookie(w http.ResponseWriter, r *http.Request)
}

// UserResolver extracts the server-authoritative user from a request. The
// production resolver reads the user-session principal installed by the auth
// middleware; a missing principal fails the request as unauthorized.
type UserResolver func(*http.Request) (*db.User, error)

// HandlerDeps are the collaborators the export handler needs. Limiter is
// optional: a nil value selects the default per-user export window. Exporter
// is optional and defaults to the real ExportService over Store; tests inject a
// stub to exercise error mapping.
type HandlerDeps struct {
	Store     *db.Store
	Exporter  Exporter
	Resolve   UserResolver
	Elevation Elevation
	Limiter   *ratelimit.ProbeLimiter
}

// Exporter assembles the bounded export package for one user. The production
// implementation is ExportService; tests inject a fake to exercise error mapping
// without seeding tens of thousands of rows.
type Exporter interface {
	BuildExport(ctx context.Context, user *db.User) ([]byte, error)
}

// Handler is the mountable user-station account self-service route tree. It
// does not mount itself; NewHandler returns an http.Handler for the
// integration rail to register under /api/account.
type Handler struct {
	exporter  Exporter
	resolve   UserResolver
	elevation Elevation
	limiter   *ratelimit.ProbeLimiter
	mux       *http.ServeMux
}

// NewHandler builds the route tree. A nil resolver or elevation boundary
// denies every sensitive request (fail closed).
func NewHandler(deps HandlerDeps) http.Handler {
	exporter := deps.Exporter
	if exporter == nil {
		exporter = NewExportService(deps.Store)
	}
	h := &Handler{
		exporter:  exporter,
		resolve:   deps.Resolve,
		elevation: deps.Elevation,
		limiter:   deps.Limiter,
		mux:       http.NewServeMux(),
	}
	if h.limiter == nil {
		if limiter, err := ratelimit.NewProbeLimiter(ratelimit.ProbeLimiterConfig{
			Window: DefaultExportWindow, DefaultLimit: DefaultExportLimitPerUser,
		}); err == nil {
			h.limiter = limiter
		}
	}
	h.mux.HandleFunc("POST /api/account/export", h.exportAccount)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// exportAccount handles POST /api/account/export: session + elevated
// capability + rate limit, then a bounded whitelist JSON package. The
// capability is consumed only after the rate limit admits the request, so a
// rate-limited user keeps their still-valid single-use token.
func (h *Handler) exportAccount(w http.ResponseWriter, r *http.Request) {
	if r == nil || httpmw.StationOf(r) != host.StationUser {
		writeStable(w, httperr.New(httperr.CodeForbidden, "station authorization required"))
		return
	}
	if h.exporter == nil || h.elevation == nil || h.limiter == nil {
		writeStable(w, httperr.New(httperr.CodeServiceUnavailable, "export service unavailable"))
		return
	}
	if h.resolve == nil {
		// No identity boundary wired: fail as unauthenticated, never as
		// success (mirrors the endpoint rail's resolver contract).
		writeStable(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	user, err := h.resolve(r)
	if err != nil || user == nil || user.ID <= 0 || !user.IsActive() {
		writeStable(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	decision, err := h.limiter.Allow(user.ID, 0)
	if err != nil {
		writeStable(w, httperr.New(httperr.CodeServiceUnavailable, "export service unavailable"))
		return
	}
	if !decision.Allowed {
		setRetryAfter(w, decision.RetryAfter)
		writeStable(w, httperr.New(httperr.CodeRateLimited, "export rate limit reached"))
		return
	}
	if err := h.elevation.ConsumeElevated(w, r, user); err != nil {
		// The single-use token (if any) is now invalid; drop the browser
		// cookie so the SPA cannot keep replaying a dead capability.
		h.elevation.ClearElevatedCookie(w, r)
		writeStable(w, httperr.New(httperr.CodeElevationRequired, "elevated capability required"))
		return
	}
	payload, err := h.exporter.BuildExport(r.Context(), user)
	if err != nil {
		if errors.Is(err, db.ErrExportLimit) || errors.Is(err, ErrExportTooLarge) {
			writeStable(w, httperr.New(httperr.CodePayloadTooLarge, "export package too large"))
		} else {
			writeStable(w, httperr.New(httperr.CodeInternal, "export failed"))
		}
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"nonbiri-account-export-%d-%d.json\"", user.ID, time.Now().Unix()))
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	_, _ = w.Write(payload)
}

// ExportService assembles the bounded export package from the ownership-scoped
// repository projections. The name carries the "export" qualifier because the
// lifecycle package is shared with the account-deletion rail (its own service
// is named Service).
type ExportService struct {
	store *db.Store
}

// NewExportService validates the store dependency and returns the export
// service.
func NewExportService(store *db.Store) *ExportService {
	return &ExportService{store: store}
}

// BuildExport assembles the whitelist JSON package for user. Every field is
// explicit; unknown or future columns are never projected. The marshaled
// package is bounded by MaxExportBytes; a projection that crosses its row
// limit fails closed with db.ErrExportLimit.
func (s *ExportService) BuildExport(ctx context.Context, user *db.User) ([]byte, error) {
	if s == nil || s.store == nil || user == nil || user.ID <= 0 || !user.IsActive() {
		return nil, ErrExportTooLarge
	}
	limit := db.ExportCollectionLimit
	endpoints, err := s.store.ListExportEndpoints(ctx, user.ID, limit)
	if err != nil {
		return nil, err
	}
	keys, err := s.store.ListExportEndpointKeys(ctx, user.ID, limit)
	if err != nil {
		return nil, err
	}
	models, err := s.store.ListExportModels(ctx, user.ID, limit)
	if err != nil {
		return nil, err
	}
	bindings, err := s.store.ListExportBindings(ctx, user.ID, limit)
	if err != nil {
		return nil, err
	}
	callerKey, err := s.store.GetCallerKey(user.ID)
	if err != nil {
		return nil, err
	}
	usage, err := s.store.GetUserUsage(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	logSummary, err := s.store.ExportLogSummaryForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	activityDaily, err := s.store.ListExportActivityDaily(ctx, user.ID, limit)
	if err != nil {
		return nil, err
	}
	creditLedger, err := s.store.ListExportCreditLedger(ctx, user.ID, limit)
	if err != nil {
		return nil, err
	}

	keysByEndpoint := make(map[int64][]exportKey, len(keys))
	for _, key := range keys {
		keysByEndpoint[key.EndpointID] = append(keysByEndpoint[key.EndpointID], exportKey{
			ID: key.ID, DisplayHead: key.DisplayHead, DisplayTail: key.DisplayTail,
			Note: key.Note, Enabled: key.Enabled, CreatedAt: key.CreatedAt, UpdatedAt: key.UpdatedAt,
		})
	}
	bindingsByModel := make(map[int64][]exportBinding, len(bindings))
	for _, binding := range bindings {
		bindingsByModel[binding.ModelID] = append(bindingsByModel[binding.ModelID], exportBinding{
			ID: binding.ID, EndpointKeyID: binding.EndpointKeyID,
			UpstreamModelID: binding.UpstreamModelID, Ord: binding.Ord, CreatedAt: binding.CreatedAt,
		})
	}

	packageValue := exportPackage{
		SchemaVersion: 2,
		ExportedAt:    time.Now().UTC(),
		User: exportUser{
			ID: user.ID, DiscordID: user.DiscordID, Username: user.Username, Avatar: user.Avatar,
			Lang: user.Lang, Credits: credits.FormatAmount(user.Credits),
			DonationCredit: credits.FormatAmount(user.DonationCredit),
			EndpointLimit:  user.EndpointLimit, RPMLimit: user.RPMLimit,
			CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
		},
		Endpoints: make([]exportEndpoint, 0, len(endpoints)),
		Models:    make([]exportModel, 0, len(models)),
		Usage: exportUsage{
			TotalRequests:              usage.TotalRequests,
			TotalUncachedInputTokens:   usage.TotalUncachedInputTokens,
			TotalCacheWriteInputTokens: usage.TotalCacheWriteInputTokens,
			TotalCacheReadInputTokens:  usage.TotalCacheReadInputTokens,
			TotalOutputTokens:          usage.TotalOutputTokens,
			TotalPromptTokens:          usage.TotalPromptTokens,
			TotalCompletionTokens:      usage.TotalCompletionTokens,
			TotalUnknownUsageRequests:  usage.TotalUnknownUsageRequests,
		},
		LogSummary: exportLogSummary{
			TotalLogs:        logSummary.TotalLogs,
			LogsLast30Days:   logSummary.LogsLast30Days,
			ErrorLogs:        logSummary.ErrorLogs,
			UsageUnknownLogs: logSummary.UsageUnknownLogs,
			AvgDurationMs:    logSummary.AvgDurationMs,
		},
		ActivityDaily: make([]db.ActivityDailyExportRow, 0, len(activityDaily)),
		CreditLedger:  make([]db.CreditLedgerExportRow, 0, len(creditLedger)),
	}
	for _, day := range activityDaily {
		packageValue.ActivityDaily = append(packageValue.ActivityDaily, day)
	}
	for _, entry := range creditLedger {
		packageValue.CreditLedger = append(packageValue.CreditLedger, entry)
	}
	for _, ep := range endpoints {
		packageValue.Endpoints = append(packageValue.Endpoints, exportEndpoint{
			ID: ep.ID, ConnectorType: ep.ConnectorType, BaseURL: ep.BaseURL, Note: ep.Note,
			Enabled: ep.Enabled, ModelFetchFailed: ep.ModelFetchFailed, ModelFetchFailedAt: ep.ModelFetchFailedAt,
			CreatedAt: ep.CreatedAt, UpdatedAt: ep.UpdatedAt, Keys: keysByEndpoint[ep.ID],
		})
	}
	for _, m := range models {
		packageValue.Models = append(packageValue.Models, exportModel{
			ID: m.ID, Provider: m.Provider, Model: m.Model, FullName: m.FullName,
			RouteStrategy: m.RouteStrategy, SilentRetry: m.SilentRetry,
			CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, Bindings: bindingsByModel[m.ID],
		})
	}
	if callerKey != nil {
		display := db.CallerKeyPrefix + callerKey.DisplayHead + "…" + callerKey.DisplayTail
		packageValue.CallerKey = &exportCallerKey{
			Display: display, DisplayHead: callerKey.DisplayHead, DisplayTail: callerKey.DisplayTail,
			CreatedAt: callerKey.CreatedAt, UpdatedAt: callerKey.UpdatedAt,
		}
	}

	payload, err := json.Marshal(packageValue)
	if err != nil {
		return nil, fmt.Errorf("marshal export package: %w", err)
	}
	if len(payload) > MaxExportBytes {
		return nil, ErrExportTooLarge
	}
	return payload, nil
}

// --- whitelist shapes -------------------------------------------------------
// Every field below is explicitly listed. Nothing else is ever marshaled, so
// a future column or struct field added to a repository type cannot leak into
// an export package without an explicit decision here.

type exportPackage struct {
	SchemaVersion int              `json:"schema_version"`
	ExportedAt    time.Time        `json:"exported_at"`
	User          exportUser       `json:"user"`
	Endpoints     []exportEndpoint `json:"endpoints"`
	Models        []exportModel    `json:"models"`
	CallerKey     *exportCallerKey `json:"caller_key,omitempty"`
	Usage         exportUsage      `json:"usage"`
	LogSummary    exportLogSummary `json:"log_summary"`
	// CreditLedger is the user's own append-only credit history (schema v2):
	// kind, deltas, resulting balances and a bounded reason. Economic values
	// are canonical decimal strings; the actor is an opaque nullable id.
	CreditLedger []db.CreditLedgerExportRow `json:"credit_ledger"`
	// ActivityDaily is the user's own per-day activity summary (schema v2):
	// counters and site-local day keys only — no model names and no request
	// content. The site-wide rollup table is never part of a personal export.
	ActivityDaily []db.ActivityDailyExportRow `json:"activity_daily"`
}

type exportUser struct {
	ID        int64  `json:"id"`
	DiscordID string `json:"discord_id"`
	Username  string `json:"username"`
	Avatar    string `json:"avatar"`
	Lang      string `json:"lang"`
	// Economic balances as canonical decimal strings: the signed consumption
	// balance and the cumulative donor-reward balance.
	Credits        string    `json:"credits"`
	DonationCredit string    `json:"donation_credit"`
	EndpointLimit  *int      `json:"endpoint_limit"`
	RPMLimit       *int      `json:"rpm_limit"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type exportEndpoint struct {
	ID                 int64       `json:"id"`
	ConnectorType      string      `json:"connector_type"`
	BaseURL            string      `json:"base_url"`
	Note               string      `json:"note"`
	Enabled            bool        `json:"enabled"`
	ModelFetchFailed   bool        `json:"model_fetch_failed"`
	ModelFetchFailedAt int64       `json:"model_fetch_failed_at"`
	CreatedAt          int64       `json:"created_at"`
	UpdatedAt          int64       `json:"updated_at"`
	Keys               []exportKey `json:"keys"`
}

type exportKey struct {
	ID          int64  `json:"id"`
	DisplayHead string `json:"display_head"`
	DisplayTail string `json:"display_tail"`
	Note        string `json:"note"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type exportModel struct {
	ID            int64           `json:"id"`
	Provider      string          `json:"provider"`
	Model         string          `json:"model"`
	FullName      string          `json:"full_name"`
	RouteStrategy string          `json:"route_strategy"`
	SilentRetry   bool            `json:"silent_retry"`
	CreatedAt     int64           `json:"created_at"`
	UpdatedAt     int64           `json:"updated_at"`
	Bindings      []exportBinding `json:"bindings"`
}

type exportBinding struct {
	ID              int64  `json:"id"`
	EndpointKeyID   int64  `json:"endpoint_key_id"`
	UpstreamModelID string `json:"upstream_model_id"`
	Ord             int64  `json:"ord"`
	CreatedAt       int64  `json:"created_at"`
}

type exportCallerKey struct {
	Display     string    `json:"display"`
	DisplayHead string    `json:"display_head"`
	DisplayTail string    `json:"display_tail"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type exportUsage struct {
	TotalRequests              int64 `json:"total_requests"`
	TotalUncachedInputTokens   int64 `json:"total_uncached_input_tokens"`
	TotalCacheWriteInputTokens int64 `json:"total_cache_write_input_tokens"`
	TotalCacheReadInputTokens  int64 `json:"total_cache_read_input_tokens"`
	TotalOutputTokens          int64 `json:"total_output_tokens"`
	// Compatibility mirrors: total_prompt_tokens is the sum of the three
	// input buckets, total_completion_tokens the output bucket. Rows recorded
	// before cache-aware collection carry no cache split (their legacy totals
	// were backfilled into the uncached/output buckets) and are never valid
	// for retroactive billing of cache-differentiated pricing.
	TotalPromptTokens         int64 `json:"total_prompt_tokens"`
	TotalCompletionTokens     int64 `json:"total_completion_tokens"`
	TotalUnknownUsageRequests int64 `json:"total_unknown_usage_requests"`
}

type exportLogSummary struct {
	TotalLogs        int64 `json:"total_logs"`
	LogsLast30Days   int64 `json:"logs_last_30_days"`
	ErrorLogs        int64 `json:"error_logs"`
	UsageUnknownLogs int64 `json:"usage_unknown_logs"`
	AvgDurationMs    int64 `json:"avg_duration_ms"`
}

// --- helpers ---------------------------------------------------------------

func writeStable(w http.ResponseWriter, e httperr.Error) {
	httperr.WriteError(w, e)
}

func setRetryAfter(w http.ResponseWriter, retryAfter time.Duration) {
	if retryAfter <= 0 {
		return
	}
	seconds := int(retryAfter / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
}
