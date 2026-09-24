// Package imageactivity owns queued, prepaid image generation and its private
// upstream adaptation. Prompts and generated images never enter durable state.
package imageactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	maxUnix         = int64(253402300799)
	maxJSON         = 256 << 10
	maxPrompt       = 64 << 10
	maxImage        = 32 << 20
	maxImages       = 64 << 20
	maxResponse     = 96 << 20
	executionMemory = int64(256 << 20)
	resultLifetime  = int64(600)
	taskLifetime    = int64(30 * 24 * 60 * 60)
)

var (
	ErrInvalid     = errors.New("image activity: invalid request")
	ErrConflict    = errors.New("image activity: revision or state conflict")
	ErrUnavailable = errors.New("image activity: unavailable")
	ErrCapacity    = errors.New("image activity: capacity exhausted")
	ErrNotFound    = errors.New("image activity: not found")
	ErrInvariant   = errors.New("image activity: invariant violation")
	ErrExportLimit = errors.New("image activity: export limit exceeded")
)

type Vault interface {
	secret.GenerationTwoContextCodec
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}
type SourceWriter interface {
	RecordImageSourceTx(context.Context, *sql.Tx, string, int64, int64) error
	DeleteImageSourcesTx(context.Context, *sql.Tx, int64) error
	DeleteImageTaskDataTx(context.Context, *sql.Tx, string) error
}
type DiagnosticScope interface {
	ErrorScope(context.Context, observability.DiagnosticRef) context.Context
}
type ActivityRecorder interface {
	RecordLimitedActivityTx(context.Context, *sql.Tx, int64, int64) error
}
type Config struct {
	Database    *sql.DB
	Users       limitedactivities.UserAuthorizer
	Admins      limitedactivities.AdminAuthorizer
	Gate        limitedactivities.AdmissionGate
	Admission   func(context.Context, *sql.Tx, int64, string, int64) (limitedactivities.Detail, error)
	Vault       Vault
	Egress      *egress.Stack
	Sources     SourceWriter
	Diagnostics DiagnosticScope
	Activity    ActivityRecorder
	Now         func() time.Time
	// ReportError receives operational failures; callers must not log raw private error text.
	ReportError func(error)
}
type Price struct {
	Paper string `json:"paper"`
	Brush string `json:"brush"`
}
type ParameterKey string

const (
	Prompt         ParameterKey = "prompt"
	NegativePrompt ParameterKey = "negative_prompt"
	N              ParameterKey = "n"
	Size           ParameterKey = "size"
	AspectRatio    ParameterKey = "aspect_ratio"
	Resolution     ParameterKey = "resolution"
	Seed           ParameterKey = "seed"
	Steps          ParameterKey = "steps"
	Guidance       ParameterKey = "guidance"
	Quality        ParameterKey = "quality"
)

type ParameterRule struct {
	Key        ParameterKey      `json:"key"`
	Supported  bool              `json:"supported"`
	Required   bool              `json:"required"`
	Type       string            `json:"type"`
	Minimum    *float64          `json:"minimum,omitempty"`
	Maximum    *float64          `json:"maximum,omitempty"`
	Step       *float64          `json:"step,omitempty"`
	Enum       []json.RawMessage `json:"enum,omitempty"`
	Default    json.RawMessage   `json:"default,omitempty"`
	MinLength  *int              `json:"min_length,omitempty"`
	MaxLength  *int              `json:"max_length,omitempty"`
	LengthUnit string            `json:"length_unit,omitempty"`
	Dimensions *DimensionRule    `json:"dimensions,omitempty"`
}
type DimensionRange struct {
	Minimum int `json:"minimum"`
	Maximum int `json:"maximum"`
	Step    int `json:"step"`
}
type DimensionRule struct {
	Format string         `json:"format"`
	Width  DimensionRange `json:"width"`
	Height DimensionRange `json:"height"`
}
type CombinationRule struct {
	Keys    []ParameterKey      `json:"keys"`
	Allowed [][]json.RawMessage `json:"allowed"`
}
type Model struct {
	ID           string            `json:"id"`
	DisplayName  string            `json:"display_name"`
	Description  string            `json:"description"`
	Revision     string            `json:"revision"`
	Price        Price             `json:"price"`
	Parameters   []ParameterRule   `json:"parameters"`
	Combinations []CombinationRule `json:"combinations"`
}
type SubmitInput struct {
	ModelID               string          `json:"model_id"`
	ExpectedModelRevision string          `json:"expected_model_revision"`
	Prompt                string          `json:"prompt"`
	NegativePrompt        json.RawMessage `json:"negative_prompt,omitempty"`
	N                     *int            `json:"n,omitempty"`
	Size                  json.RawMessage `json:"size,omitempty"`
	AspectRatio           json.RawMessage `json:"aspect_ratio,omitempty"`
	Resolution            json.RawMessage `json:"resolution,omitempty"`
	Seed                  json.RawMessage `json:"seed,omitempty"`
	Steps                 json.RawMessage `json:"steps,omitempty"`
	Guidance              json.RawMessage `json:"guidance,omitempty"`
	Quality               json.RawMessage `json:"quality,omitempty"`
}
type ImageInfo struct {
	Index int    `json:"index"`
	MIME  string `json:"mime"`
	Bytes int    `json:"bytes"`
}
type Task struct {
	ID              string      `json:"id"`
	ModelID         string      `json:"model_id"`
	Status          string      `json:"status"`
	BillingState    string      `json:"billing_state"`
	N               int         `json:"n"`
	ActualImages    int         `json:"actual_images"`
	CreatedAt       int64       `json:"created_at"`
	DispatchedAt    *int64      `json:"dispatched_at"`
	CompletedAt     *int64      `json:"completed_at"`
	Charge          Price       `json:"charge"`
	Refund          Price       `json:"refund"`
	QueuePosition   *int        `json:"queue_position"`
	ResultExpiresAt *int64      `json:"result_expires_at"`
	ResultAvailable bool        `json:"result_available"`
	ErrorCode       *string     `json:"error_code"`
	Images          []ImageInfo `json:"images"`
}
type TaskResult struct {
	Task Task `json:"task"`
}
type OwnPosition struct {
	TaskID     string `json:"task_id"`
	Position   *int   `json:"position"`
	AcceptedAt int64  `json:"accepted_at"`
}
type Queue struct {
	Queued         int           `json:"queued"`
	Running        int           `json:"running"`
	Own            []OwnPosition `json:"own"`
	DispatchPaused bool          `json:"dispatch_paused"`
}
type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}
type MutationResult[T any] struct {
	Value    T
	Replayed bool
}
type UserPrincipal struct{ UserID int64 }
type AdminPrincipal struct{ UserID int64 }
type AuthorizedUserHandler func(http.ResponseWriter, *http.Request, UserPrincipal)
type AuthorizedAdminHandler func(http.ResponseWriter, *http.Request, AdminPrincipal)
type UserRouteRegistrar interface {
	RegisterUserRoute(string, string, AuthorizedUserHandler) error
}
type AdminRouteRegistrar interface {
	RegisterAdminRoute(string, string, AuthorizedAdminHandler) error
}
