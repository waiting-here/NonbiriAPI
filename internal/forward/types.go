// Package forward owns the Generation 2 CallerKey-only OpenAI-compatible
// public exit. It composes logical routing, dispatch claims, connectors, and
// Debug capture without reading a credential or posting an arbitrary ledger
// operation.
package forward

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	MaxRouteAttempts       = claim.MaxAttempts
	MaxCallerModels        = 1000
	MaxModelListBytes      = 1 << 20
	DefaultForwardTimeout  = 5 * time.Minute
	DefaultSettlementLimit = 30 * time.Second
	DefaultBackoffBase     = 200 * time.Millisecond
	DefaultBackoffMax      = 2 * time.Second
)

var (
	ErrInvalidConfiguration = errors.New("forward: invalid configuration")
	ErrClosed               = errors.New("forward: service is closed")
	ErrInternal             = errors.New("forward: internal failure")
)

// CallerKeyResolver is the only credential verifier consumed by the public
// exit. Implementations return an already account-validated Generation 2
// identity and never expose stored verifier material.
type CallerKeyResolver interface {
	ResolveCallerKey(context.Context, string) (resources.CallerIdentity, error)
}

// PersonalPreflight contains candidate-free owner-scoped logical facts.
type PersonalPreflight struct {
	ModelID          int64
	OwnerUserID      int64
	Provider         string
	Model            string
	FullName         string
	RouteStrategy    string
	SilentRetry      bool
	FlattenToolCalls bool
	Revision         int64
	BindingRevision  int64
}

// CharityPreflight contains candidate-free charity policy and price facts.
type CharityPreflight struct {
	ModelID          int64
	Provider         string
	Model            string
	FullName         string
	FlattenToolCalls bool
	ReservedMilli    int64
}

// RouteCandidate is one credential-free physical candidate frozen before
// request acceptance. Its routine formatting is always redacted.
type RouteCandidate struct {
	EndpointID       int64
	EndpointKeyID    int64
	DonationKeyID    int64
	ConnectorType    connectorcontract.Type
	CanonicalBaseURL string
	UpstreamModelID  string
	Policy           connectorcontract.AttemptPolicy
	Order            int
}

func (RouteCandidate) String() string   { return "[redacted forward candidate]" }
func (RouteCandidate) GoString() string { return "[redacted forward candidate]" }
func (RouteCandidate) LogValue() slog.Value {
	return slog.StringValue("[redacted forward candidate]")
}

type PersonalSnapshot struct {
	PersonalPreflight
	Candidates []RouteCandidate
}

type CharitySnapshot struct {
	CharityPreflight
	Candidates []RouteCandidate
}

type ListedModel struct {
	ModelID   int64
	Provider  string
	FullName  string
	CreatedAt int64
}

// PersonalRouter is the two-stage personal routing boundary. Preflight must
// not inspect physical candidates; Snapshot is called only on non-Dry paths.
type PersonalRouter interface {
	Preflight(context.Context, int64, string) (PersonalPreflight, error)
	Snapshot(context.Context, int64, string) (PersonalSnapshot, error)
	ListRoutableModels(context.Context, int64, int) ([]ListedModel, error)
}

// CharityRouter is the two-stage charity routing boundary. Preflight owns
// caller/content/credit policy but not candidate health or quota inspection.
type CharityRouter interface {
	Preflight(context.Context, int64, string, *openai.ChatRequest, int64) (CharityPreflight, error)
	Snapshot(context.Context, int64, int64, []connectorcontract.Type) (CharitySnapshot, error)
	ListAvailableModels(context.Context, int64, int) ([]ListedModel, error)
}

// ClaimRail is the complete closed dispatch state machine consumed here.
type ClaimRail interface {
	Accept(context.Context, claim.AcceptInput) (claim.Request, error)
	Claim(context.Context, claim.ClaimInput) (claim.Handle, error)
	TakeForDispatch(context.Context, claim.Handle) (DispatchGrant, error)
	ReleaseUndispatched(context.Context, claim.Handle) (claim.Attempt, error)
	CompleteAttempt(context.Context, claim.Handle, claim.AttemptOutcome) (claim.Attempt, error)
	CompleteRequest(context.Context, claim.CompleteRequestInput) (claim.Request, error)
}

// DispatchGrant is the one-shot credential transfer returned only after the
// claim rail has committed its dispatched marker. The production claim value
// implements this interface directly; the adapter below keeps claim's exact
// method signatures out of the forward composition surface.
type DispatchGrant interface {
	Target() connectorcontract.Target
	Policy() connectorcontract.AttemptPolicy
	TakeCredential() (*connectorcontract.ShortLivedSecret, bool)
	Clear()
}

type CharityChargeCalculator interface {
	CalculateRequestCharge(context.Context, string, claim.AccountingDisposition) (int64, error)
}

type DebugCapture interface {
	DecideAfterAdmission(context.Context, debug.CaptureInput) (debug.CaptureDecision, error)
}

// Config is the production composition surface. Connector instances are
// constructed by root wiring; forward validates them against Registry and
// never reaches Backend, egress, or Vault directly.
type Config struct {
	Personal       PersonalRouter
	Charity        CharityRouter
	Claims         ClaimRail
	CharityCharges CharityChargeCalculator
	Debug          DebugCapture
	Registry       *connector.Registry
	Connectors     []connector.Connector
	Safety         *SafetyIdentifierFactory
	Observer       *connector.SafeObserver
	Now            func() time.Time
	ForwardTimeout time.Duration
	Settlement     time.Duration
	Backoff        BackoffConfig
}

func (Config) String() string   { return "[forward config]" }
func (Config) GoString() string { return "[forward config]" }
func (Config) LogValue() slog.Value {
	return slog.StringValue("[forward config]")
}

// Model and ModelList retain the tagged alpha.3 OpenAI-compatible list wire.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type Handler struct {
	service *Service
}

var _ http.Handler = (*Handler)(nil)
