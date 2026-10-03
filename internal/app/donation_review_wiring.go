package app

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func initializeDonationReview(ctx context.Context, database *sql.DB, vault *secret.Vault) (*secret.DonationReview, error) {
	review, err := secret.NewDonationReview(vault)
	if err != nil {
		return nil, err
	}
	if err := review.Initialize(ctx, database); err != nil {
		return nil, err
	}
	var after int64
	for {
		next, count, err := backfillDonationReviewBatch(ctx, database, review, after)
		if err != nil {
			return nil, err
		}
		if count < 1000 {
			return review, nil
		}
		after = next
	}
}

func backfillDonationReviewBatch(ctx context.Context, database *sql.DB, review *secret.DonationReview, after int64) (int64, int, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return after, 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM endpoint_key_secrets
WHERE id>? AND key_body_review_hmac IS NULL ORDER BY id LIMIT 1000`, after)
	if err != nil {
		return after, 0, err
	}
	ids := make([]int64, 0, 1000)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return after, 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil || len(ids) == 0 {
		return after, 0, err
	}
	if err := review.BackfillLegacySecrets(ctx, tx, ids, time.Now()); err != nil {
		return after, 0, err
	}
	if err := tx.Commit(); err != nil {
		return after, 0, err
	}
	return ids[len(ids)-1], len(ids), nil
}
