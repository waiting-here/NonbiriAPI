package claim

import "context"

func (s *Service) InitializeUsageTotals(ctx context.Context) error {
	at, err := s.nowUnix()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := InitializeUsageTotalsTx(ctx, tx, at); err != nil {
		return err
	}
	return tx.Commit()
}
