package app

import (
	"context"
	"database/sql"

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
	return review, nil
}
