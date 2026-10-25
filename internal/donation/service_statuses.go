package donation

import (
	"context"
	"database/sql"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/donationstate"
)

// Numbered key reads already have an authorized, bounded ID selection.
func readDonationKeySelectionWithStatusesTx(ctx context.Context, tx *sql.Tx, donationID int64, status string, now int64, selectedIDs []int64) ([]AdminDonationKey, error) {
	statuses, err := donationstate.LoadServiceStatuses(ctx, tx, selectedIDs, now)
	if err != nil {
		return nil, err
	}
	return readDonationKeySelectionTx(ctx, tx, donationID, status, now, selectedIDs, statuses)
}

// Collection IDs were authorized by the caller in this transaction. Preload
// health for the collection rather than adding a query to each detail read.
func readDonationServiceStatusesTx(ctx context.Context, tx *sql.Tx, donationIDs []int64, now int64) (map[int64][]donationstate.ServiceStatus, error) {
	if len(donationIDs) == 0 {
		return map[int64][]donationstate.ServiceStatus{}, nil
	}
	ids := []int64{}
	const batchSize = 100
	for start := 0; start < len(donationIDs); start += batchSize {
		batch := donationIDs[start:min(start+batchSize, len(donationIDs))]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM donation_keys WHERE donation_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")+`) ORDER BY id`, args...)
		if err != nil {
			return nil, err
		}
		selected, err := scanIDs(rows)
		if err != nil {
			return nil, err
		}
		ids = append(ids, selected...)
	}
	return donationstate.LoadServiceStatuses(ctx, tx, ids, now)
}
