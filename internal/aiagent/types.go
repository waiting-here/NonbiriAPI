// Package aiagent coordinates administrator-owned assistants. Model output is
// a proposed plan; it never confers authority to modify site resources.
package aiagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

var (
	ErrInvalid      = errors.New("ai agent: invalid input")
	ErrForbidden    = errors.New("ai agent: forbidden")
	ErrConflict     = errors.New("ai agent: the conversation or resource has changed")
	ErrNotFound     = errors.New("ai agent: not found")
	ErrUnavailable  = errors.New("ai agent: unavailable")
	ErrInterrupted  = errors.New("ai agent: interrupted")
	ErrChatDisabled = errors.New("ai agent: chat is not available")
)

type Actor = agentplan.Actor

type TemplateMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Templates support shared memories, intelligence, interlocutor identity,
// personal memory and the conversation node.
// A conversation placeholder is a separate node so original tool messages
// retain their roles and call identifiers rather than becoming quoted text.
type Settings struct {
	Revision         int64             `json:"revision,string"`
	Mode             string            `json:"mode"`
	ChatEnabled      bool              `json:"chat_enabled"`
	AutoTemplate     []TemplateMessage `json:"auto_template"`
	ChatTemplate     []TemplateMessage `json:"chat_template"`
	AutoPrefill      string            `json:"auto_prefill"`
	ChatPrefill      string            `json:"chat_prefill"`
	Tools            map[string]bool   `json:"tools"`
	ToolDescriptions map[string]string `json:"tool_descriptions"`
	SearchURL        string            `json:"search_url"`
	SearchHasKey     bool              `json:"search_has_key"`
}

type SettingsUpdate struct {
	Settings
	ExpectedRevision int64   `json:"expected_revision,string"`
	SearchAPIKey     *string `json:"search_api_key,omitempty"`
}

type Memory struct {
	Kind      string `json:"kind"`
	Content   string `json:"content"`
	Revision  int64  `json:"revision,string"`
	UpdatedAt int64  `json:"updated_at"`
	UpdatedBy string `json:"updated_by"`
	OwnerID   int64  `json:"owner_id,string,omitempty"`
	Writable  bool   `json:"writable"`
}

type MemoryOwner struct {
	ID    int64  `json:"id,string"`
	Role  string `json:"role"`
	Label string `json:"label"`
}

type MemoryOwnerPage struct {
	Page[MemoryOwner]
	CurrentOwnerID int64 `json:"current_owner_id,string"`
}

type Message struct {
	ID         int64           `json:"id,string"`
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	CreatedAt  int64           `json:"created_at"`
	Active     bool            `json:"active"`
	Generation int64           `json:"generation,string"`
	Wire       json.RawMessage `json:"-"`
}

type Conversation struct {
	Enabled    bool      `json:"enabled"`
	ID         int64     `json:"id,string"`
	Generation int64     `json:"generation,string"`
	Messages   []Message `json:"messages"`
	Summary    string    `json:"summary"`
	Job        *Job      `json:"job,omitempty"`
	Plans      []Plan    `json:"plans"`
	HasOlder   bool      `json:"has_older"`
}

type SendInput struct {
	Content            string `json:"content"`
	ExpectedGeneration int64  `json:"expected_generation,string"`
	RequestKey         string `json:"request_key"`
}

type Job struct {
	ID             int64                    `json:"id,string"`
	Kind           string                   `json:"kind"`
	ActorID        int64                    `json:"actor_id,string"`
	ActorRole      string                   `json:"actor_role"`
	ActorLabel     string                   `json:"actor_label"`
	ConversationID int64                    `json:"conversation_id,string"`
	Generation     int64                    `json:"generation,string"`
	DonationID     int64                    `json:"donation_id,string"`
	State          string                   `json:"state"`
	RequestID      int64                    `json:"request_id,string"`
	Error          string                   `json:"error"`
	CreatedAt      int64                    `json:"created_at"`
	UpdatedAt      int64                    `json:"updated_at"`
	Queue          agentrequest.QueueStatus `json:"queue"`
}

type Operation = agentplan.Operation
type OperationPreview = agentplan.Preview

type Plan struct {
	ID          int64              `json:"id,string"`
	JobID       int64              `json:"job_id,string"`
	State       string             `json:"state"`
	Operations  []Operation        `json:"operations"`
	Preview     []OperationPreview `json:"preview"`
	ToolCallIDs []string           `json:"tool_call_ids"`
	Result      json.RawMessage    `json:"result,omitempty"`
	CreatedAt   int64              `json:"created_at"`
	DecidedAt   *int64             `json:"decided_at,omitempty"`
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	ChatOnly    bool            `json:"chat_only"`
}

type ReviewResult struct {
	Decision       string          `json:"decision"`
	InternalReason string          `json:"internal_reason"`
	UserNote       string          `json:"user_note"`
	Bindings       []ReviewBinding `json:"bindings"`
}

type ReviewBinding struct {
	CharityModelID          string   `json:"charity_model_id"`
	ExpectedBindingRevision string   `json:"expected_binding_revision"`
	DonationKeyID           string   `json:"donation_key_id"`
	UpstreamModelID         string   `json:"upstream_model_id"`
	NativeServices          []string `json:"native_services"`
}

type Review struct {
	Job          Job             `json:"job"`
	Result       *ReviewResult   `json:"result,omitempty"`
	Execution    json.RawMessage `json:"execution,omitempty"`
	Intelligence json.RawMessage `json:"intelligence"`
	Turns        []ModelResult   `json:"turns,omitempty"`
}

type ModelResult struct {
	RequestID int64           `json:"request_id,string"`
	Kind      string          `json:"kind"`
	Message   json.RawMessage `json:"message"`
	Error     string          `json:"error"`
	CreatedAt int64           `json:"created_at"`
}

type Page[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total,string"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

type PreparedBatch = agentplan.PreparedBatch

type Operations interface {
	Tools(context.Context, Actor, bool) ([]Tool, error)
	Read(context.Context, Actor, string, json.RawMessage) (json.RawMessage, error)
	Preview(context.Context, Actor, []Operation) ([]OperationPreview, error)
	// Prepare acquires domain lifecycle gates before the writer transaction.
	// Apply rechecks authority and versions in that final transaction.
	Prepare(context.Context, Actor, []Operation) (PreparedBatch, error)
	Intelligence(context.Context, int64) (json.RawMessage, string, error)
	// ReviewOperations builds a whole-donation review plus existing-model
	// bindings from a strict model result and the captured revision signature.
	ReviewOperations(context.Context, int64, string, ReviewResult) ([]Operation, error)
}

type SearchSecrets interface {
	SealAgentKey([]byte, string) (string, error)
	OpenAgentKey(string, string) ([]byte, error)
}

type Config struct {
	History    *managementlog.Service
	DB         *sql.DB
	ReadDB     *sql.DB
	Driver     ModelDriver
	Operations Operations
	Now        func() time.Time
	Authorize  func(context.Context, *sql.Tx, Actor, string) error
	// Search uses the existing controlled egress stack, not a raw HTTP client.
	Search  func(context.Context, Actor, string, []byte, string) (json.RawMessage, error)
	Secrets SearchSecrets
}

type ModelDriver interface {
	ResolveSettings(context.Context, agentrequest.Attribution) (agentrequest.Settings, error)
	ReadyFor(context.Context, *sql.Tx, agentrequest.Attribution) (bool, error)
	Submit(context.Context, agentrequest.Input) (agentrequest.Request, error)
	Wait(context.Context, int64) (agentrequest.Result, error)
	Cancel(context.Context, int64) error
	Queue(context.Context, int64) (agentrequest.QueueStatus, error)
	AckInTransaction(context.Context, *sql.Tx, int64) error
	Unconsumed(context.Context, int) ([]agentrequest.Request, error)
}
