package ranking

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type Service struct {
	database   *sql.DB
	authorizer resources.FinalTxAuthorizer
	now        func() time.Time
}

func New(database *sql.DB, authorizer resources.FinalTxAuthorizer, now func() time.Time) (*Service, error) {
	if database == nil || authorizer == nil {
		return nil, ErrInvalid
	}
	if now == nil {
		now = time.Now
	}
	return &Service{database, authorizer, now}, nil
}

// Advance commits bounded progress even when the full backlog needs another
// call. Request handlers recheck readiness and authorization in their final
// read snapshot; a settlement in between cannot expose stale totals.
func (s *Service) Advance(ctx context.Context, now int64) (bool, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	ready, err := AdvanceTx(ctx, tx, now)
	if err != nil {
		return false, err
	}
	return ready, tx.Commit()
}

func (s *Service) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		batch, cancel := context.WithTimeout(ctx, 2*time.Second)
		ready, err := s.Advance(batch, s.now().Unix())
		cancel()
		delay := time.Second
		if err == nil && !ready {
			delay = 25 * time.Millisecond
		}
		timer.Reset(delay)
	}
}

func (s *Service) Read(ctx context.Context, user int64, board, window string, page pagination.Request) (Board, error) {
	if user <= 0 || !validBoard(board, window) || board == "charity" && (!page.Valid() || page.Size != 20) {
		return Board{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// The first gate prevents an unauthorized caller from scheduling catch-up.
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Board{}, err
	}
	err = s.authorizer.AuthorizeUserMutation(ctx, tx, user)
	tx.Rollback()
	if err != nil {
		return Board{}, err
	}
	now := s.now().Unix()
	if board != "charity" {
		ready, err := s.Advance(ctx, now)
		if err != nil {
			return Board{}, err
		}
		if !ready {
			return Board{}, ErrCatchingUp
		}
	}
	tx, err = s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Board{}, err
	}
	defer tx.Rollback()
	if err := s.authorizer.AuthorizeUserMutation(ctx, tx, user); err != nil {
		return Board{}, err
	}
	var maintenance int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM maintenance_state WHERE id=1`).Scan(&maintenance); err != nil {
		return Board{}, err
	}
	if maintenance != 0 {
		return Board{}, resources.ErrMaintenance
	}
	// Catch-up can cross a clock second. The final identity read has pinned
	// this snapshot; label it with a fresh time and recheck newly due facts.
	now = s.now().Unix()
	if board != "charity" {
		ready, err := ReadyTx(ctx, tx, now)
		if err != nil {
			return Board{}, err
		}
		if !ready {
			return Board{}, ErrCatchingUp
		}
	}
	return ReadTx(ctx, tx, user, board, window, page, now)
}

func validBoard(board, window string) bool {
	switch board {
	case "charity":
		return window == "history"
	case "game_charity":
		return window == "7d"
	case "bidding", "blackjack":
		return window == "7d" || window == "30d" || window == "history"
	}
	return false
}

// DeleteTx participates in the account coordinator's transaction, before
// retiring the ledger owner. Late settlements cannot recreate a deleted FK.
func DeleteTx(ctx context.Context, tx *sql.Tx, user int64) error {
	for _, table := range []string{"game_rank_expiry_work", "game_rank_events", "game_rank_totals"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id=?`, user); err != nil {
			return err
		}
	}
	return nil
}
