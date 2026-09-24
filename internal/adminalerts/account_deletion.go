package adminalerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type AccountDeletion struct {
	UserID         string `json:"user_id"`
	DiscordID      string `json:"discord_id"`
	GeneralBalance string `json:"general_balance"`
	GameBalance    string `json:"game_balance"`
	DonationCredit string `json:"donation_credit"`
	SketchPaper    string `json:"sketch_paper"`
	SketchBrush    string `json:"sketch_brush"`
}

func points(value *big.Int) string {
	negative := value.Sign() < 0
	absolute := new(big.Int).Abs(value)
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(absolute, big.NewInt(1000), fraction)
	result := whole.String()
	if fraction.Sign() != 0 {
		result += "." + strings.TrimRight(fmt.Sprintf("%03d", fraction.Int64()), "0")
	}
	if negative {
		result = "-" + result
	}
	return result
}

// RecordAccountDeletionTx runs after pending work has been settled and before
// wallet zeroing. It deliberately retains the identity without a user FK.
// Alert creation and account deletion must commit in the same transaction.
func RecordAccountDeletionTx(ctx context.Context, tx *sql.Tx, userID, at int64) error {
	if tx == nil || userID <= 0 || at < 0 || at > maxUnixSecond {
		return ErrInvalidRequest
	}
	snapshot := AccountDeletion{UserID: strconv.FormatInt(userID, 10), SketchPaper: "0", SketchBrush: "0"}
	var donation []byte
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(discord_id,''),donation_credit_mag FROM users WHERE id=? AND is_admin=0`, userID).Scan(&snapshot.DiscordID, &donation); err != nil {
		return err
	}
	if !validAlertText(snapshot.DiscordID, 64) {
		return ErrInvariant
	}
	credit, err := db.DecodeU128(donation)
	if err != nil {
		return err
	}
	snapshot.DonationCredit = points(credit.Big())
	negative := false
	for _, asset := range ledger.Assets() {
		var sign int
		var magnitude []byte
		err := tx.QueryRowContext(ctx, `SELECT balance_sign,balance_mag FROM credit_accounts WHERE user_id=? AND kind='user' AND asset_type=?`, userID, asset).Scan(&sign, &magnitude)
		if err == sql.ErrNoRows && (asset == ledger.SketchPaper || asset == ledger.SketchBrush) {
			continue
		}
		if err != nil {
			return err
		}
		value, err := db.NewSM128(sign, magnitude)
		if err != nil {
			return err
		}
		amount := points(value.Big())
		switch asset {
		case ledger.General:
			snapshot.GeneralBalance = amount
			negative = negative || sign < 0
		case ledger.Game:
			snapshot.GameBalance = amount
			negative = negative || sign < 0
		case ledger.SketchPaper:
			snapshot.SketchPaper = amount
		case ledger.SketchBrush:
			snapshot.SketchBrush = amount
		}
	}
	resolved := 1
	var resolvedAt any = at
	message := "Account deleted by its user."
	if negative {
		resolved = 0
		resolvedAt = nil
		message = "Account deleted with a negative credit balance; review required."
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,created_at,resolved,resolved_at) VALUES('account_deleted',?,?,?,?)`, message, at, resolved, resolvedAt)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO admin_account_deletions(alert_id,snapshot_json) VALUES(?,?)`, id, string(body))
	return err
}

func deletionSnapshot(ctx context.Context, tx *sql.Tx, alertID int64) (*AccountDeletion, error) {
	var body string
	if err := tx.QueryRowContext(ctx, `SELECT snapshot_json FROM admin_account_deletions WHERE alert_id=?`, alertID).Scan(&body); err != nil {
		return nil, err
	}
	var snapshot AccountDeletion
	if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
		return nil, ErrInvariant
	}
	return &snapshot, nil
}
