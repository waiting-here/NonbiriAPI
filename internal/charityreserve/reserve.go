// Package charityreserve resolves the admission amount shared by charity
// routing and transaction-local accounting. Accepted requests freeze the
// returned amount in their existing reservation record.
package charityreserve

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

var ErrNotConfigured = errors.New("charity reservation: amount is not configured")

func Resolve(ctx context.Context, tx *sql.Tx, modelID int64) (int64, error) {
	if ctx == nil || tx == nil || modelID <= 0 {
		return 0, errors.New("charity reservation: invalid input")
	}
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(
(SELECT amount_milli FROM charity_model_token_reserves WHERE model_id=?),
(SELECT value FROM site_config WHERE key='charity_token_reserve_milli'))`, modelID).Scan(&raw); err != nil {
		return 0, fmt.Errorf("charity reservation: read amount: %w", err)
	}
	value, err := strconv.ParseInt(raw.String, 10, 64)
	if !raw.Valid {
		return 0, ErrNotConfigured
	}
	if err != nil || value < 1 || value > db.MaxMoneyMilli {
		return 0, errors.New("charity reservation: invalid configured amount")
	}
	return value, nil
}

// WithOutputFloor raises the existing one-request reservation to cover a
// forced maximum output. The regular configured reserve remains the floor.
// It uses the same exact per-million-token pricing and finite money cap as
// ordinary charity charging; no second reservation is created.
func WithOutputFloor(configured, outputUnitPrice, outputTokens int64) (int64, error) {
	if configured < 0 || configured > db.MaxMoneyMilli || outputUnitPrice < 0 || outputUnitPrice > db.MaxMoneyMilli || outputTokens < 0 || outputTokens > 2147483647 {
		return 0, errors.New("charity reservation: invalid output budget")
	}
	price, err := credits.PriceTokenUsage(credits.TokenUsage{Output: outputTokens}, credits.TokenPrices{Output: outputUnitPrice})
	if err != nil || price > db.MaxMoneyMilli {
		return 0, errors.New("charity reservation: output budget exceeds limit")
	}
	if price > configured {
		return price, nil
	}
	return configured, nil
}

// OutputUnitPrice covers every output rate that a frozen request may select.
// Fallback rates remain relevant when an upstream omits classified usage.
func OutputUnitPrice(fallback int64, contextPricing string) (int64, error) {
	if fallback < 0 || fallback > db.MaxMoneyMilli {
		return 0, errors.New("charity reservation: invalid output price")
	}
	if contextPricing == "" {
		contextPricing = "{}"
	}
	policy, err := credits.DecodeContextPricing(contextPricing)
	if err != nil {
		return 0, err
	}
	maximum := fallback
	include := func(p *credits.ModalityPricing) {
		if p == nil || !p.OutputEnabled {
			return
		}
		for _, rate := range []*credits.TokenPrices{p.Text, p.Image, p.Audio, p.Video} {
			if rate != nil && rate.Output > maximum {
				maximum = rate.Output
			}
		}
	}
	include(policy.UserModalities)
	if tier := policy.LongContext; tier != nil {
		if tier.UserPrices[3] > maximum {
			maximum = tier.UserPrices[3]
		}
		include(tier.UserModalities)
	}
	if maximum > db.MaxMoneyMilli {
		return 0, errors.New("charity reservation: output price exceeds limit")
	}
	return maximum, nil
}
