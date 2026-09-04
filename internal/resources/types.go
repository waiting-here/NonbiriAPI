package resources

import (
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

type MutationResult[T any] struct {
	Value    T
	Status   int
	Body     []byte
	Replayed bool
}

type Endpoint struct {
	ID            string         `json:"id"`
	ConnectorType string         `json:"connector_type"`
	BaseURL       string         `json:"base_url"`
	Origin        EndpointOrigin `json:"origin"`
	Note          string         `json:"note"`
	Enabled       bool           `json:"enabled"`
	Revision      string         `json:"revision"`
	KeyCount      string         `json:"key_count"`
	CreatedAt     int64          `json:"created_at"`
	UpdatedAt     int64          `json:"updated_at"`
}

// EndpointOrigin is deliberately a small, closed public projection. Internal
// channel category and revision remain administrator-only provenance.
type EndpointOrigin struct {
	Kind      string `json:"kind"`
	ChannelID string `json:"channel_id,omitempty"`
	Name      string `json:"name,omitempty"`
}

type MainstreamChannel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	ConnectorType string `json:"connector_type"`
	BaseURL       string `json:"base_url"`
	Enabled       bool   `json:"enabled"`
	State         string `json:"state"`
	Revision      string `json:"revision"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
	RetiredAt     *int64 `json:"retired_at"`
}

type MainstreamChannelOption struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ConnectorType string `json:"connector_type"`
	BaseURL       string `json:"base_url"`
}

type EndpointCreateOptions struct {
	BaseConnectorTypes []string                  `json:"base_connector_types"`
	MainstreamChannels []MainstreamChannelOption `json:"mainstream_channels"`
}

type EndpointKey struct {
	ID              string `json:"id"`
	EndpointID      string `json:"endpoint_id"`
	DisplayHead     string `json:"display_head"`
	DisplayTail     string `json:"display_tail"`
	Note            string `json:"note"`
	Enabled         bool   `json:"enabled"`
	ForceStoreFalse bool   `json:"force_store_false"`
	SuspensionState string `json:"suspension_state"`
	Revision        string `json:"revision"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

type CallerKeyMetadata struct {
	Display    string `json:"display"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	Generation string `json:"generation"`
}

type CallerKeyState struct {
	Metadata   *CallerKeyMetadata
	Generation string
}

type CallerKeySecret struct {
	Secret   string            `json:"secret"`
	Metadata CallerKeyMetadata `json:"metadata"`
}

type DiscoveryEvidence struct {
	State      string  `json:"state"`
	Revision   string  `json:"revision"`
	Result     *string `json:"result"`
	SafeClass  string  `json:"safe_class"`
	ObservedAt *int64  `json:"observed_at"`
	Count      *string `json:"count"`
}

type CatalogEntry struct {
	ID              string `json:"id"`
	SourceType      string `json:"source_type"`
	UpstreamModelID string `json:"upstream_model_id"`
	Provider        string `json:"provider"`
	SourceRevision  string `json:"source_revision"`
	PairRevision    string `json:"pair_revision"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

type CatalogView struct {
	Evidence         DiscoveryEvidence `json:"evidence"`
	AutomaticEntries []CatalogEntry    `json:"automatic_entries"`
	ManualEntries    []CatalogEntry    `json:"manual_entries"`
	NextCursor       *string           `json:"next_cursor"`
}

type BindingCandidate struct {
	EndpointKeyID          string   `json:"endpoint_key_id"`
	EndpointBaseURL        string   `json:"endpoint_base_url"`
	ConnectorType          string   `json:"connector_type"`
	EndpointNote           string   `json:"endpoint_note"`
	EndpointKeyDisplayHead string   `json:"endpoint_key_display_head"`
	EndpointKeyDisplayTail string   `json:"endpoint_key_display_tail"`
	EndpointKeyNote        string   `json:"endpoint_key_note"`
	UpstreamModelID        string   `json:"upstream_model_id"`
	SourceTypes            []string `json:"source_types"`
}

type Binding struct {
	ID                     string `json:"id"`
	EndpointKeyID          string `json:"endpoint_key_id"`
	EndpointBaseURL        string `json:"endpoint_base_url"`
	ConnectorType          string `json:"connector_type"`
	EndpointNote           string `json:"endpoint_note"`
	EndpointKeyDisplayHead string `json:"endpoint_key_display_head"`
	EndpointKeyDisplayTail string `json:"endpoint_key_display_tail"`
	EndpointKeyNote        string `json:"endpoint_key_note"`
	UpstreamModelID        string `json:"upstream_model_id"`
	Ord                    int    `json:"ord"`
}

type Model struct {
	ID               string `json:"id"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	FullName         string `json:"full_name"`
	RouteStrategy    string `json:"route_strategy"`
	SilentRetry      bool   `json:"silent_retry"`
	FlattenToolCalls bool   `json:"flatten_tool_calls"`
	Revision         string `json:"revision"`
	BindingRevision  string `json:"binding_revision"`
	BindingCount     string `json:"binding_count"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

type ManualEntriesResponse struct {
	Entries []CatalogEntry `json:"entries"`
}

type AffectedModel struct {
	Model    Model     `json:"model"`
	Bindings []Binding `json:"bindings"`
}

type ManualUpdateResponse struct {
	Entries        []CatalogEntry  `json:"entries"`
	AffectedModels []AffectedModel `json:"affected_models"`
}

type BindingsResponse struct {
	Bindings        []Binding `json:"bindings"`
	BindingRevision string    `json:"binding_revision"`
}

type DiscoveryAccepted struct {
	OperationID string            `json:"operation_id"`
	Evidence    DiscoveryEvidence `json:"evidence"`
}

type CreateEndpointInput struct {
	Source        string
	ChannelID     string
	ConnectorType string
	BaseURL       string
	Note          string
	Enabled       bool
}

type CreateMainstreamChannelInput struct {
	Name          string
	Category      string
	ConnectorType string
	BaseURL       string
	Enabled       bool
}

type PatchMainstreamChannelInput struct {
	Name             *string
	Category         *string
	ConnectorType    *string
	BaseURL          *string
	Enabled          *bool
	ExpectedRevision int64
}

type PatchEndpointInput struct {
	Note             *string
	Enabled          *bool
	ExpectedRevision int64
}

type CreateEndpointKeyInput struct {
	Secret             []byte
	Note               string
	Enabled            bool
	ForceStoreFalse    bool
	OwnershipConfirmed bool
}

type PatchEndpointKeyInput struct {
	Note             *string
	Enabled          *bool
	ForceStoreFalse  *bool
	ExpectedRevision int64
}

type ManualCatalogInput struct {
	UpstreamModelID string `json:"upstream_model_id"`
	Provider        string `json:"provider"`
}

type BindingReplacement struct {
	BindingID                  int64
	ReplacementUpstreamModelID string
}

type UpdateManualInput struct {
	UpstreamModelID      string
	Provider             string
	ExpectedPairRevision int64
	Replacements         []BindingReplacement
}

type DeleteManualInput struct {
	ExpectedPairRevision int64
	Replacements         []BindingReplacement
}

type CreateModelInput struct {
	Provider         string
	Model            string
	RouteStrategy    string
	SilentRetry      bool
	FlattenToolCalls bool
}

type PatchModelInput struct {
	Provider         *string
	Model            *string
	RouteStrategy    *string
	SilentRetry      *bool
	FlattenToolCalls *bool
	ExpectedRevision int64
}

type BindingSelection struct {
	EndpointKeyID   int64
	UpstreamModelID string
}

type CandidateQuery struct {
	EndpointID int64
	KeyID      int64
	Source     string
	Query      string
	Limit      int
	Cursor     string
}

type ControlMutation struct {
	IdempotencyKey string
	Method         string
	Route          string
	PathIDs        []string
	Query          string
	CanonicalBody  []byte
}

type DiscoveryFailureClass string

const (
	DiscoveryFailureAuth        DiscoveryFailureClass = "auth"
	DiscoveryFailureRateLimit   DiscoveryFailureClass = "rate_limit"
	DiscoveryFailureTimeout     DiscoveryFailureClass = "timeout"
	DiscoveryFailureProtocol    DiscoveryFailureClass = "protocol"
	DiscoveryFailureTransport   DiscoveryFailureClass = "transport"
	DiscoveryFailureInterrupted DiscoveryFailureClass = "interrupted"
)

type DiscoveredModel struct {
	UpstreamModelID string
	Provider        string
}

type DiscoveryClaimInput struct {
	OperationID      string
	OwnerUserID      int64
	EndpointID       int64
	EndpointKeyID    int64
	ConnectorType    connectorcontract.Type
	CanonicalBaseURL string
	Discoverer       connector.ModelDiscoverer
}

type DiscoveryClaimResult struct {
	Succeeded      bool
	Models         []DiscoveredModel
	FailureClass   DiscoveryFailureClass
	SafeDiagnostic string
}
