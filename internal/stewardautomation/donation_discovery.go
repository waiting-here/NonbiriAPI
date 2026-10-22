package stewardautomation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type ownedDiscovery struct {
	identity  charityrouting.OwnBindingDiscovery
	selection resources.ManagedDiscoverySelection
	sourceID  int64
}

func ownDiscoveryTx(ctx context.Context, tx *sql.Tx, userID, keyID, now int64) (ownedDiscovery, error) {
	var selected ownedDiscovery
	choice, err := donation.SelectApprovedDiscoveryUseTx(ctx, tx, keyID, now)
	if err != nil {
		return selected, err
	}
	use := choice.Use
	if use.Grant.OwnerUserID != userID {
		return selected, resources.ErrNotFound
	}
	selected.identity = charityrouting.OwnBindingDiscovery{UseID: use.UseID, ApprovedRevisionID: use.ApprovedRevisionID, SourceRevisionID: use.DiscoverySourceRevisionID, CredentialVersionID: use.Grant.CredentialVersionID, NativeService: use.NativeService}
	selected.selection = resources.ManagedDiscoverySelection{UseID: use.UseID, ExpectedRevision: use.UseRevision}
	selected.sourceID = choice.SourceID
	if err := revalidateOwnDiscoveryTx(ctx, tx, userID, keyID, selected); err != nil {
		return ownedDiscovery{}, err
	}
	return selected, nil
}

func revalidateOwnDiscoveryTx(ctx context.Context, tx *sql.Tx, userID, keyID int64, selected ownedDiscovery) error {
	use, err := connectionstore.LoadApprovedUseTx(ctx, tx, keyID, selected.identity.NativeService)
	if errors.Is(err, connectionstore.ErrNotFound) || errors.Is(err, connectionstore.ErrUnavailable) {
		return resources.ErrConflict
	}
	if err != nil {
		return err
	}
	want := selected.identity
	if !use.DiscoveryEnabled || use.Grant.OwnerUserID != userID || use.UseID != want.UseID || use.ApprovedRevisionID != want.ApprovedRevisionID || use.DiscoverySourceRevisionID != want.SourceRevisionID || use.Grant.CredentialVersionID != want.CredentialVersionID {
		return resources.ErrConflict
	}
	source, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: userID, SourceID: selected.sourceID, Purpose: connectionstore.SourceCharity, DonationUseID: use.UseID})
	if err != nil {
		return err
	}
	if source.SourceRevisionID != want.SourceRevisionID || source.CredentialVersionID != want.CredentialVersionID || source.UseApprovedRevisionID != want.ApprovedRevisionID {
		return resources.ErrConflict
	}
	return nil
}
