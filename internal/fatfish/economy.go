package fatfish

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func paymentAccounts(ctx context.Context, tx *sql.Tx, userID int64) (ledger.Account, ledger.Account, error) {
	user, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		return ledger.Account{}, ledger.Account{}, err
	}
	platform, err := ledger.CodedAccount(ctx, tx, "platform")
	if err != nil {
		return ledger.Account{}, ledger.Account{}, err
	}
	if user.Asset != ledger.General || platform.Asset != ledger.General {
		return ledger.Account{}, ledger.Account{}, ErrInvariant
	}
	return user, platform, nil
}

func (s *Service) chargeTx(ctx context.Context, tx *sql.Tx, userID, nowMS int64, kind, receiptKey string, mag []byte) (sql.NullString, error) {
	amount, err := amountFromMag(mag)
	if err != nil {
		return sql.NullString{}, err
	}
	if amount.Sign() < 0 {
		return sql.NullString{}, ErrInvariant
	}
	if amount.IsZero() && kind == "ticket" {
		return sql.NullString{}, nil
	}
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT operation_id FROM fatfish_financial_receipts WHERE receipt_key=? AND kind=? AND amount_mag=?`, receiptKey, kind, mag).Scan(&prior)
	if err == nil {
		return sql.NullString{String: prior, Valid: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, err
	}
	user, platform, err := paymentAccounts(ctx, tx, userID)
	if err != nil {
		return sql.NullString{}, err
	}
	opID, err := db.GenerateOpaqueID("op_")
	if err != nil {
		return sql.NullString{}, err
	}
	meta := ledger.Meta{OperationID: opID, ActorUserID: userID, CreatedAt: nowMS / 1000}
	var plan ledger.Plan
	switch kind {
	case "unlock":
		plan, err = ledger.NewFatFishUnlock(meta, user.ID, platform.ID, amount)
	case "ticket":
		plan, err = ledger.NewFatFishTicket(meta, user.ID, platform.ID, amount)
	default:
		return sql.NullString{}, ErrInvalid
	}
	if err != nil {
		return sql.NullString{}, err
	}
	if _, err = ledger.Apply(ctx, tx, plan); err != nil {
		return sql.NullString{}, err
	}
	if !amount.IsZero() {
		_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_financial_receipts(receipt_key,kind,operation_id,amount_mag,created_at) VALUES(?,?,?,?,?)`, receiptKey, kind, opID, mag, nowMS/1000)
		if err != nil {
			return sql.NullString{}, err
		}
	}
	return sql.NullString{String: opID, Valid: true}, nil
}

func (s *Service) refundTx(ctx context.Context, tx *sql.Tx, c challengeRow, nowMS int64) (sql.NullString, error) {
	amount, err := amountFromMag(c.price)
	if err != nil {
		return sql.NullString{}, err
	}
	if amount.IsZero() {
		return sql.NullString{}, nil
	}
	key := "refund:" + c.id
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT operation_id FROM fatfish_financial_receipts WHERE receipt_key=? AND kind='refund' AND amount_mag=?`, key, c.price).Scan(&prior)
	if err == nil {
		return sql.NullString{String: prior, Valid: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, err
	}
	user, platform, err := paymentAccounts(ctx, tx, c.userID)
	if err != nil {
		return sql.NullString{}, err
	}
	opID, err := db.GenerateOpaqueID("op_")
	if err != nil {
		return sql.NullString{}, err
	}
	plan, err := ledger.NewFatFishRefund(ledger.Meta{OperationID: opID, CreatedAt: nowMS / 1000}, platform.ID, user.ID, amount)
	if err != nil {
		return sql.NullString{}, err
	}
	if _, err = ledger.Apply(ctx, tx, plan); err != nil {
		return sql.NullString{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_financial_receipts(receipt_key,kind,operation_id,amount_mag,created_at) VALUES(?, 'refund',?,?,?)`, key, opID, c.price, nowMS/1000)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: opID, Valid: true}, nil
}

func ticketReceiptKey(challengeID string) string { return "ticket:" + challengeID }
func unlockReceiptKey(userID int64, periodID, nodeID string) string {
	return "unlock:" + strconv.FormatInt(userID, 10) + ":" + periodID + ":" + nodeID
}
