package db

// Generation 2 storage bounds shared by bootstrap preflight and the retained
// ownership projections.  They describe the current schema's byte limits;
// they are not compatibility allowances for retired storage formats.
const (
	maxStoredEndpointBaseURLBytes      = 4096
	maxEndpointCredentialEnvelopeBytes = 128 << 10
	siteConfigKeyDefaultEndpointLimit  = "default_endpoint_limit"
)
