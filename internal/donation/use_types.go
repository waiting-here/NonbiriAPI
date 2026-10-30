package donation

import (
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type UseRevision struct {
	CredentialID            string                       `json:"credential_id"`
	CredentialVersion       string                       `json:"credential_version"`
	GrantRevision           string                       `json:"grant_revision"`
	Status                  string                       `json:"status"`
	ReviewNote              string                       `json:"review_note"`
	ReviewedAt              *int64                       `json:"reviewed_at"`
	ExpectedReviewRevision  *string                      `json:"expected_review_revision"`
	DiscoverySourceID       *string                      `json:"discovery_source_id"`
	DiscoverySourceRevision *string                      `json:"discovery_source_revision"`
	DisplayHead             string                       `json:"display_head"`
	DisplayTail             string                       `json:"display_tail"`
	Review                  ReviewState                  `json:"review"`
	Discovery               *resources.DiscoveryEvidence `json:"discovery"`
	available               bool
}

type UseHealth struct {
	FailureStreak   string `json:"failure_streak"`
	FailureDisabled bool   `json:"failure_disabled"`
	RetryAfterUntil *int64 `json:"retry_after_until"`
	BackoffRetryAt  *int64 `json:"backoff_retry_at"`
}

type UseCapabilities struct {
	CanReview         bool    `json:"can_review"`
	CanManage         bool    `json:"can_manage"`
	CanResetFailures  bool    `json:"can_reset_failures"`
	CanDiscover       bool    `json:"can_discover"`
	CanBind           bool    `json:"can_bind"`
	UnavailableReason *string `json:"unavailable_reason"`
}

type Use struct {
	FailureReset     *ServiceFailureReset   `json:"failure_reset,omitempty"`
	ID               string                 `json:"id"`
	DonationID       string                 `json:"donation_id"`
	DonationKeyID    string                 `json:"donation_key_id"`
	EndpointKeyID    *string                `json:"endpoint_key_id"`
	NativeService    contract.NativeService `json:"native_service"`
	Enabled          bool                   `json:"enabled"`
	DiscoveryEnabled bool                   `json:"discovery_enabled"`
	Revision         string                 `json:"revision"`
	Approved         *UseRevision           `json:"approved"`
	Pending          *UseRevision           `json:"pending"`
	Latest           *UseRevision           `json:"latest"`
	Health           *UseHealth             `json:"health"`
	Capabilities     UseCapabilities        `json:"capabilities"`
}

type UseFilter struct {
	State         string
	CredentialID  int64
	DonationKeyID int64
}

type UsePatch struct {
	ExpectedRevision          int64
	Enabled, DiscoveryEnabled *bool
}

type UseReviewItem struct {
	UseID, ExpectedRevision int64
	ExpectedReviewRevision  *int64
	Decision, ReviewNote    string
}

type UseReviewError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type UseReviewResult struct {
	UseID string          `json:"use_id"`
	OK    bool            `json:"ok"`
	Use   *Use            `json:"use,omitempty"`
	Error *UseReviewError `json:"error,omitempty"`
}
type UseReviewReceipt struct {
	Results []UseReviewResult `json:"results"`
}
