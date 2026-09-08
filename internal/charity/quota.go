package charity

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func (s *Service) ValidateRecurringState(ctx context.Context) error {
	if s == nil || s.db == nil || ctx == nil {
		return claim.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return donationquota.ValidateState(ctx, tx)
}
