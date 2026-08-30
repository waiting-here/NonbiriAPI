package donation

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

var (
	ErrInvalidRequest  = errors.New("donation: invalid request")
	ErrUnauthorized    = errors.New("donation: unauthorized")
	ErrForbidden       = errors.New("donation: forbidden")
	ErrFeatureDisabled = errors.New("donation: feature disabled")
	ErrNotFound        = errors.New("donation: not found")
	ErrConflict        = errors.New("donation: conflict")
	ErrResourceLocked  = errors.New("donation: resource locked")
	ErrResourceLimit   = errors.New("donation: resource limit exceeded")
	ErrUnavailable     = errors.New("donation: unavailable")
	ErrInvariant       = errors.New("donation: invariant violation")
)

type OwnerFinalTxAuthorizer interface {
	AuthorizeUserMutation(context.Context, *sql.Tx, int64) error
}

// RoleFinalTxAuthorizer revalidates the exact live role in the final business
// transaction. Admin callers use the environment singleton user ID; steward
// callers use the authenticated user principal supplied by the user registrar.
type RoleFinalTxAuthorizer interface {
	AuthorizeAdminMutation(context.Context, *sql.Tx, int64) error
	AuthorizeStewardMutation(context.Context, *sql.Tx, int64) error
}

type AdminRouteRegistrar interface {
	RegisterAdminRoute(method, pattern string, handler http.Handler) error
}

type UserRouteRegistrar = resources.UserRouteRegistrar
type UserPrincipal = resources.UserPrincipal
type AuthorizedUserHandler = resources.AuthorizedUserHandler

type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

type SafeSource struct {
	BaseURL       string `json:"base_url"`
	ConnectorType string `json:"connector_type"`
}

type DonationLimits struct {
	Price  *string `json:"price"`
	Calls  *string `json:"calls"`
	Tokens *string `json:"tokens"`
}

type DonationUsage struct {
	PriceUsed      string `json:"price_used"`
	PriceInflight  string `json:"price_inflight"`
	CallsUsed      string `json:"calls_used"`
	CallsInflight  string `json:"calls_inflight"`
	TokensUsed     string `json:"tokens_used"`
	TokensInflight string `json:"tokens_inflight"`
}

type DonationStreak struct {
	Generation      string `json:"generation"`
	Count           string `json:"count"`
	FailureDisabled bool   `json:"failure_disabled"`
}

type DonationKey struct {
	ID              string         `json:"id"`
	EndpointKeyID   *string        `json:"endpoint_key_id"`
	DisplayHead     string         `json:"display_head"`
	DisplayTail     string         `json:"display_tail"`
	SafeSource      SafeSource     `json:"safe_source"`
	PhysicalEnabled bool           `json:"physical_enabled"`
	CharityState    string         `json:"charity_state"`
	Limits          DonationLimits `json:"limits"`
	Usage           DonationUsage  `json:"usage"`
	TokenReserve    int64          `json:"token_reserve"`
	Streak          DonationStreak `json:"streak"`
	// SafeNote is retained only as an internal projection field. Owner and
	// export DTOs must never expose the reviewer-only note.
	SafeNote    string  `json:"-"`
	EndedReason *string `json:"ended_reason"`
}

type AdminDonationKey struct {
	DonationKey
	SafeNote string `json:"safe_note"`
}

type StewardDonationKey struct {
	DonationKey
	SafeNote string `json:"safe_note"`
}

type ReviewResult struct {
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	ReviewedAt int64  `json:"reviewed_at"`
}

type Donation struct {
	ID           string        `json:"id"`
	Status       string        `json:"status"`
	Revision     string        `json:"revision"`
	Description  string        `json:"description"`
	ReviewResult *ReviewResult `json:"review_result"`
	ExpiresAt    *int64        `json:"expires_at"`
	Keys         []DonationKey `json:"keys"`
	CreatedAt    int64         `json:"created_at"`
	UpdatedAt    int64         `json:"updated_at"`
}

type DonationOwner struct {
	UserID      string  `json:"user_id"`
	DiscordID   *string `json:"discord_id"`
	DisplayName string  `json:"display_name"`
}

type StewardDonationOwner struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

type DonationReviewer struct {
	UserID *string `json:"user_id"`
	Role   string  `json:"role"`
}

// AdminDonation and StewardDonation intentionally do not alias one another.
// This keeps later role-specific expansion from accidentally widening L5.
type AdminDonation struct {
	ID           string             `json:"id"`
	Status       string             `json:"status"`
	Revision     string             `json:"revision"`
	Description  string             `json:"description"`
	ReviewResult *ReviewResult      `json:"review_result"`
	ExpiresAt    *int64             `json:"expires_at"`
	Keys         []AdminDonationKey `json:"keys"`
	Owner        *DonationOwner     `json:"owner"`
	Reviewer     *DonationReviewer  `json:"reviewer"`
	CreatedAt    int64              `json:"created_at"`
	UpdatedAt    int64              `json:"updated_at"`
}

type StewardDonation struct {
	ID           string               `json:"id"`
	Status       string               `json:"status"`
	Revision     string               `json:"revision"`
	Description  string               `json:"description"`
	ReviewResult *ReviewResult        `json:"review_result"`
	ExpiresAt    *int64               `json:"expires_at"`
	Keys         []StewardDonationKey `json:"keys"`
	Owner        StewardDonationOwner `json:"owner"`
	Reviewer     *DonationReviewer    `json:"reviewer"`
	CreatedAt    int64                `json:"created_at"`
	UpdatedAt    int64                `json:"updated_at"`
}

type CreateInput struct {
	Description         string
	EndpointKeyIDs      []int64
	OwnershipAuthorized bool
}

type EditInput struct {
	Description      string
	ExpectedRevision int64
}

type RevisionInput struct {
	ExpectedRevision int64
}

type TerminateInput struct {
	ExpectedRevision int64
	Confirmation     string
}

type KeySetting struct {
	DonationKeyID int64
	PriceLimit    *string
	CallsLimit    *string
	TokensLimit   *string
	TokenReserve  int64
	Enabled       bool
	SafeNote      string
}

type ReviewInput struct {
	Decision         string
	ExpectedRevision int64
	Reason           string
	ExpiresAt        *int64
	KeySettings      []KeySetting
}

type KeyManagementInput struct {
	ExpectedRevision   int64
	Enabled            *bool
	PriceLimit         **string
	CallsLimit         **string
	TokensLimit        **string
	TokenReserve       *int64
	SafeNote           *string
	ResetFailureStreak bool
}

type ExportDonation struct {
	ID           string        `json:"id"`
	Status       string        `json:"status"`
	Description  string        `json:"description"`
	ReviewResult *ReviewResult `json:"review_result"`
	ExpiresAt    *int64        `json:"expires_at"`
	Keys         []DonationKey `json:"keys"`
	CreatedAt    int64         `json:"created_at"`
	UpdatedAt    int64         `json:"updated_at"`
}
