package reports

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	publicRoute                     = "/api/reports/credential-theft"
	adminBadgeRoute                 = "/admin/api/reports/badge"
	adminCasesRoute                 = "/admin/api/reports"
	adminCaseRoute                  = "/admin/api/reports/{id}"
	adminTargetsRoute               = "/admin/api/reports/{id}/targets"
	adminApproveRoute               = "/admin/api/reports/{id}/approve"
	adminRejectRoute                = "/admin/api/reports/{id}/reject"
	adminResumeRoute                = "/admin/api/reports/{id}/resume"
	maxUnixSecond             int64 = 253402300799
	replayWindowSeconds       int64 = 24 * 60 * 60
	caseRetentionSeconds      int64 = 90 * 24 * 60 * 60
	maxBaseURLBytes                 = 4096
	maxSecretBytes                  = 64 << 10
	maxNoteRunes                    = 2048
	maxNoteBytes                    = 4 * maxNoteRunes
	maxPublicRequestBodyBytes       = 80 << 10
	maxAdminRequestBodyBytes        = 256 << 10
	defaultPageLimit                = 50
	maxPageLimit                    = 100
	workerBatchLimit                = 100
	workerTransactionLimit          = 2 * time.Second
	defaultWorkerInterval           = 30 * time.Second
	publicConcurrency               = 8
)

const acceptedMessage = "If matching credentials exist, temporary protection will be applied and an administrator will review the report."

var acceptedResponseBody = []byte(`{"accepted":true,"message":"` + acceptedMessage + `"}` + "\n")

type BaseURLValidator interface {
	ValidateBaseURL(string) (string, error)
}

type GenerationTwoSubkeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

// EndpointKeyDeletionFunc is the sole approved-processing capability. It runs
// inside the report worker's transaction and must perform the integrated
// resource deletion path, including binding, donation, claim, and secret cleanup.
// It must not independently authorize or commit.
type EndpointKeyDeletionFunc func(context.Context, *sql.Tx, int64, int64, int64) error

// IssueProjectionHook filters and re-derives the owner-private issue
// projection whenever the persisted report reason set changes. It runs in the
// report owner's transaction and must not authorize or commit.
type IssueProjectionHook interface {
	ReconcileReportReason(context.Context, *sql.Tx, int64, int64) error
}

// DelayFunc makes the uniform public acceptance delay testable without
// weakening the production context-cancellable timer.
type DelayFunc func(context.Context, time.Duration) error

type Config struct {
	Store           *db.Store
	Connectors      *connector.Registry
	BaseURLs        BaseURLValidator
	KeyDeriver      GenerationTwoSubkeyDeriver
	Authorizer      *authz.Authorizer
	DeleteKey       EndpointKeyDeletionFunc
	IssueProjection IssueProjectionHook
	Random          io.Reader
	Now             func() time.Time
	Delay           DelayFunc
	GenerateID      func(string) (string, error)
	WorkerInterval  time.Duration
}

// RouteRegistrar is implemented by auth.Runtime. Its optional-user wrapper
// supplies either no principal or a live user session and applies the normal
// user-station maintenance and same-origin boundaries. Admin registration is
// maintenance-exempt but still passes through full ADM authentication.
type RouteRegistrar interface {
	RegisterOptionalUserRoute(string, string, auth.OptionalUserHandler) error
	RegisterAdminRoute(string, string, http.Handler) error
}

type PublicSubmission struct {
	ConnectorType    string
	CanonicalBaseURL string
	Secret           []byte
	Note             string
	IdempotencyKey   string
	SourceIP         [16]byte
	Reporter         *authz.Actor
}

func (submission *PublicSubmission) clear() {
	if submission == nil {
		return
	}
	clear(submission.Secret)
	submission.Secret = nil
}

func (*PublicSubmission) String() string   { return "[redacted credential report submission]" }
func (*PublicSubmission) GoString() string { return "[redacted credential report submission]" }

type AcceptedResponse struct {
	Accepted bool   `json:"accepted"`
	Message  string `json:"message"`
}

type BadgeResponse struct {
	Total    string            `json:"total"`
	ByStatus map[string]string `json:"by_status"`
}

type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

type CaseSummary struct {
	ID               string       `json:"id"`
	Status           string       `json:"status"`
	ProgressState    string       `json:"progress_state"`
	ConnectorType    string       `json:"connector_type"`
	CanonicalBaseURL string       `json:"canonical_base_url"`
	MaterialVersion  string       `json:"material_version"`
	TargetVersion    string       `json:"target_version"`
	Deadline         int64        `json:"deadline"`
	Counts           ReportCounts `json:"counts"`
	Retry            *ReportRetry `json:"retry"`
	CreatedAt        int64        `json:"created_at"`
	TerminalAt       *int64       `json:"terminal_at"`
}

type ReportCounts struct {
	Materials      string `json:"materials"`
	Targets        string `json:"targets"`
	DistinctOwners string `json:"distinct_owners"`
	Processed      string `json:"processed"`
	Deleted        string `json:"deleted"`
	Released       string `json:"released"`
}

type ReportRetry struct {
	AttemptCount   string `json:"attempt_count"`
	NextAttemptAt  int64  `json:"next_attempt_at"`
	LastErrorClass string `json:"last_error_class"`
}

type Reporter struct {
	UserID    string `json:"user_id"`
	DiscordID string `json:"discord_id"`
}

type Material struct {
	ID        string    `json:"id"`
	NoteText  string    `json:"note_text"`
	Reporter  *Reporter `json:"reporter"`
	SourceIP  string    `json:"source_ip"`
	CreatedAt int64     `json:"created_at"`
}

type CaseDetail struct {
	CaseSummary
	Materials Page[Material] `json:"materials"`
	Decision  *Decision      `json:"decision"`
}

type Decision struct {
	Action      string  `json:"action"`
	Reason      string  `json:"reason"`
	ActorUserID *string `json:"actor_user_id"`
	CreatedAt   int64   `json:"created_at"`
}

type TargetOwner struct {
	UserID      string `json:"user_id"`
	DiscordID   string `json:"discord_id"`
	DisplayName string `json:"display_name"`
}

type TargetEndpoint struct {
	ConnectorType    string `json:"connector_type"`
	CanonicalBaseURL string `json:"canonical_base_url"`
	DisplayHead      string `json:"display_head"`
	DisplayTail      string `json:"display_tail"`
}

type Target struct {
	ID                string         `json:"id"`
	TargetSequence    string         `json:"target_seq"`
	State             string         `json:"state"`
	EndpointKeyID     *string        `json:"endpoint_key_id"`
	KeyRef            string         `json:"key_ref"`
	Owner             *TargetOwner   `json:"owner"`
	Endpoint          TargetEndpoint `json:"endpoint"`
	DiscoveredVersion string         `json:"discovered_version"`
	DecidedVersion    *string        `json:"decided_version"`
	CreatedAt         int64          `json:"created_at"`
	UpdatedAt         int64          `json:"updated_at"`
}

type DecisionResponse struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	MaterialVersion string `json:"material_version"`
	TargetVersion   string `json:"target_version"`
}

type MutationResult struct {
	Value    DecisionResponse
	Status   int
	Body     []byte
	Replayed bool
}

type ApproveCommand struct {
	ExpectedMaterialVersion int64
	ExpectedTargetVersion   int64
	Reason                  string
	Confirmation            bool
	IdempotencyKey          string
}

type RejectCommand struct {
	ExpectedMaterialVersion int64
	ExpectedTargetVersion   int64
	Reason                  string
	IdempotencyKey          string
}

type ResumeCommand struct {
	ExpectedTargetVersion int64
	IdempotencyKey        string
}

type WorkerResult struct {
	CasesProcessed int
	More           bool
}
