package donation

import "github.com/waiting-here/NonbiriAPI/internal/donationquota"

// DonationSummary contains no key records or management-only fields. Source
// previews are bounded to three distinct sources; source_count is authoritative.
type DonationSummary struct {
	ID           string            `json:"id"`
	Status       string            `json:"status"`
	Revision     string            `json:"revision"`
	Description  string            `json:"description"`
	ReviewResult *ReviewResult     `json:"review_result"`
	CreatedAt    int64             `json:"created_at"`
	UpdatedAt    int64             `json:"updated_at"`
	KeyCount     string            `json:"key_count"`
	StateCounts  map[string]string `json:"state_counts"`
	SourceCount  string            `json:"source_count"`
	Sources      []SafeSource      `json:"sources"`
}

type AdminDonationSummary struct {
	DonationSummary
	Owner    *DonationOwner    `json:"owner"`
	Reviewer *DonationReviewer `json:"reviewer"`
	Handling DonationHandling  `json:"handling"`
}

type StewardDonationSummary struct {
	DonationSummary
	Owner    *StewardDonationOwner `json:"owner"`
	Reviewer *DonationReviewer     `json:"reviewer"`
	Handling DonationHandling      `json:"handling"`
}

type RecurringSummary struct {
	RuleCount string                   `json:"rule_count"`
	Rules     []donationquota.RuleView `json:"rules"`
}

type OwnerKeySummary struct {
	DonationKey
	RecurringReceipt
	RecurringSummary
}

// The shared source view never embeds AdminDonationKey or donor identities.
// Even an administrator obtains those private details only via original detail.
type ManagedKeySummary struct {
	DonationKey
	RecurringReceipt
	RecurringSummary
	AuthorizedExpiresAt *int64           `json:"authorized_expires_at"`
	SafeNote            string           `json:"safe_note"`
	MaxConcurrency      *int64           `json:"max_concurrency"`
	MaxRPM              *int64           `json:"max_rpm"`
	BindingCount        string           `json:"binding_count"`
	Idle                bool             `json:"idle"`
	Handling            DonationHandling `json:"handling"`
}

type DonationSource struct {
	SourceKey            string     `json:"source_key"`
	SafeSource           SafeSource `json:"safe_source"`
	DonationCount        string     `json:"donation_count"`
	KeyCount             string     `json:"key_count"`
	UsableKeyCount       string     `json:"usable_key_count"`
	PendingDonationCount string     `json:"pending_donation_count"`
}

type SourceFilter struct{ Query, Scope, Handling, Idle string }
