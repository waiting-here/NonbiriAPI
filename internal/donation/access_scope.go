package donation

import (
	"context"
	"database/sql"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"strconv"
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
	if a.mode == authz.DonationScopeFull {
		return "1", nil
	}
	predicate := "0"
	var args []any
	if a.review {
		predicate = "(" + pendingUseReviewSQL + ")"
		args = []any{now}
	}
	if a.mode == authz.DonationScopeEnhanced {
		predicate = "(" + predicate + " OR " + charityscope.TaggedDonationSQL + ")"
	}
	return predicate, args
}
func (a donationAccessScope) wholeTx(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	if a.mode == authz.DonationScopeFull {
		return true, nil
	}
	if a.mode != authz.DonationScopeEnhanced {
		return false, nil
	}
	var tagged bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donations d WHERE d.id=? AND `+charityscope.TaggedDonationSQL+`)`, id).Scan(&tagged)
	return tagged, err
}
func (a donationAccessScope) requireTx(ctx context.Context, tx *sql.Tx, id, now int64) (bool, error) {
	whole, err := a.wholeTx(ctx, tx, id)
	if err != nil || whole {
		return whole, err
	}
	if !a.review {
		return false, ErrNotFound
	}
	if err := requirePendingUseReviewTx(ctx, tx, id, now); err != nil {
		return false, err
	}
	return false, nil
}
func (a donationAccessScope) projectTx(ctx context.Context, tx *sql.Tx, value AdminDonation) (AdminDonation, error) {
	id, err := strconv.ParseInt(value.ID, 10, 64)
	if err != nil {
		return value, ErrInvariant
	}
	whole, err := a.wholeTx(ctx, tx, id)
	if err != nil {
		return value, err
	}
	if !whole {
		keys, err := pendingReviewKeysTx(ctx, tx, []int64{id})
		if err != nil {
			return value, err
		}
		kept := value.Keys[:0]
		for _, key := range value.Keys {
			if keys[key.ID] {
				kept = append(kept, key)
			}
		}
		value.Keys = kept
	}
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
	id, err := strconv.ParseInt(value.ID, 10, 64)
	if err != nil {
		return value, ErrInvariant
	}
	whole, err := a.wholeTx(ctx, tx, id)
	if err != nil {
		return value, err
	}
	forceAllowed := whole && a.review && a.force
	value.CanForceReject = value.CanForceReject && forceAllowed
	if !forceAllowed {
		value.ForceRejectUnavailableReason = nil
	}
	for i := range value.Keys {
		value.Keys[i].CanManage = value.Keys[i].CanManage && whole && a.manage
	}
	if a.redact {
		value.Owner, value.Reviewer = nil, nil
		value.DiscordPublicThanks = nil
		for i := range value.Keys {
			value.Keys[i].EndpointKeyID = nil
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
