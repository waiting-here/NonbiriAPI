package lifecycle

import (
	"context"
	"database/sql"
)

// DeleteSource describes the initiating authority, never an inferred reason
// string. Authorization and every domain handoff share the deletion transaction.
type DeleteSource string

const (
	DeleteSelf   DeleteSource = "self"
	DeleteAdmin  DeleteSource = "admin"
	DeleteSystem DeleteSource = "system"
)

func (source DeleteSource) Valid() bool {
	return source == DeleteSelf || source == DeleteAdmin || source == DeleteSystem
}

// InteractionExport is the closed next-version projection. Domain adapters
// return these safe types, never their storage JSON or secret-bearing DTOs.
type InteractionExport struct {
	RequestAdaptations []RequestAdaptationExport     `json:"request_adaptations"`
	Continuity         []ContinuityEligibilityExport `json:"continuity"`
	FatFish            FatFishExport                 `json:"fat_fish"`
}

type RequestAdaptationExport struct {
	EndpointID           string                  `json:"endpoint_id"`
	Revision             string                  `json:"revision"`
	ForwardHeaders       []string                `json:"forward_headers"`
	FixedHeaders         []AdaptationValueExport `json:"fixed_headers"`
	BodyDefaults         []AdaptationValueExport `json:"body_defaults"`
	BodyForced           []AdaptationValueExport `json:"body_forced"`
	NativeExtensionPaths []string                `json:"native_extension_paths"`
}

type AdaptationValueExport struct {
	Path     string `json:"path"`
	HasValue bool   `json:"has_value"`
}

// Eligibility excludes internal identity keys, old account IDs and abuse
// evidence. Only the current account's reward/check-in eligibility is exposed.
type ContinuityEligibilityExport struct {
	Kind      string `json:"kind"`
	Scope     string `json:"scope"`
	Window    string `json:"window"`
	State     string `json:"state"`
	ExpiresAt *int64 `json:"expires_at"`
}

type FatFishExport struct {
	Summaries []FatFishSummaryExport  `json:"summaries"`
	Progress  []FatFishProgressExport `json:"progress"`
}

type FatFishSummaryExport struct {
	ID                 string `json:"id"`
	PeriodID           string `json:"period_id"`
	NodeID             string `json:"node_id"`
	VersionID          string `json:"version_id"`
	EngineVersion      int    `json:"engine_version"`
	ScoringVersion     int    `json:"scoring_version"`
	State              string `json:"state"`
	PreparedAt         int64  `json:"prepared_at_ms"`
	StartedAt          *int64 `json:"started_at_ms"`
	CompletedAt        int64  `json:"completed_at_ms"`
	Passed             bool   `json:"passed"`
	Stars              int    `json:"stars"`
	ScoreUnits         string `json:"score_units"`
	TicketCharge       string `json:"ticket_charge"`
	TicketRefund       string `json:"ticket_refund"`
	Rewards            string `json:"rewards"`
	SeedCommit         string `json:"seed_commit"`
	CommitmentVerified bool   `json:"commitment_verified"`
}

type FatFishProgressExport struct {
	PeriodID          string  `json:"period_id"`
	NodeID            string  `json:"node_id"`
	UnlockedAt        int64   `json:"unlocked_at"`
	UnlockOperationID *string `json:"unlock_operation_id"`
	Passed            bool    `json:"passed"`
	BestStars         int     `json:"best_stars"`
	BestScoreUnits    string  `json:"best_score_units"`
	BestAt            *int64  `json:"best_at_ms"`
	BestVersionID     *string `json:"best_version_id"`
}

type RequestAdaptationExporter interface {
	ExportRequestAdaptations(context.Context, *sql.Tx, ExportRequest) ([]RequestAdaptationExport, error)
}

type ContinuityExporter interface {
	ExportContinuity(context.Context, *sql.Tx, ExportRequest) ([]ContinuityEligibilityExport, error)
}

type FatFishExporter interface {
	ExportFatFish(context.Context, *sql.Tx, ExportRequest) (FatFishExport, ExportFinalizer, error)
}

// These compile-time ports keep every new state family attached to explicit
// owners when services are constructed, without a table-name registry.
type CharityRoutingLifecycle interface {
	DeleteAdapter
	RecoveryAdapter
	RetentionAdapter
}

type RequestAdaptationLifecycle interface {
	RequestAdaptationExporter
	DeleteAdapter
}

type ContinuityLifecycle interface {
	ContinuityExporter
	DeleteAdapter
	RetentionAdapter
}

type FatFishLifecycle interface {
	FatFishExporter
	DeleteAdapter
	RecoveryAdapter
	RetentionAdapter
}
