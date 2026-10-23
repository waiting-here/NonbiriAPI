package stewardautomation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type nativeServiceSelection []contract.NativeService

func (value *nativeServiceSelection) UnmarshalJSON(body []byte) error {
	var services []contract.NativeService
	if err := json.Unmarshal(body, &services); err != nil {
		return errInvalid
	}
	set, err := contract.ParseNativeServices(services)
	if err != nil || set.Empty() {
		return errInvalid
	}
	*value = set.Services()
	return nil
}
func (value nativeServiceSelection) pointer() *[]contract.NativeService {
	if value == nil {
		return nil
	}
	services := []contract.NativeService(value)
	return &services
}

type strictBool bool

func (value *strictBool) UnmarshalJSON(body []byte) error {
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return errInvalid
	}
	var current bool
	if err := json.Unmarshal(body, &current); err != nil {
		return errInvalid
	}
	*value = strictBool(current)
	return nil
}

type rerankSelection string

func (value *rerankSelection) UnmarshalJSON(body []byte) error {
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return errInvalid
	}
	var current string
	if err := json.Unmarshal(body, &current); err != nil {
		return errInvalid
	}
	*value = rerankSelection(current)
	return nil
}

func attachCreatedDonationSourceTx(ctx context.Context, tx *sql.Tx, userID, keyID int64, input *donation.CreateKeyInput) error {
	var sourceID, sourceRevisionID, versionID int64
	err := tx.QueryRowContext(ctx, `SELECT source_id,source_revision_id,credential_version_id FROM endpoint_key_discovery_sources WHERE endpoint_key_id=?`, keyID).Scan(&sourceID, &sourceRevisionID, &versionID)
	if err == sql.ErrNoRows {
		return resources.ErrResourceLocked
	}
	if err != nil {
		return err
	}
	grant, err := connectionstore.LoadSelfGrantTx(ctx, tx, userID, keyID)
	if err != nil {
		return err
	}
	source, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: userID, SourceID: sourceID, Purpose: connectionstore.SourceSelf})
	if err != nil {
		return err
	}
	if source.SourceRevisionID != sourceRevisionID || source.CredentialVersionID != versionID || versionID != grant.CredentialVersionID {
		return resources.ErrConflict
	}
	input.DiscoverySourceID = &sourceID
	input.DiscoverySourceRevision = &source.SourceRevision
	input.CredentialID = grant.CredentialID
	input.CredentialVersion = grant.CredentialVersion
	input.GrantRevision = grant.GrantRevision
	return nil
}
