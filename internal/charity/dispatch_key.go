package charity

import (
	"context"
	"database/sql"
	"errors"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func (s *Service) revalidateDispatchKey(ctx context.Context, tx *sql.Tx, input claim.CharityDispatch) error {
	check := claim.CharityClaimInput{RequestID: input.RequestID, ClaimID: input.ClaimID, ActorUserID: input.ActorUserID, ClaimedAt: input.DispatchedAt}
	a := &check.Authorization
	var nativeBit int64
	err := tx.QueryRowContext(ctx, `SELECT c.donation_key_id,t.endpoint_key_id_snapshot,t.endpoint_id_snapshot,t.upstream_model_id,
b.binding_id,t.target_id_snapshot,t.target_revision,t.credential_id_snapshot,t.credential_version_id,t.credential_version,
t.grant_revision_id_snapshot,t.grant_revision,t.configured_type,t.rerank_format,t.use_id_snapshot,t.use_revision_id_snapshot,u.use_revision,t.native_service_bit
FROM dispatch_claims c JOIN dispatch_claim_targets t ON t.claim_id=c.id AND t.authorization_kind='charity'
JOIN charity_binding_targets b ON b.id=t.target_id_snapshot
JOIN donation_use_revisions u ON u.id=t.use_revision_id_snapshot AND u.use_id=t.use_id_snapshot
WHERE c.id=? AND c.state='claimed'`, input.ClaimID).Scan(&check.DonationKeyID, &check.EndpointKeyID, &check.EndpointID, &check.UpstreamModelID,
		&a.BindingID, &a.TargetID, &a.TargetRevision, &a.CredentialID, &a.CredentialVersionID, &a.CredentialVersion,
		&a.GrantRevisionID, &a.GrantRevision, &a.ConfiguredType, &a.RerankFormat, &a.UseID, &a.UseRevisionID, &a.UseRevision, &nativeBit)
	if errors.Is(err, sql.ErrNoRows) {
		return claim.ErrNotFound
	}
	if err != nil {
		return err
	}
	services, err := contract.NativeServicesFromMask(nativeBit)
	if err != nil || len(services.Services()) != 1 {
		return claim.ErrInvariant
	}
	a.NativeService = services.Services()[0]
	row, err := readClaimKey(ctx, tx, check)
	if err != nil {
		return err
	}
	if !row.approvedUse || row.endedReason != "" || row.keyEnabled != 1 || row.failureDisabled != 0 || row.endpointEnabled != 1 || row.physicalKeyEnabled != 1 || (row.expiresAt.Valid && row.expiresAt.Int64 <= input.DispatchedAt) {
		return claim.ErrNotFound
	}
	if row.rateLimitedUntil.Valid && row.rateLimitedUntil.Int64 > input.DispatchedAt {
		return claim.ErrKeyRateLimited
	}
	reservation, err := readUsageReservation(ctx, tx, input.RequestID, input.ClaimID)
	if err != nil {
		return err
	}
	budget, err := donationquota.ReadTokenBudget(ctx, tx, row.donationKeyID)
	if err != nil {
		return err
	}
	row.tokenReserve = budget.Reservation.Total
	for _, dimension := range []struct {
		used, reserved, limit []byte
		own, next             int64
	}{
		{row.priceUsed, row.priceReserved, row.priceLimit, reservation.priceReserved, reservation.priceReserved},
		{row.callsUsed, row.callsReserved, row.callLimit, int64(reservation.callsReserved), int64(reservation.callsReserved)},
		{row.tokensUsed, row.tokensReserved, row.tokenLimit, reservation.tokensReserved, row.tokenReserve},
	} {
		u, err := db.DecodeU128(dimension.used)
		if err != nil {
			return claim.ErrInvariant
		}
		r, err := db.DecodeU128(dimension.reserved)
		if err != nil {
			return claim.ErrInvariant
		}
		withoutOwn := new(big.Int).Sub(r.Big(), big.NewInt(dimension.own))
		if withoutOwn.Sign() < 0 {
			return claim.ErrInvariant
		}
		if dimension.limit == nil {
			continue
		}
		limit, err := db.DecodeU128(dimension.limit)
		if err != nil {
			return claim.ErrInvariant
		}
		occupied := new(big.Int).Add(u.Big(), withoutOwn)
		if occupied.Cmp(limit.Big()) >= 0 || new(big.Int).Add(occupied, big.NewInt(dimension.next)).Cmp(limit.Big()) > 0 {
			return donationquota.ErrLimited
		}
	}
	// The per-key token fallback is mutable until dispatch. Replace our own
	// total reservation and its claim snapshot before reconciling the current
	// recurring rules. A later rejection rolls all three changes back together.
	if err := budget.ReplaceReservation(ctx, tx, row.donationKeyID, reservation.tokenVector()); err != nil {
		return err
	}
	if reservation.tokensReserved != row.tokenReserve {
		previous, err := db.DecodeU128(row.tokensReserved)
		if err != nil {
			return claim.ErrInvariant
		}
		next, err := db.U128FromBig(new(big.Int).Add(new(big.Int).Sub(previous.Big(), big.NewInt(reservation.tokensReserved)), big.NewInt(row.tokenReserve)))
		if err != nil {
			return claim.ErrInvariant
		}
		result, err := tx.ExecContext(ctx, `UPDATE donation_keys SET tokens_reserved=?,updated_at=? WHERE id=? AND tokens_reserved=?`, db.EncodeU128(next), input.DispatchedAt, row.donationKeyID, row.tokensReserved)
		if err != nil {
			return err
		}
		if err := requireOne(result); err != nil {
			return err
		}
	}
	for _, query := range []string{
		`UPDATE donation_usage_reservations SET tokens_reserved=?,input_tokens_reserved=?,output_tokens_reserved=? WHERE claim_id=? AND state='reserved' AND tokens_reserved=? AND input_tokens_reserved IS ? AND output_tokens_reserved IS ?`,
		`UPDATE dispatch_claims SET reserved_tokens=?,reserved_input_tokens=?,reserved_output_tokens=? WHERE id=? AND state='claimed' AND reserved_tokens=? AND reserved_input_tokens IS ? AND reserved_output_tokens IS ?`,
	} {
		result, err := tx.ExecContext(ctx, query, row.tokenReserve, budget.Reservation.Input, budget.Reservation.Output, input.ClaimID, reservation.tokensReserved, reservation.inputTokensReserved, reservation.outputTokensReserved)
		if err != nil {
			return err
		}
		if err := requireOne(result); err != nil {
			return err
		}
	}
	return nil
}
