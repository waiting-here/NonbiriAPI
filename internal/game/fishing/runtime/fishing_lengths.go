package runtime

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

type fishingLengthCandidate struct {
	ordinal int
	species string
	tier    string
	size    int
	length  *string
	kind    *string
}

func (value fishingLengthCandidate) displayLength() string {
	if value.length != nil {
		return *value.length
	}
	return strconv.Itoa(value.size)
}

func batchLengthMaximum(ctx context.Context, tx *sql.Tx, record batchRecord) (fishingLengthCandidate, error) {
	var value fishingLengthCandidate
	var count int
	err := tx.QueryRowContext(ctx, `SELECT o.ordinal,o.species_key,o.tier,o.size_cm,l.length_cm,l.kind,COUNT(*) OVER()
FROM game_fishing_outcomes o LEFT JOIN game_fishing_outcome_lengths l ON l.batch_id=o.batch_id AND l.ordinal=o.ordinal
WHERE o.batch_id=? ORDER BY length(COALESCE(l.length_cm,CAST(o.size_cm AS TEXT))) DESC,
COALESCE(l.length_cm,CAST(o.size_cm AS TEXT)) COLLATE BINARY DESC,o.ordinal ASC LIMIT 1`, record.ID).
		Scan(&value.ordinal, &value.species, &value.tier, &value.size, &value.length, &value.kind, &count)
	if err != nil {
		return value, classifyDB(err)
	}
	if count != record.Count || value.length != nil && (value.tier != string(fishing.TierLegend) || !fishing.ValidPresentationLength(*value.length) || value.kind == nil || *value.kind != "blue" && *value.kind != "milk") {
		return value, ErrInvariant
	}
	return value, nil
}

func applyLengthFact(ctx context.Context, tx *sql.Tx, record batchRecord) error {
	value, err := batchLengthMaximum(ctx, tx, record)
	if err != nil {
		return err
	}
	var blue, milk *string
	if value.kind != nil && *value.kind == "blue" {
		blue = value.length
	}
	if value.kind != nil && *value.kind == "milk" {
		milk = value.length
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_fishing_length_facts(batch_id_text,ordinal,species_key,tier,size_cm,caught_at,blue_fat_fish_length_cm,milk_crab_length_cm) VALUES(?,?,?,?,?,?,?,?)`,
		record.ID, value.ordinal, value.species, value.tier, value.size, record.CreatedAt, blue, milk)
	return classifyDB(err)
}
