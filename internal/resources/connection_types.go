package resources

import contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"

type ConnectionPrimaryEntry struct {
	ID      string `json:"id"`
	BaseURL string `json:"base_url"`
	Note    string `json:"note"`
}

type Connection struct {
	ID              string                  `json:"id"`
	Name            string                  `json:"name"`
	DisplayName     string                  `json:"display_name"`
	PrimaryEntry    *ConnectionPrimaryEntry `json:"primary_entry"`
	Note            string                  `json:"note"`
	Revision        string                  `json:"revision"`
	EntryCount      string                  `json:"entry_count"`
	CredentialCount string                  `json:"credential_count"`
	CreatedAt       int64                   `json:"created_at"`
	UpdatedAt       int64                   `json:"updated_at"`
}

type Credential struct {
	ID                string  `json:"id"`
	ConnectionID      *string `json:"connection_id"`
	Note              string  `json:"note"`
	Enabled           bool    `json:"enabled"`
	Revision          string  `json:"revision"`
	CredentialVersion string  `json:"credential_version"`
	DisplayHead       string  `json:"display_head"`
	DisplayTail       string  `json:"display_tail"`
	CreatedAt         int64   `json:"created_at"`
	UpdatedAt         int64   `json:"updated_at"`
}

type CredentialVersion struct {
	CredentialID      string `json:"credential_id"`
	CredentialVersion string `json:"credential_version"`
	Enabled           bool   `json:"enabled"`
	Revision          string `json:"revision"`
	DisplayHead       string `json:"display_head"`
	DisplayTail       string `json:"display_tail"`
	CreatedAt         int64  `json:"created_at"`
}

type CredentialImpactSummary struct {
	EndpointKeys     string `json:"endpoint_keys"`
	DonationKeys     string `json:"donation_keys"`
	DiscoverySources string `json:"discovery_sources"`
}

type CredentialImpactItem struct {
	EndpointID        *string                  `json:"endpoint_id"`
	EndpointKeyID     *string                  `json:"endpoint_key_id"`
	DonationID        *string                  `json:"donation_id"`
	DonationKeyID     *string                  `json:"donation_key_id"`
	UseID             *string                  `json:"use_id"`
	DiscoverySourceID *string                  `json:"discovery_source_id"`
	CredentialVersion string                   `json:"credential_version"`
	NativeServices    []contract.NativeService `json:"native_services"`
	State             string                   `json:"state"`
}

type CredentialImpact struct {
	Page[CredentialImpactItem]
	Kind    string                  `json:"kind"`
	Summary CredentialImpactSummary `json:"summary"`
}

type DiscoverySource struct {
	CredentialAvailable     bool    `json:"credential_available"`
	ModelsURL               string  `json:"models_url"`
	ID                      string  `json:"id"`
	Name                    string  `json:"name"`
	ConnectionID            *string `json:"connection_id"`
	BaseURL                 string  `json:"base_url"`
	ListFormat              string  `json:"list_format"`
	CredentialID            string  `json:"credential_id"`
	CredentialVersion       string  `json:"credential_version"`
	Revision                string  `json:"revision"`
	DiscoverySourceRevision string  `json:"discovery_source_revision"`
	Enabled                 bool    `json:"enabled"`
	State                   string  `json:"state"`
	CheckedAt               *int64  `json:"checked_at"`
	SafeClass               string  `json:"safe_class"`
	SafeDiagnostic          string  `json:"safe_diag"`
	CreatedAt               int64   `json:"created_at"`
	UpdatedAt               int64   `json:"updated_at"`
}

type DiscoverySourceModel struct {
	ID                    string                   `json:"id"`
	UpstreamModelID       string                   `json:"upstream_model_id"`
	Provider              string                   `json:"provider"`
	NativeServices        []contract.NativeService `json:"native_services"`
	ServiceEvidence       string                   `json:"service_evidence"`
	NeedsServiceSelection bool                     `json:"needs_service_selection"`
}

type DiscoverySourceAccepted struct {
	OperationID     string          `json:"operation_id"`
	DiscoverySource DiscoverySource `json:"discovery_source"`
}

type NativeServiceOptions struct {
	ConnectorType         contract.Type            `json:"connector_type"`
	ConfigurationType     contract.Type            `json:"configuration_type"`
	NativeServices        []contract.NativeService `json:"native_services"`
	DefaultNativeServices []contract.NativeService `json:"default_native_services"`
	RerankFormats         []contract.RerankFormat  `json:"rerank_formats"`
}

type BindingServiceRoute struct {
	Operation     contract.Operation     `json:"operation"`
	NativeService contract.NativeService `json:"native_service"`
	Mode          string                 `json:"mode"`
}

type BindingCapabilities struct {
	CanBind           bool    `json:"can_bind"`
	UnavailableReason *string `json:"unavailable_reason"`
}

type BindingTarget struct {
	ID                string                   `json:"id"`
	Revision          string                   `json:"revision"`
	EndpointID        string                   `json:"endpoint_id"`
	EndpointKeyID     string                   `json:"endpoint_key_id"`
	ConnectorType     string                   `json:"connector_type"`
	ConfigurationType string                   `json:"configuration_type"`
	BaseURL           string                   `json:"base_url"`
	CredentialID      string                   `json:"credential_id"`
	CredentialVersion string                   `json:"credential_version"`
	GrantRevision     string                   `json:"grant_revision"`
	UpstreamModelID   string                   `json:"upstream_model_id"`
	NativeServices    []contract.NativeService `json:"native_services"`
	ModelTypes        []contract.Operation     `json:"model_types"`
	ServiceRoutes     []BindingServiceRoute    `json:"service_routes"`
	Capabilities      BindingCapabilities      `json:"capabilities"`
}

type ConnectionInput struct {
	Name string
	Note string
}

type PatchConnectionInput struct {
	Name             *string
	Note             *string
	ExpectedRevision int64
}
