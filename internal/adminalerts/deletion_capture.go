package adminalerts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type DeletionPenalty struct {
	State            string  `json:"state"`
	ActiveAtDeletion *bool   `json:"active_at_deletion"`
	Reason           *string `json:"reason"`
	Until            *int64  `json:"until"`
}

// DeletionBefore is captured once, before domain settlement or automatic
// registration blocking can change the account's original penalty state.
type DeletionBefore struct {
	UserID         int64
	DiscordID      string
	RegisteredAt   int64
	EffectiveLevel int
	Ban            DeletionPenalty
	CharityPause   DeletionPenalty
	Source         string
	ActorUserID    *int64
	DecisionNow    int64
}

func CaptureAccountDeletionTx(ctx context.Context, tx *sql.Tx, userID, now int64, source string, actorUserID *int64) (*DeletionBefore, error) {
	if ctx == nil || tx == nil || userID <= 0 || now < 0 || now > maxUnixSecond ||
		(source != "self" && source != "admin" && source != "system") ||
		(actorUserID != nil && *actorUserID <= 0) ||
		(source == "self" && (actorUserID == nil || *actorUserID != userID)) ||
		(source == "admin" && actorUserID == nil) {
		return nil, ErrInvalidRequest
	}
	value := &DeletionBefore{UserID: userID, Source: source, ActorUserID: actorUserID, DecisionNow: now}
	var banned int
	var banReason string
	var banUntil, pauseUntil, manual sql.NullInt64
	var autoLevel int
	var donation []byte
	err := tx.QueryRowContext(ctx, `SELECT discord_id,created_at,auto_level,level,donation_credit_mag,is_banned,banned_reason,banned_until,charity_suspended_until FROM users WHERE id=? AND is_admin=0`, userID).Scan(&value.DiscordID, &value.RegisteredAt, &autoLevel, &manual, &donation, &banned, &banReason, &banUntil, &pauseUntil)
	if err != nil {
		return nil, err
	}
	credit, err := db.DecodeU128(donation)
	if err != nil {
		return nil, err
	}
	var thresholds [5]int64
	for level := 2; level <= 4; level++ {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, fmt.Sprintf("level_threshold_%d_milli", level)).Scan(&raw); err != nil {
			return nil, err
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != raw || n < 0 || n > db.MaxMoneyMilli {
			return nil, ErrInvariant
		}
		thresholds[level] = n
	}
	value.EffectiveLevel = authz.AutomaticLevel(autoLevel, credit, thresholds)
	if manual.Valid {
		value.EffectiveLevel = int(manual.Int64)
	}
	if value.EffectiveLevel < 1 || value.EffectiveLevel > 6 {
		return nil, ErrInvariant
	}
	banActive := banned == 1 && (!banUntil.Valid || banUntil.Int64 > now)
	pauseActive := pauseUntil.Valid && pauseUntil.Int64 > now
	value.Ban = DeletionPenalty{State: "known", ActiveAtDeletion: &banActive}
	if banReason != "" {
		value.Ban.Reason = &banReason
	}
	if banUntil.Valid {
		until := banUntil.Int64
		value.Ban.Until = &until
	}
	value.CharityPause = DeletionPenalty{State: "known", ActiveAtDeletion: &pauseActive}
	if pauseUntil.Valid {
		until := pauseUntil.Int64
		value.CharityPause.Until = &until
	}
	var pauseReason string
	err = tx.QueryRowContext(ctx, `SELECT reason_code FROM abuse_cases WHERE user_id=? AND kind='charity_suspend' AND state='active' AND ends_at=? ORDER BY started_at DESC LIMIT 1`, userID, pauseUntil).Scan(&pauseReason)
	if err == nil {
		value.CharityPause.Reason = &pauseReason
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return value, nil
}
