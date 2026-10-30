// Package agentrequest owns dedicated model transport, scheduling and request
// history. It does not grant permission to execute model-proposed operations.
package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/chatgptauth"
	"github.com/waiting-here/NonbiriAPI/internal/codexauth"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

var (
	ErrInvalid      = errors.New("agent request: invalid input")
	ErrConflict     = errors.New("agent request: revision conflict")
	ErrNotFound     = errors.New("agent request: not found")
	ErrUnconfigured = errors.New("agent request: driver is not configured")
	ErrClosed       = errors.New("agent request: runtime closed")
	ErrInterrupted  = errors.New("agent request: request interrupted")
	ErrUpstream     = errors.New("agent request: upstream request failed")
	ErrProtocol     = errors.New("agent request: invalid model response")
	ErrCapacity     = errors.New("agent request: queue capacity reached")
)

type Settings struct {
	CodexAccountID       string          `json:"-"`
	ConnectionRevision   int64           `json:"connection_revision,string"`
	DriverID             int64           `json:"driver_id,string"`
	Provider             string          `json:"provider"`
	OutputReserveTokens  int             `json:"output_reserve_tokens"`
	OutputTokenParameter string          `json:"output_token_parameter"`
	Revision             int64           `json:"revision,string"`
	BaseURL              string          `json:"base_url"`
	HasKey               bool            `json:"has_key"`
	Model                string          `json:"model"`
	ContextWindow        int             `json:"context_window"`
	MaxOutputTokens      int             `json:"max_output_tokens"`
	Temperature          *float64        `json:"temperature,omitempty"`
	TopP                 *float64        `json:"top_p,omitempty"`
	ExtraParameters      json.RawMessage `json:"extra_parameters"`
	Concurrency          int             `json:"concurrency"`
	TimeoutSeconds       int             `json:"timeout_seconds"`
}
type SettingsUpdate struct {
	OutputTokenParameter string          `json:"output_token_parameter"`
	ExpectedRevision     int64           `json:"expected_revision,string"`
	BaseURL              string          `json:"base_url"`
	APIKey               *string         `json:"api_key,omitempty"`
	Model                string          `json:"model"`
	ContextWindow        int             `json:"context_window"`
	MaxOutputTokens      int             `json:"max_output_tokens"`
	Temperature          *float64        `json:"temperature,omitempty"`
	TopP                 *float64        `json:"top_p,omitempty"`
	ExtraParameters      json.RawMessage `json:"extra_parameters"`
	Concurrency          int             `json:"concurrency"`
	TimeoutSeconds       int             `json:"timeout_seconds"`
}
type Attribution struct {
	ClientRequestID string `json:"client_request_id"`
	Source          string `json:"source"` // auto_review, admin_chat or steward_chat
	Kind            string `json:"kind"`   // model or compaction
	ActorID         int64  `json:"actor_id,string"`
	ActorRole       string `json:"actor_role"` // admin, level5 or level6
	ActorLabel      string `json:"actor_label"`
}
type Input struct {
	Attribution
	Messages       json.RawMessage `json:"messages"`
	Tools          json.RawMessage `json:"tools,omitempty"`
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
	ToolChoice     json.RawMessage `json:"tool_choice,omitempty"`
}
type Usage struct {
	InputTokens           int64 `json:"input_tokens,string"`
	CachedInputTokens     int64 `json:"cached_input_tokens,string"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens,string"`
	OutputTokens          int64 `json:"output_tokens,string"`
	ReasoningTokens       int64 `json:"reasoning_tokens,string"`
	Unknown               bool  `json:"unknown"`
}
type Request struct {
	ID       int64  `json:"id,string"`
	DriverID int64  `json:"driver_id,string"`
	Provider string `json:"provider"`
	Attribution
	State            string `json:"state"` // pending, running, succeeded, failed, cancelled, interrupted
	BaseURL          string `json:"base_url"`
	Model            string `json:"model"`
	CreatedAt        int64  `json:"created_at"` // Unix milliseconds
	StartedAt        *int64 `json:"started_at,omitempty"`
	ConsumedAt       *int64 `json:"consumed_at,omitempty"`
	CompletedAt      *int64 `json:"completed_at,omitempty"`
	StatusCode       int    `json:"status_code"`
	ErrorCode        string `json:"error_code"`
	ContentRedacted  bool   `json:"content_redacted"`
	ResponseComplete bool   `json:"response_complete"`
	Usage            Usage  `json:"usage"`
}
type Detail struct {
	ErrorDiagnostics *diagnostic.Failure `json:"error_diagnostics,omitempty"`
	DriverSettings   json.RawMessage     `json:"driver_settings"`
	QueuedInput      json.RawMessage     `json:"queued_input"`
	Request
	RequestBody        string `json:"request_body"`
	ResponseBody       string `json:"response_body"`
	ResponseBodyBase64 string `json:"response_body_base64,omitempty"`
}
type Result struct {
	RequestID    int64           `json:"request_id,string"`
	Message      json.RawMessage `json:"message"`
	RawResponse  string          `json:"raw_response"`
	FinishReason string          `json:"finish_reason"`
	Usage        Usage           `json:"usage"`
}
type QueueStatus struct {
	Length   int `json:"length"`
	Active   int `json:"active"`
	Limit    int `json:"limit"`
	Position int `json:"position"` // 1-based pending position, zero otherwise
}
type Event struct {
	RequestID       int64       `json:"request_id,string"`
	ClientRequestID string      `json:"client_request_id"`
	State           string      `json:"state"`
	Queue           QueueStatus `json:"queue"`
}
type ListFilter struct {
	Provider string
	From, To int64
	Source   string
	Kind     string
	ActorID  *int64
	State    string
	BeforeID int64
	Page     int
	PageSize int
}
type Page struct {
	Items    []Request `json:"items"`
	Total    int64     `json:"total,string"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
	CutoffID int64     `json:"cutoff_id,string"`
}
type CleanupResult struct {
	DeletedCount int64 `json:"deleted_count,string"`
	SkippedCount int64 `json:"skipped_count,string"`
}
type CleanupInput struct {
	Provider  string   `json:"provider,omitempty"`
	From      int64    `json:"from,string,omitempty"`
	To        int64    `json:"to,string,omitempty"`
	State     string   `json:"state,omitempty"`
	IDs       []string `json:"ids,omitempty"`
	ThroughID int64    `json:"through_id,string,omitempty"` // fixed by the confirmation view
	Source    string   `json:"source,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	ActorID   *int64   `json:"actor_id,string,omitempty"`
}
type UsageSummary struct {
	ActorID    int64  `json:"actor_id,string"`
	ActorRole  string `json:"actor_role"`
	ActorLabel string `json:"actor_label"`
	Source     string `json:"source"`
	Kind       string `json:"kind"`
	Requests   int64  `json:"requests,string"`
	Usage      Usage  `json:"usage"`
}
type UsageReport struct {
	Total       []UsageSummary `json:"total"`
	Last24Hours []UsageSummary `json:"last_24_hours"`
	AsOf        int64          `json:"as_of"`
}
type Config struct {
	Codex          *codexauth.Manager
	ChatGPT        *chatgptauth.Manager
	AdminAuthorize func(context.Context, *sql.Tx) error
	DB             *sql.DB
	ReadDB         *sql.DB
	Stack          *egress.Stack
	Flow           *flowcontrol.Controller
	Secrets        secret.AgentCodec
	Now            func() time.Time
	// Authorize revalidates the durable job/context and live actor immediately
	// before dispatch. It must not keep a database transaction open.
	Authorize func(context.Context, Attribution) error
	// OnEvent is called after persistence and outside locks. It must return
	// promptly; durable state remains authoritative if a UI misses an event.
	OnEvent func(Event)
}
