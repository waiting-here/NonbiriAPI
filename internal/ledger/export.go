package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const MaxUserExportEntries = 10_000

var (
	ErrInvalidExport  = errors.New("ledger: invalid export request")
	ErrExportTooLarge = errors.New("ledger: export exceeds entry limit")
)

// UserExportEntry is the minimal account-owner projection of one immutable
// ledger line. It deliberately omits actor, reason, donation-credit metadata,
// pool balances, account identity, and line-level post-balance internals.
type UserExportEntry struct {
	OperationID string
	Kind        Kind
	SourceType  string
	SourceID    string
	Delta       string
	CreatedAt   int64
}

// ExportUserEntries reads only entries attached to userID's current wallet in
// the supplied transaction. Delta is an exact display-credit decimal (1,000
// milli-credits per credit), never a narrowed integer or floating-point value.
func ExportUserEntries(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	limit int,
) ([]UserExportEntry, error) {
	if ctx == nil || tx == nil || userID <= 0 || limit <= 0 || limit > MaxUserExportEntries {
		return nil, ErrInvalidExport
	}
	wallet, err := UserAccount(ctx, tx, userID)
	if err != nil {
		return nil, fmt.Errorf("ledger: read export wallet: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT
o.id,o.kind,o.source_type,o.source_id,o.source_seq,o.created_at,
e.delta_sign,e.delta_mag
FROM credit_entries e
JOIN credit_operations o ON o.id=e.operation_id
WHERE e.account_id=? AND e.account_kind_snapshot='user'
ORDER BY o.ledger_seq,e.line_no
LIMIT ?`, wallet.ID, limit+1)
	if err != nil {
		return nil, classifySQLError("export user ledger", err)
	}
	defer rows.Close()

	entries := make([]UserExportEntry, 0, limit)
	for rows.Next() {
		var (
			entry     UserExportEntry
			kindText  string
			sourceSeq []byte
			deltaSign int
			deltaMag  []byte
		)
		if err := rows.Scan(
			&entry.OperationID,
			&kindText,
			&entry.SourceType,
			&entry.SourceID,
			&sourceSeq,
			&entry.CreatedAt,
			&deltaSign,
			&deltaMag,
		); err != nil {
			return nil, classifySQLError("scan user ledger export", err)
		}
		entry.Kind = Kind(kindText)
		if !validExportOperation(entry, sourceSeq) {
			clear(sourceSeq)
			clear(deltaMag)
			return nil, ErrInvariant
		}
		amount, err := amountFromParts(deltaSign, deltaMag)
		clear(sourceSeq)
		clear(deltaMag)
		if err != nil {
			return nil, err
		}
		entry.Delta = formatDisplayCredits(amount.Big())
		entries = append(entries, entry)
		if len(entries) > limit {
			return nil, ErrExportTooLarge
		}
	}
	if err := rows.Err(); err != nil {
		return nil, classifySQLError("iterate user ledger export", err)
	}
	return entries, nil
}

func validExportOperation(entry UserExportEntry, sourceSeqRaw []byte) bool {
	if !db.ValidateOpaqueID(entry.OperationID, "op_") || !validUnix(entry.CreatedAt) {
		return false
	}
	wantSource, ok := sourceTypeForKind(entry.Kind)
	if !ok || entry.SourceType != string(wantSource) {
		return false
	}
	prefix := map[sourceType]string{
		sourceOperation:       "op_",
		sourceLogicalRequest:  "req_",
		sourceDispatchClaim:   "clm_",
		sourcePeriod:          "thu_",
		sourceFishingBatch:    "fb_",
		sourceLinkLinkSession: "ll_",
		sourceRPSQueue:        "rpsq_",
		sourceRPSSession:      "rps_",
	}[wantSource]
	if prefix == "" || !db.ValidateOpaqueID(entry.SourceID, prefix) ||
		wantSource == sourceOperation && entry.SourceID != entry.OperationID {
		return false
	}
	sequence, err := db.DecodeU128(sourceSeqRaw)
	if err != nil {
		return false
	}
	if entry.Kind == KindRPSRoundCut {
		return sequence.Big().Sign() > 0
	}
	return sequence.Big().Sign() == 0
}

func formatDisplayCredits(value *big.Int) string {
	if value == nil || value.Sign() == 0 {
		return "0"
	}
	negative := value.Sign() < 0
	absolute := new(big.Int).Abs(new(big.Int).Set(value))
	whole := new(big.Int)
	remainder := new(big.Int)
	whole.QuoRem(absolute, big.NewInt(1_000), remainder)
	out := whole.String()
	if remainder.Sign() != 0 {
		fraction := fmt.Sprintf("%03d", remainder.Int64())
		out += "." + strings.TrimRight(fraction, "0")
	}
	if negative {
		return "-" + out
	}
	return out
}
