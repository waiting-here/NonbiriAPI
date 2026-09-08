package charity

import (
	"context"
	"database/sql"
	"errors"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func (s *Service) revalidateDispatchKey(ctx context.Context, tx *sql.Tx, input claim.CharityDispatch) error {
	check := claim.CharityClaimInput{RequestID: input.RequestID, ClaimID: input.ClaimID, ActorUserID: input.ActorUserID, ClaimedAt: input.DispatchedAt}
	err := tx.QueryRowContext(ctx, `SELECT c.donation_key_id,c.endpoint_key_id,k.endpoint_id,l.upstream_model_id FROM dispatch_claims c JOIN endpoint_keys k ON k.id=c.endpoint_key_id JOIN request_logs l ON l.logical_request_id=c.logical_request_id WHERE c.id=?`, input.ClaimID).Scan(&check.DonationKeyID, &check.EndpointKeyID, &check.EndpointID, &check.UpstreamModelID)
	if errors.Is(err, sql.ErrNoRows) {
		return claim.ErrNotFound
	}
	if err != nil {
		return err
	}
	row, err := readClaimKey(ctx, tx, check)
	if err != nil {
		return err
	}
	if row.status != "approved" || row.endedReason != "" || row.keyEnabled != 1 || row.failureDisabled != 0 || row.endpointEnabled != 1 || row.physicalKeyEnabled != 1 || (row.expiresAt.Valid && row.expiresAt.Int64 <= input.DispatchedAt) {
		return claim.ErrNotFound
	}
	reservation, err := readUsageReservation(ctx, tx, input.RequestID, input.ClaimID)
	if err != nil {
		return err
	}
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
		for _, query := range []string{
			`UPDATE donation_usage_reservations SET tokens_reserved=? WHERE claim_id=? AND state='reserved' AND tokens_reserved=?`,
			`UPDATE dispatch_claims SET reserved_tokens=? WHERE id=? AND state='claimed' AND reserved_tokens=?`,
		} {
			result, err := tx.ExecContext(ctx, query, row.tokenReserve, input.ClaimID, reservation.tokensReserved)
			if err != nil {
				return err
			}
			if err := requireOne(result); err != nil {
				return err
			}
		}
	}
	return nil
}
