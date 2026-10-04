package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const preLedgerRetentionManifestHash = "e2f99944f596dda1702a76de6c7ba540761bd1d27f748c437968df0ecc1cf452"
const ledgerRetentionMarker = "\n-- Ledger detail retention\n"

func preLedgerRetentionSchema() string {
	previous, _, _ := strings.Cut(generationTwoSchema, ledgerRetentionMarker)
	return previous
}

func applyLedgerRetentionExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preLedgerRetentionManifestHash {
		return errors.New("unrecognized ledger retention source manifest")
	}
	_, additive, ok := strings.Cut(generationTwoSchema, ledgerRetentionMarker)
	if !ok {
		return errors.New("canonical ledger retention schema is missing")
	}
	_, err = tx.ExecContext(ctx, additive)
	return err
}

// LedgerCompactionState also supports the historical schemas validated within
// an upgrade, before the retention extension has been applied.
func LedgerCompactionState(ctx context.Context, tx *sql.Tx) (through, before int64, present bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='credit_compaction')`).Scan(&present)
	if err != nil || !present {
		return
	}
	err = tx.QueryRowContext(ctx, `SELECT through_seq,details_before FROM credit_compaction WHERE id=1`).Scan(&through, &before)
	return
}
