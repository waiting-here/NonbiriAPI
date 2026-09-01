package charity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const maxConsumerExportRows = 10_000

var ErrResourceLimit = errors.New("charity: resource limit exceeded")

// ConsumerExport is the closed, identity-free aggregate of the account's
// charity consumption. It contains no model, donation, endpoint, key, claim,
// worker, or ledger identity.
type ConsumerExport struct {
	RequestCount      string
	OriginalCharge    string
	Charged           string
	Saved             string
	DonorReward       string
	UncachedInput     string
	CacheWriteInput   string
	CacheReadInput    string
	Output            string
	UsageUnknownCount string
}

// ExportConsumerTx computes the consumer aggregate from the caller-owned
// transaction and fixed decision time. Terminal rows obey their ordinary
// 400-day retention cutoff even when physical cleanup has not yet run.
func (s *Service) ExportConsumerTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
	limit int,
) (ConsumerExport, error) {
	if s == nil || ctx == nil || tx == nil || userID <= 0 || !validTime(decisionNow) ||
		limit < 1 || limit > maxConsumerExportRows {
		return ConsumerExport{}, claim.ErrInvalidInput
	}
	cutoff := decisionNow - terminalRetention
	rows, err := tx.QueryContext(ctx, `SELECT original_charge_milli,user_charge_milli,
donor_reward_total_mag,usage_uncached_input_tokens,cache_write_input_tokens,
cache_read_input_tokens,usage_output_tokens,usage_unknown
FROM charity_reservations
WHERE user_id=? AND (
 state IN ('reserved','dispatched') OR
 (state IN ('committed','released') AND finalized_at IS NOT NULL AND finalized_at>?)
)
ORDER BY created_at,id LIMIT ?`, userID, cutoff, limit+1)
	if err != nil {
		return ConsumerExport{}, fmt.Errorf("charity: read consumer export: %w", err)
	}
	defer rows.Close()

	requestCount := 0
	originalCharge, charged, donorReward := new(big.Int), new(big.Int), new(big.Int)
	uncachedInput, cacheWriteInput := new(big.Int), new(big.Int)
	cacheReadInput, output := new(big.Int), new(big.Int)
	usageUnknownCount := new(big.Int)
	for rows.Next() {
		requestCount++
		if requestCount > limit {
			return ConsumerExport{}, ErrResourceLimit
		}
		var original, userCharge, uncached, cacheWrite, cacheRead, outputTokens int64
		var usageUnknown int
		var rewardBlob []byte
		if err := rows.Scan(&original, &userCharge, &rewardBlob, &uncached, &cacheWrite,
			&cacheRead, &outputTokens, &usageUnknown); err != nil {
			return ConsumerExport{}, fmt.Errorf("charity: scan consumer export: %w", err)
		}
		if original < 0 || userCharge < 0 || userCharge > original || uncached < 0 || cacheWrite < 0 ||
			cacheRead < 0 || outputTokens < 0 || usageUnknown != 0 && usageUnknown != 1 {
			return ConsumerExport{}, claim.ErrInvariant
		}
		reward, err := db.DecodeU128(rewardBlob)
		if err != nil {
			return ConsumerExport{}, claim.ErrInvariant
		}
		for _, pair := range []struct {
			total *big.Int
			value *big.Int
		}{
			{originalCharge, big.NewInt(original)},
			{charged, big.NewInt(userCharge)},
			{donorReward, reward.Big()},
			{uncachedInput, big.NewInt(uncached)},
			{cacheWriteInput, big.NewInt(cacheWrite)},
			{cacheReadInput, big.NewInt(cacheRead)},
			{output, big.NewInt(outputTokens)},
			{usageUnknownCount, big.NewInt(int64(usageUnknown))},
		} {
			if err := addConsumerExportValue(pair.total, pair.value); err != nil {
				return ConsumerExport{}, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return ConsumerExport{}, fmt.Errorf("charity: iterate consumer export: %w", err)
	}

	saved := new(big.Int).Sub(new(big.Int).Set(originalCharge), charged)
	if saved.Sign() < 0 {
		return ConsumerExport{}, claim.ErrInvariant
	}
	return ConsumerExport{
		RequestCount:      new(big.Int).SetInt64(int64(requestCount)).String(),
		OriginalCharge:    formatConsumerMilli(originalCharge),
		Charged:           formatConsumerMilli(charged),
		Saved:             formatConsumerMilli(saved),
		DonorReward:       formatConsumerMilli(donorReward),
		UncachedInput:     uncachedInput.String(),
		CacheWriteInput:   cacheWriteInput.String(),
		CacheReadInput:    cacheReadInput.String(),
		Output:            output.String(),
		UsageUnknownCount: usageUnknownCount.String(),
	}, nil
}

// CleanupTx performs one bounded terminal-reservation cleanup batch in the
// caller-owned transaction. In-flight rows are never age-deleted.
func (s *Service) CleanupTx(ctx context.Context, tx *sql.Tx, decisionNow int64, limit int) (int, error) {
	if s == nil || ctx == nil || tx == nil || !validTime(decisionNow) || limit < 1 || limit > 100 {
		return 0, claim.ErrInvalidInput
	}
	cutoff := decisionNow - terminalRetention
	if cutoff < 0 {
		cutoff = 0
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM donation_usage_reservations WHERE claim_id IN (
SELECT claim_id FROM donation_usage_reservations
WHERE state IN ('committed','released') AND finalized_at<=? ORDER BY finalized_at,claim_id LIMIT ?)`, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("charity: delete terminal usage: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("charity: count cleanup: %w", err)
	}
	remaining := int64(limit) - count
	if remaining > 0 {
		result, err = tx.ExecContext(ctx, `DELETE FROM charity_reservations WHERE logical_request_id IN (
SELECT logical_request_id FROM charity_reservations
WHERE state IN ('committed','released') AND finalized_at<=? ORDER BY finalized_at,logical_request_id LIMIT ?)`, cutoff, remaining)
		if err != nil {
			return 0, fmt.Errorf("charity: delete terminal request reservations: %w", err)
		}
		requestCount, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("charity: count request cleanup: %w", err)
		}
		count += requestCount
	}
	return int(count), nil
}

func addConsumerExportValue(total, value *big.Int) error {
	if total == nil || value == nil || value.Sign() < 0 {
		return claim.ErrInvariant
	}
	total.Add(total, value)
	if _, err := db.U128FromBig(total); err != nil {
		return claim.ErrInvariant
	}
	return nil
}

func formatConsumerMilli(value *big.Int) string {
	if value == nil || value.Sign() == 0 {
		return "0"
	}
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(value, big.NewInt(1000), remainder)
	text := whole.String()
	if remainder.Sign() == 0 {
		return text
	}
	fraction := fmt.Sprintf("%03d", remainder.Int64())
	for fraction[len(fraction)-1] == '0' {
		fraction = fraction[:len(fraction)-1]
	}
	return text + "." + fraction
}
