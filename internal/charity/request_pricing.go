package charity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/usageinfo"
)

type requestCharge struct {
	original, charge, fine int64
	pricing                usageinfo.Pricing
}

func calculateRequestChargeTx(ctx context.Context, tx *sql.Tx, requestID string, disposition claim.AccountingDisposition) (int64, int64, int64, error) {
	result, err := calculateRequestPricingTx(ctx, tx, requestID, disposition)
	return result.original, result.charge, result.fine, err
}

func calculateRequestPricingTx(ctx context.Context, tx *sql.Tx, requestID string, disposition claim.AccountingDisposition) (requestCharge, error) {
	if disposition == claim.AccountingRelease {
		return requestCharge{}, nil
	}
	var started bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_usage_reservations u
JOIN dispatch_claims c ON c.id=u.claim_id
WHERE c.logical_request_id=? AND u.state='committed' AND u.calls_actual=1)`, requestID).Scan(&started); err != nil {
		return requestCharge{}, fmt.Errorf("charity: read response evidence: %w", err)
	}
	if !started {
		return requestCharge{}, nil
	}
	var mode, contextPricing, details string
	var discount int
	var scalar, reserved int64
	var prices credits.TokenPrices
	var usage contract.Usage
	var unknown int
	err := tx.QueryRowContext(ctx, `SELECT pricing_mode,discount_percent,request_user_price_milli,user_reserved_milli,
uncached_user_price_milli,cache_write_user_price_milli,cache_read_user_price_milli,output_user_price_milli,
usage_uncached_input_tokens,cache_write_input_tokens,cache_read_input_tokens,usage_output_tokens,usage_unknown,context_pricing,usage_details
FROM charity_reservations WHERE logical_request_id=?`, requestID).Scan(
		&mode, &discount, &scalar, &reserved, &prices.UncachedInput, &prices.CacheWriteInput, &prices.CacheReadInput, &prices.Output,
		&usage.UncachedInputTokens, &usage.CacheWriteInputTokens, &usage.CacheReadInputTokens, &usage.OutputTokens, &unknown, &contextPricing, &details)
	if errors.Is(err, sql.ErrNoRows) {
		return requestCharge{}, claim.ErrNotFound
	}
	if err != nil {
		return requestCharge{}, fmt.Errorf("charity: calculate request charge: %w", err)
	}
	policy, err := credits.DecodeContextPricing(contextPricing)
	if err != nil {
		return requestCharge{}, claim.ErrInvariant
	}
	usage.Present = unknown == 0
	usage, err = usageinfo.Apply(usage, details)
	if err != nil {
		return requestCharge{}, claim.ErrInvariant
	}
	// Valid reports already accumulated before an unknown attempt still provide
	// a lower bound for the existing long-context threshold and penalty rules.
	tokens := tokenUsage(usage)
	scalar, prices = policy.SelectPrices(tokens, scalar, prices)
	original, decision, priceErr := priceUsage(mode, usage, scalar, prices, policy.SelectModalityPricing(tokens, false))
	missing := !usage.Present
	if mode == "per_search_unit" {
		missing = !usage.SearchUnitsPresent
	}
	if priceErr != nil && !(mode == "per_token" && missing) {
		return requestCharge{}, priceErr
	}
	if mode != "per_request" && missing && original < reserved {
		original = reserved
	}
	charge, err := credits.ApplyDiscountPercent(original, discount)
	if err != nil {
		return requestCharge{}, claim.ErrInvariant
	}
	if missing && charge > reserved {
		charge = reserved
	}
	// Apply the fine once, after discount and the conservative charge cap. It
	// never contributes to donor rewards or donation-key usage.
	fine := policy.Fine(tokens)
	original, err = credits.Add(original, fine)
	if err != nil {
		return requestCharge{}, claim.ErrInvariant
	}
	charge, err = credits.Add(charge, fine)
	if err != nil {
		return requestCharge{}, claim.ErrInvariant
	}
	if !recordsPricing(mode, policy, false) {
		decision = usageinfo.Pricing{}
	}
	return requestCharge{original: original, charge: charge, fine: fine, pricing: decision}, nil
}

func persistCallerPricing(ctx context.Context, tx *sql.Tx, requestID string, pricing usageinfo.Pricing) error {
	// The reservation includes only started responses. The caller log includes
	// every attempt, so attach the decision without copying either usage total.
	for _, table := range []string{"charity_reservations", "request_logs"} {
		var details string
		err := tx.QueryRowContext(ctx, `SELECT usage_details FROM `+table+` WHERE logical_request_id=?`, requestID).Scan(&details)
		if table == "request_logs" && errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("charity: read caller usage details: %w", err)
		}
		details, err = usageinfo.WithPricing(details, pricing)
		if err != nil {
			return claim.ErrInvariant
		}
		result, err := tx.ExecContext(ctx, `UPDATE `+table+` SET usage_details=? WHERE logical_request_id=?`, details, requestID)
		if err != nil {
			return fmt.Errorf("charity: persist caller pricing decision: %w", err)
		}
		if err := requireOne(result); err != nil {
			return err
		}
	}
	return nil
}
