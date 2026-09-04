package rps

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

type expiringRankFact struct {
	SessionID  string
	UserID     int64
	Mode       string
	ExpiresAt  int64
	Sign       int
	Magnitude  db.U128
	Profitable int
}

func (service *Service) expireRankFacts(ctx context.Context, queryNow int64) (int, error) {
	total := 0
	for {
		tx, err := service.database.BeginTx(ctx, nil)
		if err != nil {
			return total, classifyDB(err)
		}
		count, err := service.expireRankFactsBatchTx(ctx, tx, queryNow)
		if err != nil {
			_ = tx.Rollback()
			return total, err
		}
		if err := tx.Commit(); err != nil {
			return total, classifyDB(err)
		}
		total += count
		if count < workerBatchSize {
			return total, nil
		}
	}
}

func (service *Service) expireRankFactsBatchTx(ctx context.Context, tx *sql.Tx, queryNow int64) (int, error) {
	return service.expireRankFactsBatchTxLimit(ctx, tx, queryNow, workerBatchSize)
}

func (service *Service) expireRankFactsBatchTxLimit(ctx context.Context, tx *sql.Tx, queryNow int64, limit int) (int, error) {
	if service == nil || ctx == nil || tx == nil || queryNow < 0 || queryNow > 253402300799 || limit < 1 || limit > workerBatchSize {
		return 0, ErrInvalidRequest
	}
	rows, err := tx.QueryContext(ctx, `SELECT session_id_text,user_id,mode,expires_at,wallet_net_sign,wallet_net_mag,profitable
	FROM game_rps_rank_facts WHERE aggregate_applied=1 AND expires_at<=?
	ORDER BY expires_at,session_id_text,user_id LIMIT ?`, queryNow, limit)
	if err != nil {
		return 0, classifyDB(err)
	}
	facts := make([]expiringRankFact, 0, limit)
	for rows.Next() {
		var fact expiringRankFact
		var raw []byte
		if err := rows.Scan(&fact.SessionID, &fact.UserID, &fact.Mode, &fact.ExpiresAt, &fact.Sign, &raw, &fact.Profitable); err != nil {
			_ = rows.Close()
			return 0, classifyDB(err)
		}
		fact.Magnitude, err = db.DecodeU128(raw)
		if err != nil || fact.UserID <= 0 || !db.ValidateOpaqueID(fact.SessionID, "rps_") || game.ResolveMode(game.RPSID, fact.Mode) != nil ||
			fact.Sign < -1 || fact.Sign > 1 || fact.Profitable < 0 || fact.Profitable > 1 || (fact.Sign > 0) != (fact.Profitable == 1) {
			_ = rows.Close()
			return 0, ErrInvariant
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return 0, classifyDB(err)
	}
	for _, fact := range facts {
		if err := service.expireOneRankFactTx(ctx, tx, fact); err != nil {
			return 0, err
		}
	}
	return len(facts), nil
}

func (service *Service) expireOneRankFactTx(ctx context.Context, tx *sql.Tx, fact expiringRankFact) error {
	aggregate, found, err := loadRankAggregate(ctx, tx, fact.UserID, fact.Mode)
	if err != nil || !found {
		if err == nil {
			err = ErrInvariant
		}
		return err
	}
	if aggregate.SessionCount.Big().Sign() <= 0 || aggregate.ProfitableCount.Big().Cmp(big.NewInt(int64(fact.Profitable))) < 0 {
		return ErrInvariant
	}
	removeSign := -fact.Sign
	netSign, netMagnitude, err := signedAdd(aggregate.NetSign, aggregate.NetMag.Big(), removeSign, fact.Magnitude.Big())
	if err != nil {
		return err
	}
	if aggregate.SessionCount.Big().Cmp(bigOne) == 0 {
		if aggregate.ProfitableCount.Big().Cmp(big.NewInt(int64(fact.Profitable))) != 0 || netSign != 0 || netMagnitude.Big().Sign() != 0 {
			return ErrInvariant
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM game_rps_rank_aggregates WHERE user_id=? AND mode=? AND revision=?`,
			fact.UserID, fact.Mode, db.EncodeU128(aggregate.Revision))
		if err != nil {
			return classifyDB(err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			if err != nil {
				return classifyDB(err)
			}
			return ErrConflict
		}
	} else {
		sessions, err := u128(new(big.Int).Sub(aggregate.SessionCount.Big(), bigOne))
		if err != nil {
			return err
		}
		profitable, err := u128(new(big.Int).Sub(aggregate.ProfitableCount.Big(), big.NewInt(int64(fact.Profitable))))
		if err != nil {
			return err
		}
		rate, err := profitRate(sessions.Big(), profitable.Big())
		if err != nil {
			return err
		}
		profitAchieved, netAchieved := aggregate.ProfitAchieved, aggregate.NetAchieved
		if rate != aggregate.ProfitRate {
			profitAchieved = fact.ExpiresAt
		}
		if netSign != aggregate.NetSign || netMagnitude != aggregate.NetMag {
			netAchieved = fact.ExpiresAt
		}
		revision, err := incU128(aggregate.Revision)
		if err != nil {
			return err
		}
		eligible := 0
		if sessions.Big().Cmp(big.NewInt(10)) >= 0 {
			eligible = 1
		}
		result, err := tx.ExecContext(ctx, `UPDATE game_rps_rank_aggregates SET
session_count=?,profitable_count=?,net_profit_sign=?,net_profit_mag=?,eligible=?,profit_rate_bp=?,
profit_rate_achieved_at=?,net_profit_achieved_at=?,revision=?,updated_at=?
WHERE user_id=? AND mode=? AND revision=?`, db.EncodeU128(sessions), db.EncodeU128(profitable), netSign,
			db.EncodeU128(netMagnitude), eligible, rate, profitAchieved, netAchieved, db.EncodeU128(revision), fact.ExpiresAt,
			fact.UserID, fact.Mode, db.EncodeU128(aggregate.Revision))
		if err != nil {
			return classifyDB(err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			if err != nil {
				return classifyDB(err)
			}
			return ErrConflict
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM game_rps_rank_facts
WHERE session_id_text=? AND user_id=? AND aggregate_applied=1`, fact.SessionID, fact.UserID)
	if err != nil {
		return classifyDB(err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return classifyDB(err)
		}
		return ErrConflict
	}
	return nil
}

func (service *Service) Leaderboard(ctx context.Context, userID int64, mode, board string) (Leaderboard, error) {
	if service == nil || service.closed.Load() || userID <= 0 || game.ResolveMode(game.RPSID, mode) != nil ||
		(board != "profit_rate" && board != "net_profit") {
		return Leaderboard{}, ErrInvalidRequest
	}
	queryNow, err := service.decisionNow()
	if err != nil {
		return Leaderboard{}, err
	}
	catchupCtx, cancelCatchup := context.WithTimeout(ctx, 2*time.Second)
	_, err = service.expireRankFacts(catchupCtx, queryNow)
	cancelCatchup()
	if errors.Is(err, context.DeadlineExceeded) {
		return Leaderboard{}, ErrServiceUnavailable
	}
	if err != nil {
		return Leaderboard{}, err
	}
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Leaderboard{}, classifyDB(err)
	}
	defer tx.Rollback()
	if err := service.userAuthorizer.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return Leaderboard{}, mapAuthorization(err)
	}
	if enabled, err := maintenanceEnabled(ctx, tx); err != nil {
		return Leaderboard{}, err
	} else if enabled {
		return Leaderboard{}, ErrMaintenance
	}
	return queryLeaderboardTx(ctx, tx, userID, mode, board, queryNow)
}

type leaderboardIdentity struct {
	UserID                                              int64
	Public                                              bool
	Username, GuildNick, DiscordID, Avatar, GuildAvatar string
}

func queryLeaderboardTx(ctx context.Context, tx *sql.Tx, userID int64, mode, board string, queryNow int64) (Leaderboard, error) {
	order := `a.profit_rate_bp DESC,a.profit_rate_achieved_at ASC,a.profit_public_tie_key ASC`
	if board == "net_profit" {
		order = `a.net_profit_sign DESC,
CASE WHEN a.net_profit_sign=1 THEN a.net_profit_mag END DESC,
CASE WHEN a.net_profit_sign=-1 THEN a.net_profit_mag END ASC,
a.net_profit_achieved_at ASC,a.net_public_tie_key ASC`
	}
	rows, err := tx.QueryContext(ctx, `WITH ranked AS (
SELECT a.user_id,a.session_count,a.profitable_count,a.net_profit_sign,a.net_profit_mag,a.profit_rate_bp,
ROW_NUMBER() OVER(ORDER BY `+order+`) AS rank,
COALESCE(p.game_profile_public,u.game_profile_public) AS game_profile_public,
u.username,u.guild_nick,COALESCE(u.discord_id,'') AS discord_id,u.avatar,u.guild_avatar_url
FROM game_rps_rank_aggregates a JOIN users u ON u.id=a.user_id
LEFT JOIN game_user_preferences p ON p.user_id=u.id
WHERE a.mode=? AND a.eligible=1 AND u.is_admin=0
AND (u.is_banned=0 OR (u.banned_until IS NOT NULL AND u.banned_until<=?)))
SELECT user_id,session_count,profitable_count,net_profit_sign,net_profit_mag,profit_rate_bp,rank,
game_profile_public,username,guild_nick,discord_id,avatar,guild_avatar_url
FROM ranked WHERE rank<=20 OR user_id=? ORDER BY rank`, mode, queryNow, userID)
	if err != nil {
		return Leaderboard{}, classifyDB(err)
	}
	defer rows.Close()
	windowStart := queryNow - summaryWindowSeconds
	if windowStart < 0 {
		windowStart = 0
	}
	result := Leaderboard{Mode: mode, Board: board, WindowDays: 30, WindowStart: windowStart, MinSessions: 10, Rows: make([]LeaderboardRow, 0, 20)}
	for rows.Next() {
		var identity leaderboardIdentity
		var sessionsRaw, profitableRaw, netRaw []byte
		var netSign, rate, public int
		var rank int64
		if err := rows.Scan(&identity.UserID, &sessionsRaw, &profitableRaw, &netSign, &netRaw, &rate, &rank,
			&public, &identity.Username, &identity.GuildNick, &identity.DiscordID, &identity.Avatar, &identity.GuildAvatar); err != nil {
			return Leaderboard{}, classifyDB(err)
		}
		sessions, err := db.DecodeU128(sessionsRaw)
		if err != nil {
			return Leaderboard{}, ErrInvariant
		}
		profitable, err := db.DecodeU128(profitableRaw)
		if err != nil {
			return Leaderboard{}, ErrInvariant
		}
		netMagnitude, err := db.DecodeU128(netRaw)
		if err != nil || sessions.Big().Cmp(big.NewInt(10)) < 0 || profitable.Big().Cmp(sessions.Big()) > 0 ||
			netSign < -1 || netSign > 1 || rate < 0 || rate > 10000 || rank <= 0 {
			return Leaderboard{}, ErrInvariant
		}
		identity.Public = public == 1
		row := LeaderboardRow{Board: board, Rank: strconv.FormatInt(rank, 10), Identity: projectLeaderboardIdentity(identity),
			SessionCount: sessions.Decimal(), ProfitableCount: profitable.Decimal(), ProfitRate: formatPercentBP(rate),
			NetProfit: formatSignedMilli(netSign, netMagnitude.Big()), IsMe: identity.UserID == userID}
		if rank <= 20 {
			result.Rows = append(result.Rows, row)
		} else if row.IsMe {
			copy := row
			result.Me = &copy
		}
	}
	if err := rows.Err(); err != nil {
		return Leaderboard{}, classifyDB(err)
	}
	return result, nil
}

func formatSignedMilli(sign int, magnitude *big.Int) string {
	if sign == 0 || magnitude == nil || magnitude.Sign() == 0 {
		return "0"
	}
	formatted := formatMilli(magnitude)
	if sign < 0 {
		return "-" + formatted
	}
	return formatted
}

func projectLeaderboardIdentity(value leaderboardIdentity) Identity {
	if !value.Public {
		return Identity{Kind: "anonymous"}
	}
	display := value.GuildNick
	if display == "" {
		display = value.Username
	}
	display = safeDisplayName(display)
	if display == "" {
		return Identity{Kind: "anonymous"}
	}
	avatar := safeAvatar(value.GuildAvatar)
	if avatar == nil && safePathAtom(value.DiscordID) && safePathAtom(value.Avatar) && value.DiscordID != "" && value.Avatar != "" {
		candidate := "https://cdn.discordapp.com/avatars/" + value.DiscordID + "/" + value.Avatar + ".png"
		avatar = &candidate
	}
	return Identity{Kind: "public", DisplayName: display, AvatarURL: avatar}
}
