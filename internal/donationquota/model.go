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

type ModelLimits struct {
	Calls        *string `json:"calls"`
	Credits      *string `json:"credits"`
	Tokens       *string `json:"tokens"`
	InputTokens  *string `json:"input_tokens"`
	OutputTokens *string `json:"output_tokens"`
}
type ModelAmounts struct {
	Calls        string `json:"calls"`
	Credits      string `json:"credits"`
	Tokens       string `json:"tokens"`
	InputTokens  string `json:"input_tokens"`
	OutputTokens string `json:"output_tokens"`
}
type ModelView struct {
	HistoricalCostsPartial  bool         `json:"historical_costs_partial"`
	UpstreamModelID         string       `json:"upstream_model_id"`
	Limits                  ModelLimits  `json:"limits"`
	Used                    ModelAmounts `json:"used"`
	Reserved                ModelAmounts `json:"reserved"`
	Remaining               ModelLimits  `json:"remaining"`
	UnattributedTotalTokens string       `json:"unattributed_total_tokens"`
	TrackingStartedAt       *int64       `json:"tracking_started_at"`
}

// The ordered dimensions match the aggregate SQL projection.
var modelMetrics = [...]string{"tokens", "input_tokens", "output_tokens", "calls", "credits"}

type modelBudget struct {
	partial        bool
	limits         [len(modelMetrics)]*db.U128
	used, reserved [len(modelMetrics)]db.U128
	unattributed   db.U128
	created        *int64
}

func modelValues(a Amounts) [len(modelMetrics)]db.U128 {
	return [len(modelMetrics)]db.U128{a.Tokens, a.InputTokens, a.OutputTokens, a.Calls, a.Credits}
}
func readModelBudget(ctx context.Context, q Reader, keyID int64, model string) (modelBudget, error) {
	var b modelBudget
	var limits, used, reserved [len(modelMetrics)][]byte
	var unattributed []byte
	err := q.QueryRowContext(ctx, `SELECT token_limit_mag,input_token_limit_mag,output_token_limit_mag,call_limit_mag,price_limit_mag,
 tokens_used,input_tokens_used,output_tokens_used,calls_used,price_used_mag,tokens_reserved,input_tokens_reserved,output_tokens_reserved,calls_reserved,price_reserved_mag,unattributed_total_tokens,created_at,historical_costs_partial
 FROM donation_model_quotas WHERE donation_key_id=? AND upstream_model_id=?`, keyID, model).
		Scan(&limits[0], &limits[1], &limits[2], &limits[3], &limits[4],
			&used[0], &used[1], &used[2], &used[3], &used[4],
			&reserved[0], &reserved[1], &reserved[2], &reserved[3], &reserved[4],
			&unattributed, &b.created, &b.partial)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	for i := range modelMetrics {
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
func modelLimits(values [len(modelMetrics)]*db.U128) ModelLimits {
	var out [len(modelMetrics)]*string
	for i, n := range values {
		if n != nil {
			value := formatMagnitude(modelMetrics[i], *n)
			out[i] = &value
		}
	}
	return ModelLimits{Tokens: out[0], InputTokens: out[1], OutputTokens: out[2], Calls: out[3], Credits: out[4]}
}
func modelAmounts(values [len(modelMetrics)]db.U128) ModelAmounts {
	return ModelAmounts{Tokens: values[0].Decimal(), InputTokens: values[1].Decimal(), OutputTokens: values[2].Decimal(), Calls: values[3].Decimal(), Credits: formatMagnitude("credits", values[4])}
}
func ModelUsage(ctx context.Context, q Reader, keyID int64, model string) (ModelView, error) {
	if keyID <= 0 || !ValidModel(model) {
		return ModelView{}, ErrInvalid
	}
	b, err := readModelBudget(ctx, q, keyID, model)
	if err != nil {
		return ModelView{}, err
	}
	remaining := [len(modelMetrics)]*db.U128{}
	for i, limit := range b.limits {
		if limit == nil {
			continue
		}
		amount := limit.Big()
		amount.Sub(amount, b.used[i].Big())
		amount.Sub(amount, b.reserved[i].Big())
		n := db.U128{}
		if amount.Sign() > 0 {
			n, err = db.U128FromBig(amount)
			if err != nil {
				return ModelView{}, err
			}
		}
		remaining[i] = &n
	}
	return ModelView{
		UpstreamModelID: model, Limits: modelLimits(b.limits), Used: modelAmounts(b.used),
		Reserved: modelAmounts(b.reserved), Remaining: modelLimits(remaining),
		UnattributedTotalTokens: b.unattributed.Decimal(), TrackingStartedAt: b.created, HistoricalCostsPartial: b.partial,
	}, nil
}
func (limits ModelLimits) values() [len(modelMetrics)]*string {
	return [len(modelMetrics)]*string{limits.Tokens, limits.InputTokens, limits.OutputTokens, limits.Calls, limits.Credits}
}

func ValidateModelLimits(limits ModelLimits) error {
	for i, value := range limits.values() {
		if value != nil {
			if _, err := parseMagnitude(modelMetrics[i], *value); err != nil {
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
	var blobs [len(modelMetrics)]any
	for i, value := range limits.values() {
		if value != nil {
			n, _ := parseMagnitude(modelMetrics[i], *value)
			blobs[i] = db.EncodeU128(n)
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO donation_model_quotas(donation_key_id,upstream_model_id,token_limit_mag,input_token_limit_mag,output_token_limit_mag,call_limit_mag,price_limit_mag,created_at)
 VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(donation_key_id,upstream_model_id) DO UPDATE SET token_limit_mag=excluded.token_limit_mag,input_token_limit_mag=excluded.input_token_limit_mag,output_token_limit_mag=excluded.output_token_limit_mag,call_limit_mag=excluded.call_limit_mag,price_limit_mag=excluded.price_limit_mag`, keyID, model, blobs[0], blobs[1], blobs[2], blobs[3], blobs[4], now)
	return err
}
