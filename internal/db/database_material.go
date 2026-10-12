package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func createDatabaseMaterial(ctx context.Context, tx *sql.Tx, codec secret.GenerationTwoContextCodec) error {
	envelope, err := codec.CreateDatabaseMaterial()
	if err != nil {
		return err
	}
	defer clear(envelope)
	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `INSERT INTO donation_review_material(id,version,envelope,created_at,updated_at) VALUES(1,1,?,?,?)`, envelope, now, now)
	return err
}

func validateDatabaseMaterial(ctx context.Context, q queryer, codec secret.GenerationTwoContextCodec) error {
	var version int
	var envelope []byte
	if err := q.QueryRowContext(ctx, `SELECT version,envelope FROM donation_review_material WHERE id=1`).Scan(&version, &envelope); err != nil || version != 1 {
		return secret.ErrReviewMaterial
	}
	defer clear(envelope)
	return codec.ValidateDatabaseMaterial(envelope)
}
