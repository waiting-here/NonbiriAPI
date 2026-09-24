package donation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

// This predicate matches the donation key's configured available state. It
// does not require existing model bindings or spend any donation quota.
const discoveryEligibleSQL = `d.status='approved' AND dk.ended_reason IS NULL
 AND (dk.expires_at IS NULL OR dk.expires_at>?)
 AND dk.enabled=1 AND dk.failure_disabled=0 AND k.enabled=1 AND e.enabled=1
 AND EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_key_id=dk.id AND m.endpoint_key_id=k.id)
 AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions x WHERE x.endpoint_key_id=k.id)
	AND (u.is_banned=0 OR (u.banned_until IS NOT NULL AND u.banned_until<=?) OR (u.banned_until IS NULL AND u.ban_kind='protective_inactivity'))
 AND nbi_u128_remaining(dk.price_limit_mag,dk.price_used_mag,dk.price_reserved_mag,nbi_u128(0))<>nbi_u128(0)
 AND nbi_u128_remaining(dk.call_limit_mag,dk.calls_used,dk.calls_reserved,nbi_u128(0))<>nbi_u128(0)
 AND nbi_u128_remaining(dk.token_limit_mag,dk.tokens_used,dk.tokens_reserved,nbi_u128(0))<>nbi_u128(0)
 AND nbi_u128_remaining(dk.input_token_limit_mag,dk.input_tokens_used,dk.input_tokens_reserved,nbi_u128(0))<>nbi_u128(0)
 AND nbi_u128_remaining(dk.output_token_limit_mag,dk.output_tokens_used,dk.output_tokens_reserved,nbi_u128(0))<>nbi_u128(0)`

const discoveryTargetFromSQL = ` FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id
 JOIN endpoint_keys k ON k.id=dk.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id
 JOIN users u ON u.id=e.user_id AND u.id=d.user_id`

func (s *Service) AuthorizeManagedDiscovery(ctx context.Context, tx *sql.Tx, role string, actorID, donationID, donationKeyID int64, requireEligible bool) (resources.ManagedDiscoveryTarget, error) {
	var target resources.ManagedDiscoveryTarget
	if s == nil || tx == nil || nilDependency(s.roleAuth) || actorID <= 0 || donationID <= 0 || donationKeyID <= 0 {
		return target, resources.ErrInvalidRequest
	}
	scope, err := s.managementScope(ctx, tx, reviewerRole(role), actorID)
	if err != nil {
		return target, discoveryResourceError(err)
	}
	now, err := s.nowUnix()
	if err != nil {
		return target, resources.ErrUnavailable
	}
	if err := requireManagedDonationTx(ctx, tx, reviewerRole(role), donationID, now); err != nil {
		return target, discoveryResourceError(err)
	}
	if err := scope.RequireKey(ctx, tx, donationID, donationKeyID, now, false); err != nil {
		return target, discoveryResourceError(scopeError(err))
	}
	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT u.id,e.id,k.id,(`+discoveryEligibleSQL+`)`+discoveryTargetFromSQL+` WHERE d.id=? AND dk.id=?`, now, now, donationID, donationKeyID).
		Scan(&target.OwnerUserID, &target.EndpointID, &target.EndpointKeyID, &eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return target, resources.ErrNotFound
	}
	if err != nil {
		return target, resources.ErrUnavailable
	}
	if requireEligible && !eligible {
		return target, resources.ErrResourceLocked
	}
	return target, nil
}

func discoveryResourceError(err error) error {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return resources.ErrUnauthorized
	case errors.Is(err, ErrForbidden):
		return resources.ErrForbidden
	case errors.Is(err, ErrNotFound):
		return resources.ErrNotFound
	case errors.Is(err, ErrInvalidRequest):
		return resources.ErrInvalidRequest
	default:
		return resources.ErrUnavailable
	}
}
