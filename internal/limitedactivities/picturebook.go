package limitedactivities

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type pictureBookConfiguration struct{}

func decodeSettings(raw json.RawMessage) (ExchangeSettings, int64, int64, error) {
	var settings ExchangeSettings
	if len(raw) > 65536 || strictJSON(raw, &settings) != nil {
		return settings, 0, 0, ErrInvalid
	}
	return validateSettings(settings)
}

func (pictureBookConfiguration) Normalize(raw json.RawMessage) (json.RawMessage, error) {
	settings, _, _, err := decodeSettings(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(settings)
}

func (pictureBookConfiguration) PublicTx(ctx context.Context, tx *sql.Tx, key string, raw json.RawMessage) (json.RawMessage, error) {
	settings, _, _, err := decodeSettings(raw)
	if err != nil {
		return nil, ErrInvariant
	}
	supply, err := readSupply(ctx, tx, key, settings)
	if err != nil {
		return nil, err
	}
	return json.Marshal(supply)
}

func (pictureBookConfiguration) ApplyTx(ctx context.Context, tx *sql.Tx, key string, raw json.RawMessage) error {
	settings, _, _, err := decodeSettings(raw)
	if err != nil {
		return err
	}
	cap, _ := db.ParseU128Decimal(settings.BrushCap)
	changed, err := tx.ExecContext(ctx, `UPDATE activity_exchange_state SET cap_mag=?,revision=revision+1 WHERE activity_key=? AND asset_type='sketch_brush' AND revision<9223372036854775807`, db.EncodeU128(cap), key)
	if err != nil {
		return err
	}
	return oneRow(changed)
}
