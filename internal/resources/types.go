package resources

import (
	"context"
	"database/sql"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
	"github.com/waiting-here/NonbiriAPI/internal/sessionheader"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

type Page[T any] struct {
	Data       []T                  `json:"data"`
	NextCursor *string              `json:"next_cursor"`
	Pagination *pagination.Metadata `json:"pagination,omitempty"`
}

type MutationResult[T any] struct {
	Value    T
	Status   int
	Body     []byte
	Replayed bool
}

type Endpoint struct {
	ConnectionID      *string                           `json:"connection_id"`
	ConfigurationType string                            `json:"configuration_type"`
	NativeServices    []connectorcontract.NativeService `json:"native_services"`
	RerankFormat      connectorcontract.RerankFormat    `json:"rerank_format"`
	ID                string                            `json:"id"`
	ConnectorType     string                            `json:"connector_type"`
	BaseURL           string                            `json:"base_url"`
	Origin            EndpointOrigin                    `json:"origin"`
	Note              string                            `json:"note"`
	Enabled           bool                              `json:"enabled"`
	Revision          string                            `json:"revision"`
	KeyCount          string                            `json:"key_count"`
	Browse            *EndpointBrowse                   `json:"browse,omitempty"`
	CreatedAt         int64                             `json:"created_at"`
	UpdatedAt         int64                             `json:"updated_at"`
}

// EndpointOrigin is deliberately a small, closed public projection. Internal
// channel category and revision remain administrator-only provenance.
type EndpointOrigin struct {
	Kind      string `json:"kind"`
	ChannelID string `json:"channel_id,omitempty"`
	Name      string `json:"name,omitempty"`
}

type MainstreamChannel struct {
	MainstreamPreset
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
	MainstreamPreset
	Revision      string `json:"revision"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	ConnectorType string `json:"connector_type"`
	BaseURL       string `json:"base_url"`
}

type EndpointCreateOptions struct {
	NativeServiceOptions []NativeServiceOptions    `json:"native_service_options"`
	BaseConnectorTypes   []string                  `json:"base_connector_types"`
	MainstreamChannels   []MainstreamChannelOption `json:"mainstream_channels"`
}

type EndpointKey struct {
	CredentialAvailable bool                              `json:"credential_available"`
	CredentialID        string                            `json:"credential_id"`
	CredentialVersion   string                            `json:"credential_version"`
	GrantRevision       string                            `json:"grant_revision"`
	NativeServices      []connectorcontract.NativeService `json:"native_services"`
	DiscoverySourceID   *string                           `json:"discovery_source_id"`
	ID                  string                            `json:"id"`
	EndpointID          string                            `json:"endpoint_id"`
	DisplayHead         string                            `json:"display_head"`
	DisplayTail         string                            `json:"display_tail"`
	Note                string                            `json:"note"`
	Enabled             bool                              `json:"enabled"`
	ForceStoreFalse     bool                              `json:"force_store_false"`
	MaxConcurrency      int64                             `json:"max_concurrency"`
	MaxRPM              int64                             `json:"max_rpm"`
	SuspensionState     string                            `json:"suspension_state"`
	Browse              *EndpointKeyBrowse                `json:"browse,omitempty"`
	Revision            string                            `json:"revision"`
	CreatedAt           int64                             `json:"created_at"`
	UpdatedAt           int64                             `json:"updated_at"`
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
	Evidence         DiscoveryEvidence    `json:"evidence"`
	AutomaticEntries []CatalogEntry       `json:"automatic_entries"`
	ManualEntries    []CatalogEntry       `json:"manual_entries"`
	NextCursor       *string              `json:"next_cursor"`
	Pagination       *pagination.Metadata `json:"pagination,omitempty"`
}

type BindingCandidate struct {
	NativeServices         []connectorcontract.NativeService `json:"native_services"`
	ModelTypes             []connectorcontract.Operation     `json:"model_types"`
	ServiceRoutes          []BindingServiceRoute             `json:"service_routes"`
	Capabilities           BindingCapabilities               `json:"capabilities"`
	EndpointKeyID          string                            `json:"endpoint_key_id"`
	EndpointBaseURL        string                            `json:"endpoint_base_url"`
	ConnectorType          string                            `json:"connector_type"`
	EndpointNote           string                            `json:"endpoint_note"`
	EndpointKeyDisplayHead string                            `json:"endpoint_key_display_head"`
	EndpointKeyDisplayTail string                            `json:"endpoint_key_display_tail"`
	EndpointKeyNote        string                            `json:"endpoint_key_note"`
	UpstreamModelID        string                            `json:"upstream_model_id"`
	SourceTypes            []string                          `json:"source_types"`
}

type Binding struct {
	Targets                []BindingTarget `json:"targets"`
	ID                     string          `json:"id"`
	EndpointKeyID          string          `json:"endpoint_key_id"`
	EndpointBaseURL        string          `json:"endpoint_base_url"`
	ConnectorType          string          `json:"connector_type"`
	EndpointNote           string          `json:"endpoint_note"`
	EndpointKeyDisplayHead string          `json:"endpoint_key_display_head"`
	EndpointKeyDisplayTail string          `json:"endpoint_key_display_tail"`
	EndpointKeyNote        string          `json:"endpoint_key_note"`
	UpstreamModelID        string          `json:"upstream_model_id"`
	Ord                    int             `json:"ord"`
}

type Model struct {
	SessionHeaders         []sessionheader.Config `json:"session_headers"`
	CloudflareProxyEnabled bool                   `json:"cloudflare_proxy_enabled"`
	CloudflareProxyMode    string                 `json:"cloudflare_proxy_mode"`
	ModelTypes             modeltype.Set          `json:"model_types"`
	TransportRule          transportpolicy.Rule   `json:"transport_rule"`
	RolePolicy             rolepolicy.Policy      `json:"role_policy"`
	ID                     string                 `json:"id"`
	Provider               string                 `json:"provider"`
	Model                  string                 `json:"model"`
	FullName               string                 `json:"full_name"`
	RouteStrategy          string                 `json:"route_strategy"`
	SilentRetry            bool                   `json:"silent_retry"`
	FlattenToolCalls       bool                   `json:"flatten_tool_calls"`
	Revision               string                 `json:"revision"`
	BindingRevision        string                 `json:"binding_revision"`
	BindingCount           string                 `json:"binding_count"`
	Browse                 *ModelBrowse           `json:"browse,omitempty"`
	CreatedAt              int64                  `json:"created_at"`
	UpdatedAt              int64                  `json:"updated_at"`
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
	ConnectionID    int64
	ChannelEntryID  int64
	ChannelRevision int64
	NativeServices  *[]connectorcontract.NativeService
	RerankFormat    connectorcontract.RerankFormat
	Source          string
	ChannelID       string
	ConnectorType   string
	BaseURL         string
	Note            string
	Enabled         bool
}

type CreateMainstreamChannelInput struct {
	Entries         *[]MainstreamEntry
	DiscoverySource *MainstreamDiscoveryPreset
	ModelMappings   []MainstreamModelMapping
	Name            string
	Category        string
	ConnectorType   string
	BaseURL         string
	Enabled         bool
}

type PatchMainstreamChannelInput struct {
	Entries          *[]MainstreamEntry
	DiscoverySource  **MainstreamDiscoveryPreset
	ModelMappings    *[]MainstreamModelMapping
	Name             *string
	Category         *string
	ConnectorType    *string
	BaseURL          *string
	Enabled          *bool
	ExpectedRevision int64
}

type PatchEndpointInput struct {
	ConnectionID     *int64
	NativeServices   *[]connectorcontract.NativeService
	RerankFormat     *connectorcontract.RerankFormat
	Note             *string
	Enabled          *bool
	ExpectedRevision int64
}

type CreateEndpointKeyInput struct {
	CredentialID       int64
	NativeServices     *[]connectorcontract.NativeService
	DiscoverySourceID  **int64
	Secret             []byte
	Note               string
	Enabled            bool
	ForceStoreFalse    bool
	OwnershipConfirmed bool
	MaxConcurrency     int64
	MaxRPM             int64
}

type PatchEndpointKeyInput struct {
	CredentialID      *int64
	NativeServices    *[]connectorcontract.NativeService
	DiscoverySourceID **int64
	Note              *string
	Enabled           *bool
	ForceStoreFalse   *bool
	ExpectedRevision  int64
	MaxConcurrency    *int64
	MaxRPM            *int64
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
	SessionHeaders         []sessionheader.Config
	CloudflareProxyEnabled *bool
	ModelTypes             modeltype.Set
	TransportRule          transportpolicy.Rule
	RolePolicy             *rolepolicy.Policy
	Provider               string
	Model                  string
	RouteStrategy          string
	SilentRetry            bool
	FlattenToolCalls       bool
}

type PatchModelInput struct {
	SessionHeaders         *[]sessionheader.Config
	CloudflareProxyEnabled *bool
	ModelTypes             *modeltype.Set
	TransportRule          *transportpolicy.Rule
	RolePolicy             *rolepolicy.Policy
	Provider               *string
	Model                  *string
	RouteStrategy          *string
	SilentRetry            *bool
	FlattenToolCalls       *bool
	ExpectedRevision       int64
}

type BindingSelection struct {
	Targets         []BindingTargetSelection
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
	DiscoveryFailureRegion      DiscoveryFailureClass = "region"
	DiscoveryFailureUnavailable DiscoveryFailureClass = "unavailable"
	DiscoveryFailureAuth        DiscoveryFailureClass = "auth"
	DiscoveryFailureRateLimit   DiscoveryFailureClass = "rate_limit"
	DiscoveryFailureTimeout     DiscoveryFailureClass = "timeout"
	DiscoveryFailureProtocol    DiscoveryFailureClass = "protocol"
	DiscoveryFailureTransport   DiscoveryFailureClass = "transport"
	DiscoveryFailureInterrupted DiscoveryFailureClass = "interrupted"
)

type DiscoveredModel struct {
	NativeServices  connectorcontract.NativeServiceSet
	UpstreamModelID string
	Provider        string
}

type DiscoveryClaimInput struct {
	Source                connectionstore.SourceAuthorizationRef
	SourceRevisionID      int64
	CredentialVersionID   int64
	UseApprovedRevisionID int64
	OperationID           string
	OwnerUserID           int64
	EndpointID            int64
	EndpointKeyID         int64
	ConnectorType         connectorcontract.Type
	CanonicalBaseURL      string
	Discoverer            connector.ModelDiscoverer
	// Authorize rechecks delegated discovery in the claim and dispatch transactions.
	// It is never populated from an HTTP field or persisted as caller input.
	Authorize func(context.Context, *sql.Tx) error
}

type DiscoveryClaimResult struct {
	Succeeded      bool
	Models         []DiscoveredModel
	FailureClass   DiscoveryFailureClass
	SafeDiagnostic string
}
