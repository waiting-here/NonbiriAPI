package donationquota

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func ValidModel(model string) bool {
	return utf8.ValidString(model) && utf8.RuneCountInString(model) >= 1 && utf8.RuneCountInString(model) <= 512 && !strings.ContainsRune(model, 0)
}
func tokenMetric(metric string) bool {
	return metric == "tokens" || metric == "input_tokens" || metric == "output_tokens"
}

type ModelLimits struct {
	Tokens       *string `json:"tokens"`
	InputTokens  *string `json:"input_tokens"`
	OutputTokens *string `json:"output_tokens"`
}
type ModelAmounts struct {
	Tokens       string `json:"tokens"`
	InputTokens  string `json:"input_tokens"`
	OutputTokens string `json:"output_tokens"`
}
type ModelView struct {
	UpstreamModelID         string       `json:"upstream_model_id"`
	Limits                  ModelLimits  `json:"limits"`
	Used                    ModelAmounts `json:"used"`
	Reserved                ModelAmounts `json:"reserved"`
	Remaining               ModelLimits  `json:"remaining"`
	UnattributedTotalTokens string       `json:"unattributed_total_tokens"`
	TrackingStartedAt       *int64       `json:"tracking_started_at"`
}
type modelBudget struct {
	limits         [3]*db.U128
	used, reserved [3]db.U128
	unattributed   db.U128
	created        *int64
}

func modelValues(v TokenVector) [3]db.U128 {
	a := v.Amounts()
	return [3]db.U128{a.Tokens, a.InputTokens, a.OutputTokens}
}
func readModelBudget(ctx context.Context, q Reader, keyID int64, model string) (modelBudget, error) {
	var b modelBudget
	var limits, used, reserved [3][]byte
	var unattributed []byte
	err := q.QueryRowContext(ctx, `SELECT token_limit_mag,input_token_limit_mag,output_token_limit_mag,
 tokens_used,input_tokens_used,output_tokens_used,tokens_reserved,input_tokens_reserved,output_tokens_reserved,unattributed_total_tokens,created_at
 FROM donation_model_quotas WHERE donation_key_id=? AND upstream_model_id=?`, keyID, model).
		Scan(&limits[0], &limits[1], &limits[2], &used[0], &used[1], &used[2], &reserved[0], &reserved[1], &reserved[2], &unattributed, &b.created)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	for i := range 3 {
		if b.used[i], err = magnitude(used[i]); err != nil {
			return b, err
		}
		if b.reserved[i], err = magnitude(reserved[i]); err != nil {
			return b, err
		}
		if limits[i] != nil {
			n, err := magnitude(limits[i])
			if err != nil {
				return b, err
			}
			b.limits[i] = &n
		}
	}
	b.unattributed, err = magnitude(unattributed)
	return b, err
}
func modelLimits(values [3]*db.U128) ModelLimits {
	var out [3]*string
	for i, n := range values {
		if n != nil {
			value := n.Decimal()
			out[i] = &value
		}
	}
	return ModelLimits{out[0], out[1], out[2]}
}
func modelAmounts(values [3]db.U128) ModelAmounts {
	return ModelAmounts{values[0].Decimal(), values[1].Decimal(), values[2].Decimal()}
}
func ModelUsage(ctx context.Context, q Reader, keyID int64, model string) (ModelView, error) {
	if keyID <= 0 || !ValidModel(model) {
		return ModelView{}, ErrInvalid
	}
	b, err := readModelBudget(ctx, q, keyID, model)
	if err != nil {
		return ModelView{}, err
	}
	remaining := [3]*db.U128{}
	for i, limit := range b.limits {
		if limit == nil {
			continue
		}
		occupied, err := add(b.used[i], b.reserved[i])
		if err != nil {
			return ModelView{}, err
		}
		n := db.U128{}
		if limit.Big().Cmp(occupied.Big()) > 0 {
			n, err = subtract(*limit, occupied)
			if err != nil {
				return ModelView{}, err
			}
		}
		remaining[i] = &n
	}
	return ModelView{model, modelLimits(b.limits), modelAmounts(b.used), modelAmounts(b.reserved), modelLimits(remaining), b.unattributed.Decimal(), b.created}, nil
}
func ValidateModelLimits(limits ModelLimits) error {
	for _, value := range []*string{limits.Tokens, limits.InputTokens, limits.OutputTokens} {
		if value != nil {
			if _, err := parseMagnitude("tokens", *value); err != nil {
				return err
			}
		}
	}
	return nil
}
func SetModelLimits(ctx context.Context, tx *sql.Tx, keyID, now int64, model string, limits ModelLimits) error {
	if keyID <= 0 || !validNow(now) || !ValidModel(model) {
		return ErrInvalid
	}
	if err := ValidateModelLimits(limits); err != nil {
		return err
	}
	if limits.InputTokens != nil || limits.OutputTokens != nil {
		budget, err := ReadTokenBudget(ctx, tx, keyID)
		if err != nil {
			return err
		}
		if budget.Reservation.Input == nil {
			return ErrInvalid
		}
	}
	var blobs [3]any
	for i, value := range []*string{limits.Tokens, limits.InputTokens, limits.OutputTokens} {
		if value != nil {
			n, _ := parseMagnitude("tokens", *value)
			blobs[i] = db.EncodeU128(n)
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO donation_model_quotas(donation_key_id,upstream_model_id,token_limit_mag,input_token_limit_mag,output_token_limit_mag,created_at)
 VALUES(?,?,?,?,?,?) ON CONFLICT(donation_key_id,upstream_model_id) DO UPDATE SET token_limit_mag=excluded.token_limit_mag,input_token_limit_mag=excluded.input_token_limit_mag,output_token_limit_mag=excluded.output_token_limit_mag`, keyID, model, blobs[0], blobs[1], blobs[2], now)
	return err
}
