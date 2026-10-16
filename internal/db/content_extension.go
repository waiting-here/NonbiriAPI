package db

import (
	"context"
	"database/sql"
)

// extendContentAndProtocols is the first stable upgrade. Both changes share
// one startup transaction, so cancellation cannot expose a partial schema.
func extendContentAndProtocols(ctx context.Context, tx *sql.Tx) error {
	if err := extendContentProtocols(ctx, tx); err != nil {
		return err
	}
	return migrateFishingPresentations(ctx, tx)
}
