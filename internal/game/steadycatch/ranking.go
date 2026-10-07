package steadycatch

import (
	"context"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
)

type RankRow struct {
	Rank       string           `json:"rank"`
	Score      int              `json:"score"`
	AchievedAt int64            `json:"achieved_at"`
	IsMe       bool             `json:"is_me"`
	Identity   ranking.Identity `json:"identity"`
}
type Board struct {
	AsOf   int64     `json:"as_of"`
	Window string    `json:"window"`
	Rows   []RankRow `json:"rows"`
	Me     *RankRow  `json:"me"`
}

func (s *Service) Leaderboard(ctx context.Context, user int64, window string) (Board, error) {
	days := int64(0)
	switch window {
	case "7d":
		days = 7
	case "30d":
		days = 30
	default:
		return Board{}, ErrInvalid
	}
	tx, nowMS, err := s.begin(ctx)
	if err != nil {
		return Board{}, err
	}
	defer tx.Rollback()
	now := nowMS / 1000
	if err = s.authorizer.AuthorizeUserMutation(ctx, tx, user); err != nil {
		return Board{}, err
	}
	out := Board{AsOf: now, Window: window, Rows: []RankRow{}}
	rows, err := tx.QueryContext(ctx, `WITH attempts AS (
 SELECT user_id,score,terminal_at,id,ROW_NUMBER() OVER(PARTITION BY user_id ORDER BY score DESC,terminal_at,id) AS own_rank
 FROM game_catch_sessions WHERE status IN ('completed','failed') AND terminal_at>? AND terminal_at<=?
), ranked AS (
 SELECT a.*,ROW_NUMBER() OVER(ORDER BY score DESC,terminal_at,a.id) AS rank FROM attempts a JOIN users u ON u.id=a.user_id WHERE own_rank=1 AND u.is_admin=0
)
SELECT r.user_id,r.score,r.terminal_at,r.rank,CASE WHEN u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) THEN 0 ELSE COALESCE(p.game_profile_public,u.game_profile_public) END,u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url
FROM ranked r JOIN users u ON u.id=r.user_id LEFT JOIN game_user_preferences p ON p.user_id=u.id WHERE r.rank<=20 OR r.user_id=? ORDER BY r.rank`, now-days*86400, now, now, user)
	if err != nil {
		return Board{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var owner, rank int64
		var public bool
		var name, nick, discord, avatar, guild string
		var row RankRow
		if err = rows.Scan(&owner, &row.Score, &row.AchievedAt, &rank, &public, &name, &nick, &discord, &avatar, &guild); err != nil {
			return Board{}, err
		}
		row.Rank = strconv.FormatInt(rank, 10)
		row.IsMe = owner == user
		row.Identity = ranking.ProfileIdentity(public, name, nick, discord, avatar, guild)
		if rank > 20 {
			out.Me = &row
		} else {
			out.Rows = append(out.Rows, row)
		}
	}
	return out, rows.Err()
}
