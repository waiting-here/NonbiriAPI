package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const preRecurrenceManifestHash = "7751c34ee571203e22d40b7ad7ccbade589473372e54a08b87de2fce77dd17b0"
const runtimeControlsMarker = "\n-- Runtime capability storage"

var recurrenceTableChanges = []struct{ before, after string }{
	{"alignment TEXT CHECK(alignment IS NULL OR alignment IN ('first_success','calendar'))", "alignment TEXT CHECK(alignment IS NULL OR alignment IN ('first_success','calendar','exact_time')),\n anchor_local TEXT CHECK(anchor_local IS NULL OR (typeof(anchor_local)='text' AND length(anchor_local)=19 AND anchor_local GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]'))"},
	{"mode='reset' AND alignment IS NOT NULL AND alignment IN ('first_success','calendar')", "mode='reset' AND alignment IS NOT NULL AND alignment IN ('first_success','calendar','exact_time')"},
	{"NOT (alignment='calendar' AND interval IN ('1h','5h'))", "NOT (alignment IN ('calendar','exact_time') AND interval IN ('1h','5h'))"},
	{"CHECK((window_left IS NULL", "CHECK((alignment IS 'exact_time' AND anchor_local IS NOT NULL) OR (alignment IS NOT 'exact_time' AND anchor_local IS NULL)),\n CHECK((window_left IS NULL"},
}

// preRecurrenceSchema is the exact predecessor DDL, retained through explicit
// inverse declarations so its manifest is independently checked on upgrade.
func preRecurrenceSchema() string {
	previous, _, _ := strings.Cut(generationTwoSchema, runtimeControlsMarker)
	for i := len(recurrenceTableChanges) - 1; i >= 0; i-- {
		change := recurrenceTableChanges[i]
		previous = strings.Replace(previous, change.after, change.before, 1)
	}
	return previous
}

func applyRecurrenceExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preRecurrenceManifestHash {
		return errors.New("unrecognized recurrence source manifest")
	}
	// Rebuild once, preserving columns and dependent objects. Existing rules
	// acquire a NULL anchor and keep their original epochs and accounting.
	var definition string
	if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type='table' AND name='donation_quota_epochs'").Scan(&definition); err != nil {
		return err
	}
	after := definition
	for _, change := range recurrenceTableChanges {
		if strings.Count(after, change.before) != 1 {
			return errors.New("recurrence source constraint mismatch")
		}
		after = strings.Replace(after, change.before, change.after, 1)
	}
	if err := rebuildStorageContractTable(ctx, tx, "donation_quota_epochs", definition, after, 1); err != nil {
		return err
	}
	_, additive, ok := strings.Cut(generationTwoSchema, runtimeControlsMarker)
	if !ok {
		return errors.New("canonical runtime capability storage is missing")
	}
	_, err = tx.ExecContext(ctx, additive)
	return err
}
