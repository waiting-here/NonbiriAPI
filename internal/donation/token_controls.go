package donation

import (
	"context"
	"database/sql"
	"math"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type TokenBreakdown struct {
	InputTokenReserve  *string `json:"input_token_reserve"`
	OutputTokenReserve *string `json:"output_token_reserve"`
	BreakdownStartedAt int64   `json:"breakdown_started_at"`
}

type splitTokenWire struct {
	InputTokensLimit   nullableField[string] `json:"input_tokens_limit"`
	OutputTokensLimit  nullableField[string] `json:"output_tokens_limit"`
	InputTokenReserve  nullableField[string] `json:"input_token_reserve"`
	OutputTokenReserve nullableField[string] `json:"output_token_reserve"`
}

func parseTokenCount(value *string) (any, error) {
	if value == nil {
		return nil, nil
	}
	number, err := strconv.ParseInt(*value, 10, 64)
	if err != nil || number < 0 || strconv.FormatInt(number, 10) != *value {
		return nil, ErrInvalidRequest
	}
	return number, nil
}

func splitTokenInput(wire splitTokenWire) (KeyManagementInput, map[string]any, error) {
	input, canonical := KeyManagementInput{}, map[string]any{}
	for _, field := range []struct {
		name   string
		value  nullableField[string]
		target ***string
	}{
		{"input_tokens_limit", wire.InputTokensLimit, &input.InputTokensLimit},
		{"output_tokens_limit", wire.OutputTokensLimit, &input.OutputTokensLimit},
		{"input_token_reserve", wire.InputTokenReserve, &input.InputTokenReserve},
		{"output_token_reserve", wire.OutputTokenReserve, &input.OutputTokenReserve},
	} {
		if !field.value.Set {
			continue
		}
		if _, err := parseTokenCount(field.value.Value); err != nil {
			return input, nil, err
		}
		value := field.value.Value
		*field.target = &value
		canonical[field.name] = value
	}
	if (input.InputTokenReserve == nil) != (input.OutputTokenReserve == nil) {
		return input, nil, ErrInvalidRequest
	}
	return input, canonical, nil
}

func splitTokenChanged(input KeyManagementInput) bool {
	return input.InputTokensLimit != nil || input.OutputTokensLimit != nil || input.InputTokenReserve != nil || input.OutputTokenReserve != nil
}

func applySplitTokenControlsTx(ctx context.Context, tx *sql.Tx, keyID int64, input KeyManagementInput) error {
	if !splitTokenChanged(input) {
		return nil
	}
	var inputLimit, outputLimit []byte
	var inputReserve, outputReserve sql.NullInt64
	var recurring bool
	if err := tx.QueryRowContext(ctx, `SELECT input_token_limit_mag,output_token_limit_mag,input_token_reserve,output_token_reserve,
EXISTS(SELECT 1 FROM donation_quota_rules r JOIN donation_quota_epochs e ON e.rule_id=r.id AND e.epoch=r.current_epoch WHERE r.donation_key_id=donation_keys.id AND e.metric IN ('input_tokens','output_tokens'))
FROM donation_keys WHERE id=?`, keyID).Scan(&inputLimit, &outputLimit, &inputReserve, &outputReserve, &recurring); err != nil {
		return err
	}
	for _, field := range []struct {
		value  **string
		target *[]byte
	}{{input.InputTokensLimit, &inputLimit}, {input.OutputTokensLimit, &outputLimit}} {
		if field.value == nil {
			continue
		}
		if _, err := parseTokenCount(*field.value); err != nil {
			return err
		}
		*field.target = nil
		if *field.value != nil {
			wide, err := db.ParseU128Decimal(**field.value)
			if err != nil {
				return ErrInvalidRequest
			}
			*field.target = db.EncodeU128(wide)
		}
	}
	if (input.InputTokenReserve == nil) != (input.OutputTokenReserve == nil) {
		return ErrInvalidRequest
	}
	for _, field := range []struct {
		value  **string
		target *sql.NullInt64
	}{{input.InputTokenReserve, &inputReserve}, {input.OutputTokenReserve, &outputReserve}} {
		if field.value == nil {
			continue
		}
		value, err := parseTokenCount(*field.value)
		if err != nil {
			return err
		}
		*field.target = sql.NullInt64{}
		if value != nil {
			*field.target = sql.NullInt64{Int64: value.(int64), Valid: true}
		}
	}
	if inputReserve.Valid != outputReserve.Valid || inputReserve.Valid && (inputReserve.Int64 > math.MaxInt64-outputReserve.Int64 || inputReserve.Int64+outputReserve.Int64 <= 0) ||
		!inputReserve.Valid && (inputLimit != nil || outputLimit != nil || recurring) {
		return ErrInvalidRequest
	}
	_, err := tx.ExecContext(ctx, `UPDATE donation_keys SET input_token_limit_mag=?,output_token_limit_mag=?,input_token_reserve=?,output_token_reserve=? WHERE id=?`, inputLimit, outputLimit, inputReserve, outputReserve, keyID)
	return err
}

func readSplitTokenProjection(ctx context.Context, tx *sql.Tx, keyID int64, item *AdminDonationKey) error {
	var inLimit, outLimit, inUsed, outUsed, inReserved, outReserved, unattributed []byte
	var inReserve, outReserve sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT input_token_limit_mag,output_token_limit_mag,input_token_reserve,output_token_reserve,input_tokens_used,output_tokens_used,input_tokens_reserved,output_tokens_reserved,breakdown_started_at,unattributed_total_tokens FROM donation_keys WHERE id=?`, keyID).
		Scan(&inLimit, &outLimit, &inReserve, &outReserve, &inUsed, &outUsed, &inReserved, &outReserved, &item.BreakdownStartedAt, &unattributed)
	if err != nil {
		return err
	}
	if inReserve.Valid != outReserve.Valid {
		return ErrInvariant
	}
	if inReserve.Valid {
		a, b := strconv.FormatInt(inReserve.Int64, 10), strconv.FormatInt(outReserve.Int64, 10)
		item.InputTokenReserve, item.OutputTokenReserve = &a, &b
	}
	item.Limits.InputTokens, err = nullableDecimalFromBlob(inLimit)
	if err == nil {
		item.Limits.OutputTokens, err = nullableDecimalFromBlob(outLimit)
	}
	for _, field := range []struct {
		value  []byte
		target *string
	}{{inUsed, &item.Usage.InputTokensUsed}, {outUsed, &item.Usage.OutputTokensUsed}, {inReserved, &item.Usage.InputTokensInflight}, {outReserved, &item.Usage.OutputTokensInflight}, {unattributed, &item.Usage.UnattributedTotalTokens}} {
		if err != nil {
			return err
		}
		*field.target, err = decimalFromBlob(field.value)
	}
	if err != nil {
		return err
	}
	if item.CharityState == "available" {
		exhausted, err := anyCapExhausted(inLimit, inUsed, inReserved, outLimit, outUsed, outReserved)
		if err != nil {
			return err
		}
		if exhausted {
			item.CharityState = "exhausted"
		}
	}
	return nil
}
