package routing

import (
	"log/slog"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// TargetAuthorization freezes a selected model target and its exact grant.
// It contains no secret reference or credential material. Internal row IDs
// must not be projected as public credential versions or grant revisions.
type TargetAuthorization struct {
	BindingID           int64                           `json:"-"`
	TargetID            int64                           `json:"-"`
	TargetRevision      int64                           `json:"-"`
	CredentialID        int64                           `json:"-"`
	CredentialVersionID int64                           `json:"-"`
	CredentialVersion   int64                           `json:"-"`
	GrantRevisionID     int64                           `json:"-"`
	GrantRevision       int64                           `json:"-"`
	UseID               int64                           `json:"-"`
	UseRevisionID       int64                           `json:"-"`
	UseRevision         int64                           `json:"-"`
	ConfiguredType      connectorcontract.Type          `json:"-"`
	NativeService       connectorcontract.NativeService `json:"-"`
	RerankFormat        connectorcontract.RerankFormat  `json:"-"`
}

func (c Candidate) Authorization() TargetAuthorization {
	return TargetAuthorization{
		BindingID: c.bindingID, TargetID: c.targetID, TargetRevision: c.targetRevision,
		CredentialID: c.credentialID, CredentialVersionID: c.credentialVersionID, CredentialVersion: c.credentialVersion,
		GrantRevisionID: c.grantRevisionID, GrantRevision: c.grantRevision,
		ConfiguredType: connectorcontract.Type(c.connectorType), RerankFormat: c.rerankFormat,
	}
}

func (TargetAuthorization) String() string   { return "[redacted target authorization]" }
func (TargetAuthorization) GoString() string { return "[redacted target authorization]" }
func (TargetAuthorization) LogValue() slog.Value {
	return slog.StringValue("[redacted target authorization]")
}
