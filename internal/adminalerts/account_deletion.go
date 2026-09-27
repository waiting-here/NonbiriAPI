package adminalerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/blacklist"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type AccountDeletion struct {
	SnapshotVersion      int             `json:"snapshot_version"`
	RegisteredAt         *int64          `json:"registered_at"`
	DeletedAt            *int64          `json:"deleted_at"`
	EffectiveLevel       *int            `json:"effective_level"`
	Ban                  DeletionPenalty `json:"ban"`
	CharityPause         DeletionPenalty `json:"charity_pause"`
	Source               string          `json:"source"`
	ActorUserID          *string         `json:"actor_user_id"`
	BlacklistAction      string          `json:"blacklist_action"`
	BlacklistReasonCodes []string        `json:"blacklist_reason_codes"`
	UserID               string          `json:"user_id"`
	DiscordID            string          `json:"discord_id"`
	GeneralBalance       string          `json:"general_balance"`
	GameBalance          string          `json:"game_balance"`
	DonationCredit       string          `json:"donation_credit"`
	SketchPaper          string          `json:"sketch_paper"`
	SketchBrush          string          `json:"sketch_brush"`
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
func RecordAccountDeletionTx(ctx context.Context, tx *sql.Tx, before *DeletionBefore, operationID string) error {
	if tx == nil || before == nil || before.UserID <= 0 || before.DecisionNow < 0 || before.DecisionNow > maxUnixSecond ||
		!db.ValidateOpaqueID(operationID, "op_") || before.Ban.ActiveAtDeletion == nil || before.CharityPause.ActiveAtDeletion == nil ||
		(before.Source != "self" && before.Source != "admin" && before.Source != "system") {
		return ErrInvalidRequest
	}
	userID, at := before.UserID, before.DecisionNow
	snapshot := AccountDeletion{UserID: strconv.FormatInt(userID, 10), SketchPaper: "0", SketchBrush: "0"}
	snapshot.SnapshotVersion = 2
	snapshot.RegisteredAt = &before.RegisteredAt
	snapshot.DeletedAt = &before.DecisionNow
	snapshot.EffectiveLevel = &before.EffectiveLevel
	snapshot.Ban, snapshot.CharityPause = before.Ban, before.CharityPause
	snapshot.Source, snapshot.BlacklistAction = before.Source, "none"
	snapshot.BlacklistReasonCodes = []string{}
	if before.ActorUserID != nil {
		actor := strconv.FormatInt(*before.ActorUserID, 10)
		snapshot.ActorUserID = &actor
	}
	var donation []byte
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(discord_id,''),donation_credit_mag FROM users WHERE id=? AND is_admin=0`, userID).Scan(&snapshot.DiscordID, &donation); err != nil {
		return err
	}
	if !validAlertText(snapshot.DiscordID, 128) || snapshot.DiscordID != before.DiscordID {
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
	if before.Source == "self" {
		if *before.Ban.ActiveAtDeletion || *before.CharityPause.ActiveAtDeletion {
			snapshot.BlacklistReasonCodes = append(snapshot.BlacklistReasonCodes, blacklist.PenaltyEvasion)
		}
		if negative {
			snapshot.BlacklistReasonCodes = append(snapshot.BlacklistReasonCodes, blacklist.DebtEvasion)
		}
		if len(snapshot.BlacklistReasonCodes) > 0 {
			// The registration guard requires any still-present account to be
			// permanently blocked first. Original penalties stay in the snapshot.
			if _, err := tx.ExecContext(ctx, `UPDATE users SET is_banned=1,banned_until=NULL WHERE id=? AND is_admin=0`, userID); err != nil {
				return err
			}
			note := "Self-deletion while subject to an active penalty."
			if negative {
				note = "Self-deletion with outstanding credit debt."
			}
			if negative && len(snapshot.BlacklistReasonCodes) == 2 {
				note = "Self-deletion while subject to an active penalty and with outstanding credit debt."
			}
			action, err := blacklist.AppendTx(ctx, tx, blacklist.Event{DiscordID: snapshot.DiscordID, OperationKey: "account-delete:" + snapshot.UserID + ":" + operationID, ActorKind: blacklist.Automatic, ReasonCodes: snapshot.BlacklistReasonCodes, Note: note, At: at})
			if err != nil {
				return err
			}
			snapshot.BlacklistAction = action
		}
	}
	resolved := 1
	var resolvedAt any = at
	resolutionKind := ""
	message := "Account deleted."
	if negative {
		resolved = 0
		resolvedAt = nil
		message = "Account deleted with a negative credit balance; review required."
	}
	if snapshot.BlacklistAction == "added" || snapshot.BlacklistAction == "appended" {
		resolved, resolvedAt, resolutionKind = 1, at, "automatic_blacklist"
		message = "Account deleted; its Discord identity was automatically added to the registration blacklist."
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,created_at,resolved,resolved_at,resolution_kind) VALUES('account_deleted',?,?,?,?,?)`, message, at, resolved, resolvedAt, resolutionKind)
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
	_, err = tx.ExecContext(ctx, `INSERT INTO admin_account_deletions(alert_id,snapshot_json,snapshot_version,former_user_id,discord_id,registered_at,deleted_at,effective_level,source,actor_user_id,ban_active,pause_active,blacklist_action) VALUES(?,?,2,?,?,?,?,?,?,?,?,?,?)`, id, string(body), userID, before.DiscordID, before.RegisteredAt, at, before.EffectiveLevel, before.Source, before.ActorUserID, *before.Ban.ActiveAtDeletion, *before.CharityPause.ActiveAtDeletion, snapshot.BlacklistAction)
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
	if snapshot.SnapshotVersion == 0 {
		snapshot.SnapshotVersion = 1
	}
	if snapshot.Source == "" {
		snapshot.Source = "unknown"
	}
	if snapshot.BlacklistAction == "" {
		snapshot.BlacklistAction = "unknown"
	}
	if snapshot.Ban.State == "" {
		snapshot.Ban.State = "unknown"
	}
	if snapshot.CharityPause.State == "" {
		snapshot.CharityPause.State = "unknown"
	}
	if snapshot.BlacklistReasonCodes == nil {
		snapshot.BlacklistReasonCodes = []string{}
	}
	return &snapshot, nil
}
