// Package charityrouting owns logical charity models, their ordered bindings,
// and the safe immutable candidate projection consumed by the claim rail.
package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

var (
	ErrInvalidRequest      = errors.New("charity routing: invalid request")
	ErrUnauthorized        = errors.New("charity routing: unauthorized")
	ErrForbidden           = errors.New("charity routing: forbidden")
	ErrNotFound            = errors.New("charity routing: not found")
	ErrConflict            = errors.New("charity routing: conflict")
	ErrResourceLimit       = errors.New("charity routing: resource limit exceeded")
	ErrUnavailable         = errors.New("charity routing: unavailable")
	ErrEntropyUnavailable  = errors.New("charity routing: candidate ordering entropy unavailable")
	ErrInvariant           = errors.New("charity routing: invariant violation")
	ErrFeatureDisabled     = errors.New("charity routing: feature disabled")
	ErrCharitySuspended    = errors.New("charity routing: caller suspended")
	ErrInsufficientCredits = errors.New("charity routing: insufficient credits")
	ErrContentTooShort     = errors.New("charity routing: content too short")
)

type ContentTooShortError struct {
	Actual  int
	Minimum int
}

func (e *ContentTooShortError) Error() string {
	if e == nil {
		return ErrContentTooShort.Error()
	}
	return fmt.Sprintf("charity routing: content has %d runes; minimum is %d", e.Actual, e.Minimum)
}

func (*ContentTooShortError) Unwrap() error { return ErrContentTooShort }

type RoleFinalTxAuthorizer = donation.RoleFinalTxAuthorizer
type AdminRouteRegistrar = donation.AdminRouteRegistrar
type UserRouteRegistrar = resources.UserRouteRegistrar
type UserPrincipal = resources.UserPrincipal
type AuthorizedUserHandler = resources.AuthorizedUserHandler

type DonationStateOwner interface {
	MaterializeExpiryTx(context.Context, *sql.Tx, int64, int64) (bool, error)
	MaterializeDueExpiriesTx(context.Context, *sql.Tx, int64, int) error
}

type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

type CapabilityModel struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	FullName string `json:"full_name"`
}

type Capability struct {
	State          string            `json:"state"`
	Models         []CapabilityModel `json:"models"`
	DonationIntake string            `json:"donation_intake"`
}

type AdminTokenPrices struct {
	UncachedInput   string `json:"uncached_input"`
	CacheWriteInput string `json:"cache_write_input"`
	CacheReadInput  string `json:"cache_read_input"`
	Output          string `json:"output"`
}

type AdminPricing struct {
	Mode         string            `json:"mode"`
	UserPrice    *string           `json:"user_price,omitempty"`
	DonorReward  *string           `json:"donor_reward,omitempty"`
	UserPrices   *AdminTokenPrices `json:"user_prices,omitempty"`
	DonorRewards *AdminTokenPrices `json:"donor_rewards,omitempty"`
}

type AdminDiscount struct {
	Enabled bool   `json:"enabled"`
	Percent int    `json:"percent"`
	StartAt *int64 `json:"start_at"`
	EndAt   *int64 `json:"end_at"`
}

type AdminRollingSuccess struct {
	SampleCount  string  `json:"sample_count"`
	SuccessCount string  `json:"success_count"`
	Percent      *string `json:"percent"`
}

type AdminCharityModel struct {
	ID               string              `json:"id"`
	Provider         string              `json:"provider"`
	Model            string              `json:"model"`
	FullName         string              `json:"full_name"`
	Enabled          bool                `json:"enabled"`
	Pricing          AdminPricing        `json:"pricing"`
	Discount         AdminDiscount       `json:"discount"`
	FlattenToolCalls bool                `json:"flatten_tool_calls"`
	Revision         string              `json:"revision"`
	BindingRevision  string              `json:"binding_revision"`
	BindingCount     string              `json:"binding_count"`
	RollingSuccess   AdminRollingSuccess `json:"rolling_success"`
	CreatedAt        int64               `json:"created_at"`
	UpdatedAt        int64               `json:"updated_at"`
}

// Steward DTOs are deliberately independently compiled, including nested
// role projections.
type StewardTokenPrices struct {
	UncachedInput   string `json:"uncached_input"`
	CacheWriteInput string `json:"cache_write_input"`
	CacheReadInput  string `json:"cache_read_input"`
	Output          string `json:"output"`
}

type StewardPricing struct {
	Mode         string              `json:"mode"`
	UserPrice    *string             `json:"user_price,omitempty"`
	DonorReward  *string             `json:"donor_reward,omitempty"`
	UserPrices   *StewardTokenPrices `json:"user_prices,omitempty"`
	DonorRewards *StewardTokenPrices `json:"donor_rewards,omitempty"`
}

type StewardDiscount struct {
	Enabled bool   `json:"enabled"`
	Percent int    `json:"percent"`
	StartAt *int64 `json:"start_at"`
	EndAt   *int64 `json:"end_at"`
}

type StewardRollingSuccess struct {
	SampleCount  string  `json:"sample_count"`
	SuccessCount string  `json:"success_count"`
	Percent      *string `json:"percent"`
}

type StewardCharityModel struct {
	ID               string                `json:"id"`
	Provider         string                `json:"provider"`
	Model            string                `json:"model"`
	FullName         string                `json:"full_name"`
	Enabled          bool                  `json:"enabled"`
	Pricing          StewardPricing        `json:"pricing"`
	Discount         StewardDiscount       `json:"discount"`
	FlattenToolCalls bool                  `json:"flatten_tool_calls"`
	Revision         string                `json:"revision"`
	BindingRevision  string                `json:"binding_revision"`
	BindingCount     string                `json:"binding_count"`
	RollingSuccess   StewardRollingSuccess `json:"rolling_success"`
	CreatedAt        int64                 `json:"created_at"`
	UpdatedAt        int64                 `json:"updated_at"`
}

type CandidateSource struct {
	ConnectorType    string `json:"connector_type"`
	CanonicalBaseURL string `json:"canonical_base_url"`
	DisplayHead      string `json:"display_head"`
	DisplayTail      string `json:"display_tail"`
}

type AdminBindingCandidate struct {
	DonationKeyID   string          `json:"donation_key_id"`
	DonationID      string          `json:"donation_id"`
	Source          CandidateSource `json:"source"`
	UpstreamModelID string          `json:"upstream_model_id"`
	SourceTypes     []string        `json:"source_types"`
}

type StewardCandidateSource struct {
	ConnectorType    string `json:"connector_type"`
	CanonicalBaseURL string `json:"canonical_base_url"`
	DisplayHead      string `json:"display_head"`
	DisplayTail      string `json:"display_tail"`
}

type StewardBindingCandidate struct {
	DonationKeyID   string                 `json:"donation_key_id"`
	DonationID      string                 `json:"donation_id"`
	Source          StewardCandidateSource `json:"source"`
	UpstreamModelID string                 `json:"upstream_model_id"`
	SourceTypes     []string               `json:"source_types"`
}

type AdminBinding struct {
	ID              string          `json:"id"`
	Ord             int             `json:"ord"`
	DonationKeyID   string          `json:"donation_key_id"`
	DonationID      string          `json:"donation_id"`
	Source          CandidateSource `json:"source"`
	UpstreamModelID string          `json:"upstream_model_id"`
	SourceTypes     []string        `json:"source_types"`
}

type StewardBinding struct {
	ID              string                 `json:"id"`
	Ord             int                    `json:"ord"`
	DonationKeyID   string                 `json:"donation_key_id"`
	DonationID      string                 `json:"donation_id"`
	Source          StewardCandidateSource `json:"source"`
	UpstreamModelID string                 `json:"upstream_model_id"`
	SourceTypes     []string               `json:"source_types"`
}

type AdminBindings struct {
	Bindings        []AdminBinding `json:"bindings"`
	BindingRevision string         `json:"binding_revision"`
}

type StewardBindings struct {
	Bindings        []StewardBinding `json:"bindings"`
	BindingRevision string           `json:"binding_revision"`
}

type TokenPricesInput struct {
	UncachedInput   string `json:"uncached_input"`
	CacheWriteInput string `json:"cache_write_input"`
	CacheReadInput  string `json:"cache_read_input"`
	Output          string `json:"output"`
}

type PricingInput struct {
	Mode         string            `json:"mode"`
	UserPrice    *string           `json:"user_price,omitempty"`
	DonorReward  *string           `json:"donor_reward,omitempty"`
	UserPrices   *TokenPricesInput `json:"user_prices,omitempty"`
	DonorRewards *TokenPricesInput `json:"donor_rewards,omitempty"`
}

type DiscountInput struct {
	Enabled bool   `json:"enabled"`
	Percent int    `json:"percent"`
	StartAt *int64 `json:"start_at"`
	EndAt   *int64 `json:"end_at"`
}

type DiscountPatchInput struct {
	Enabled *bool
	Percent *int
	StartAt **int64
	EndAt   **int64
}

type ModelCreate struct {
	Provider         string        `json:"provider"`
	Model            string        `json:"model"`
	Enabled          bool          `json:"enabled"`
	Pricing          PricingInput  `json:"pricing"`
	Discount         DiscountInput `json:"discount"`
	FlattenToolCalls bool          `json:"flatten_tool_calls"`
}

type ModelPatch struct {
	ExpectedRevision string        `json:"expected_revision"`
	Provider         *string       `json:"provider,omitempty"`
	Model            *string       `json:"model,omitempty"`
	Enabled          *bool         `json:"enabled,omitempty"`
	Pricing          *PricingInput `json:"pricing,omitempty"`
	Discount         *DiscountPatchInput
	FlattenToolCalls *bool `json:"flatten_tool_calls,omitempty"`
}

type ModelDelete struct {
	ExpectedRevision string `json:"expected_revision"`
	Confirmation     string `json:"confirmation"`
}

type BindingSelection struct {
	DonationKeyID   string `json:"donation_key_id"`
	UpstreamModelID string `json:"upstream_model_id"`
}

type BindingBatch struct {
	ExpectedBindingRevision string             `json:"expected_binding_revision"`
	Selections              []BindingSelection `json:"selections"`
}

type BindingOrder struct {
	ExpectedBindingRevision string   `json:"expected_binding_revision"`
	Order                   []string `json:"order"`
}

type BindingDelete struct {
	ExpectedBindingRevision string `json:"expected_binding_revision"`
}

type CandidateQuery struct {
	DonationID    int64
	DonationKeyID int64
	Source        string
	Query         string
	AfterKeyID    int64
	AfterModelID  string
	Limit         int
}

// RuntimeCandidate is a frozen, credential-free candidate. String/log
// formatting is always redacted because its safe routing facts are still not a
// caller projection.
type RuntimeCandidate struct {
	DonationKeyID    int64
	EndpointID       int64
	EndpointKeyID    int64
	ConnectorType    connectorcontract.Type
	CanonicalBaseURL string
	UpstreamModelID  string
	Policy           connectorcontract.AttemptPolicy
}

func (RuntimeCandidate) String() string   { return "[redacted charity candidate]" }
func (RuntimeCandidate) GoString() string { return "[redacted charity candidate]" }
func (RuntimeCandidate) LogValue() slog.Value {
	return slog.StringValue("[redacted charity candidate]")
}

func (candidate RuntimeCandidate) ClaimCandidate() claim.Candidate {
	return claim.Candidate{
		EndpointID: candidate.EndpointID, EndpointKeyID: candidate.EndpointKeyID,
		ConnectorType: candidate.ConnectorType, CanonicalBaseURL: candidate.CanonicalBaseURL,
		UpstreamModelID: candidate.UpstreamModelID, Policy: candidate.Policy,
	}
}

type RuntimeSnapshot struct {
	ModelID          int64
	Provider         string
	Model            string
	FullName         string
	FlattenToolCalls bool
	ReservedMilli    int64
	candidates       []RuntimeCandidate
}

// RuntimePreflight carries only candidate-free logical, policy, and caller
// eligibility facts. It intentionally has no endpoint, key, donation, health,
// quota, or binding field.
type RuntimePreflight struct {
	ModelID          int64
	Provider         string
	Model            string
	FullName         string
	FlattenToolCalls bool
	ReservedMilli    int64
}

type AvailableModel struct {
	ModelID   int64
	Provider  string
	FullName  string
	CreatedAt int64
}

func (snapshot RuntimeSnapshot) Candidates() []RuntimeCandidate {
	return append([]RuntimeCandidate(nil), snapshot.candidates...)
}

func (RuntimeSnapshot) String() string   { return "[redacted charity routing snapshot]" }
func (RuntimeSnapshot) GoString() string { return "[redacted charity routing snapshot]" }
func (RuntimeSnapshot) LogValue() slog.Value {
	return slog.StringValue("[redacted charity routing snapshot]")
}

type roleKind string

const (
	roleAdmin   roleKind = "admin"
	roleSteward roleKind = "level5"
)

type roleContext struct {
	kind    roleKind
	actorID int64
}

type finalRoleTx interface {
	beginRoleTx(context.Context, roleKind, int64) (*sql.Tx, int64, error)
}

var _ http.Handler = http.HandlerFunc(nil)
