package issues

import (
	"context"
	"database/sql"
)

type Source string

const (
	SourceModelDiscovery    Source = "model_discovery"
	SourceRoutingProjection Source = "routing_projection"
	SourceResourceValidator Source = "resource_validator"
)

type ResourceKind string

const (
	ResourceEndpoint    ResourceKind = "endpoint"
	ResourceEndpointKey ResourceKind = "endpoint_key"
	ResourceModel       ResourceKind = "model"
)

type RootCause string

const (
	RootDiscoveryFailed      RootCause = "discovery_failed"
	RootNoRoutableBinding    RootCause = "no_routable_binding"
	RootCredentialInvalid    RootCause = "credential_invalid"
	RootConfigurationInvalid RootCause = "configuration_invalid"
)

type DeepLink struct {
	RouteID    string  `json:"route_id"`
	ResourceID *string `json:"resource_id,omitempty"`
}

type Issue struct {
	ID           string       `json:"id"`
	State        string       `json:"state"`
	Source       Source       `json:"source"`
	ResourceKind ResourceKind `json:"resource_kind"`
	SummaryCode  string       `json:"summary_code"`
	SafeDetail   string       `json:"safe_detail"`
	DeepLink     *DeepLink    `json:"deep_link"`
	FirstSeenAt  int64        `json:"first_seen_at"`
	LastSeenAt   int64        `json:"last_seen_at"`
	Count        string       `json:"count"`
	ClosedAt     *int64       `json:"closed_at"`
}

type Page struct {
	Data                 []Issue `json:"data"`
	NextCursor           *string `json:"next_cursor"`
	ProjectionIncomplete bool    `json:"projection_incomplete"`
}

type ListQuery struct {
	State  string
	Cursor string
	Limit  int
}

// ResourceValidationState is returned by the resource-validator authority.
// The issue package still revalidates resource ownership and the frozen
// source/resource/root matrix before writing any projection.
type ResourceValidationState struct {
	Active     bool
	ObservedAt int64
	SafeDetail string
}

type ResourceValidationTarget struct {
	ResourceKind ResourceKind
	ResourceID   int64
	RootCause    RootCause
}

type ResourceValidationBatch struct {
	Items      []ResourceValidationTarget
	NextCursor string
	Done       bool
}

// ResourceValidationAuthority is the typed recovery seam for the one source
// whose authority is owned outside the C1 resource tables. It cannot provide
// source, account, summary, deep-link or SQL text.
type ResourceValidationAuthority interface {
	Current(context.Context, *sql.Tx, int64, ResourceKind, int64, RootCause) (ResourceValidationState, error)
	Scan(context.Context, *sql.Tx, int64, string, int) (ResourceValidationBatch, error)
}

type RebuildResult struct {
	UserID     int64
	Generation int64
	Processed  int
	Complete   bool
}

type RetentionResult struct {
	ClosedDeleted int
}
