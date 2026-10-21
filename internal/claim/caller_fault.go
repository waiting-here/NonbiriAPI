package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// recordCallerFaultTx joins the terminal accounting transaction. The receipt
// contains no caller input and has the same lifetime as its claim.
func recordCallerFaultTx(ctx context.Context, tx *sql.Tx, record claimRecord, outcome AttemptOutcome) error {
	var status int
	var code string
	err := tx.QueryRowContext(ctx, `SELECT http_status,error_code FROM dispatch_caller_parameter_errors WHERE claim_id=?`, record.claimID).Scan(&status, &code)
	if err == nil {
		if status != outcome.UpstreamStatus || code != outcome.UpstreamCode {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	switch record.state {
	case StateClaimed:
		return ErrNotDispatched
	case StateReleased:
		return ErrTerminal
	case StateDispatched:
	default:
		return ErrConflict
	}
	started, err := responseStartedTx(ctx, tx, record.claimID)
	if err != nil {
		return err
	}
	if started {
		return ErrInvalidInput
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dispatch_caller_parameter_errors(claim_id,http_status,error_code) VALUES(?,?,?)`, record.claimID, outcome.UpstreamStatus, outcome.UpstreamCode)
	if err != nil {
		return fmt.Errorf("claim: record caller parameter error: %w", err)
	}
	return nil
}
