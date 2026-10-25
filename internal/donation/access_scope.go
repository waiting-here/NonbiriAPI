package donation

import (
	"context"
	"database/sql"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
)

type donationAccessScope struct {
	mode                          authz.DonationScope
	redact, review, manage, force bool
}

func readDonationAccessScope(ctx context.Context, tx *sql.Tx, role reviewerRole, actorID int64) (donationAccessScope, error) {
	if role == reviewerAdmin || role == recurringOwner {
		return donationAccessScope{mode: authz.DonationScopeFull, review: true, manage: true, force: true}, nil
	}
	p, err := authz.ReadStewardPermissions(ctx, tx, actorID)
	if err != nil {
		return donationAccessScope{}, mapAuthorization(err)
	}
	if p.DonationScope == authz.DonationScopeNone {
		return donationAccessScope{}, authz.ErrPermissionDenied
	}
	return donationAccessScope{mode: p.DonationScope, redact: !p.Permissions["donations.read_all"], review: p.Permissions["donations.review"], manage: p.Permissions["donations.manage_keys"], force: p.Permissions["donations.force_reject"]}, nil
}
func (a donationAccessScope) predicate(now int64) (string, []any) {
	return charityscope.DonationPredicate(a.mode, now)
}
func (a donationAccessScope) wholeTx(ctx context.Context, tx *sql.Tx, id, now int64) (bool, error) {
	predicate, args := a.predicate(now)
	args = append([]any{id}, args...)
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donations d WHERE d.id=? AND `+predicate+`)`, args...).Scan(&allowed)
	return allowed, err
}
func (a donationAccessScope) requireTx(ctx context.Context, tx *sql.Tx, id, now int64) (bool, error) {
	allowed, err := a.wholeTx(ctx, tx, id, now)
	if err == nil && !allowed {
		err = ErrNotFound
	}
	return allowed, err
}
func (a donationAccessScope) projectTx(ctx context.Context, tx *sql.Tx, value AdminDonation) (AdminDonation, error) {
	return a.projectReceiptTx(ctx, tx, value)
}
func requireDonationRangeTx(ctx context.Context, tx *sql.Tx, role reviewerRole, actorID, donationID, now int64) error {
	a, err := readDonationAccessScope(ctx, tx, role, actorID)
	if err != nil {
		return err
	}
	_, err = a.requireTx(ctx, tx, donationID, now)
	return err
}

// projectReceiptTx changes identity and action affordances without hiding the
// already-authorized decision snapshot from a successful review receipt.
func (a donationAccessScope) projectReceiptTx(ctx context.Context, tx *sql.Tx, value AdminDonation) (AdminDonation, error) {
	forceAllowed := a.review && a.force
	value.CanForceReject = value.CanForceReject && forceAllowed
	if !forceAllowed {
		value.ForceRejectUnavailableReason = nil
	}
	for i := range value.Keys {
		value.Keys[i].CanManage = value.Keys[i].CanManage && a.manage
	}
	if a.redact {
		value.Owner, value.Reviewer = nil, nil
		value.DiscordPublicThanks = nil
		for i := range value.Keys {
			value.Keys[i].EndpointKeyID = nil
			value.Keys[i].OwnerEndpointID = nil
		}
	}
	return value, nil
}

func keyManagementAllowedTx(ctx context.Context, tx *sql.Tx, role reviewerRole, actorID int64) (bool, error) {
	if role == recurringOwner {
		return false, nil
	}
	access, err := readDonationAccessScope(ctx, tx, role, actorID)
	return access.manage, err
}
