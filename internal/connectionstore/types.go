// Package connectionstore resolves persisted credential authorizations inside
// its caller's transaction. Domain permissions and accounting remain with the
// calling service.
package connectionstore

import (
	"errors"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

var (
	ErrNotFound    = errors.New("connectionstore: not found")
	ErrUnavailable = errors.New("connectionstore: authorization unavailable")
	ErrInvariant   = errors.New("connectionstore: invariant violation")
)

// GrantAuthorization contains internal facts, never a transport projection.
type GrantAuthorization struct {
	OwnerUserID         int64                              `json:"-"`
	EndpointID          int64                              `json:"-"`
	EndpointKeyID       int64                              `json:"-"`
	CredentialID        int64                              `json:"-"`
	CredentialVersionID int64                              `json:"-"`
	CredentialVersion   int64                              `json:"-"`
	GrantRevisionID     int64                              `json:"-"`
	GrantRevision       int64                              `json:"-"`
	SecretRefID         int64                              `json:"-"`
	ConfiguredType      connectorcontract.Type             `json:"-"`
	CanonicalBaseURL    string                             `json:"-"`
	EndpointServices    connectorcontract.NativeServiceSet `json:"-"`
	GrantedServices     connectorcontract.NativeServiceSet `json:"-"`
	EffectiveServices   connectorcontract.NativeServiceSet `json:"-"`
	RerankFormat        connectorcontract.RerankFormat     `json:"-"`
}

type UseAuthorization struct {
	DonationKeyID             int64                           `json:"-"`
	UseID                     int64                           `json:"-"`
	UseRevision               int64                           `json:"-"` // Stable use CAS.
	ApprovedRevisionID        int64                           `json:"-"`
	ApprovedRevision          int64                           `json:"-"`
	NativeService             connectorcontract.NativeService `json:"-"`
	DiscoveryEnabled          bool                            `json:"-"`
	DiscoverySourceRevisionID int64                           `json:"-"`
	Grant                     GrantAuthorization              `json:"-"`
}

type SourcePurpose string

const (
	SourceSelf    SourcePurpose = "self"
	SourceCharity SourcePurpose = "donation_use"
)

type SourceAuthorizationRef struct {
	OwnerUserID   int64         `json:"-"`
	SourceID      int64         `json:"-"`
	Purpose       SourcePurpose `json:"-"`
	DonationUseID int64         `json:"-"`
	UseRevisionID int64         `json:"-"`
}

type SourceAuthorization struct {
	OwnerUserID           int64         `json:"-"`
	SourceID              int64         `json:"-"`
	ResourceRevision      int64         `json:"-"`
	SourceRevisionID      int64         `json:"-"`
	SourceRevision        int64         `json:"-"`
	CredentialID          int64         `json:"-"`
	CredentialVersionID   int64         `json:"-"`
	CredentialVersion     int64         `json:"-"`
	SecretRefID           int64         `json:"-"`
	CanonicalBaseURL      string        `json:"-"`
	ListFormat            string        `json:"-"`
	Purpose               SourcePurpose `json:"-"`
	DonationUseID         int64         `json:"-"`
	UseApprovedRevisionID int64         `json:"-"`
}

type KeyCredentialEvidence struct {
	OwnerUserID         int64     `json:"-"`
	EndpointID          int64     `json:"-"`
	EndpointKeyID       int64     `json:"-"`
	GrantRevisionID     int64     `json:"-"`
	CredentialVersionID int64     `json:"-"`
	Fingerprint         [32]byte  `json:"-"`
	BodyReviewHMAC      *[32]byte `json:"-"`
	DisplayHead         string    `json:"-"`
	DisplayTail         string    `json:"-"`
}

var ErrConflict = errors.New("connectionstore: revision conflict")

type UseRevisionOrigin string

const (
	UseOriginLegacy           UseRevisionOrigin = "legacy"
	UseOriginInitial          UseRevisionOrigin = "initial"
	UseOriginRotation         UseRevisionOrigin = "rotation"
	UseOriginReattachment     UseRevisionOrigin = "reattachment"
	UseOriginServiceExpansion UseRevisionOrigin = "service_expansion"
)

type StagePendingUseRevisionInput struct {
	UseID                     int64             `json:"-"`
	ExpectedRevision          int64             `json:"-"`
	GrantRevisionID           int64             `json:"-"`
	DiscoverySourceRevisionID *int64            `json:"-"`
	Origin                    UseRevisionOrigin `json:"-"`
	At                        int64             `json:"-"`
}

type UseRevisionIdentity struct {
	UseID       int64             `json:"-"`
	UseRevision int64             `json:"-"` // New stable use CAS.
	RevisionID  int64             `json:"-"`
	Revision    int64             `json:"-"` // Immutable use_revision.
	Origin      UseRevisionOrigin `json:"-"`
}
