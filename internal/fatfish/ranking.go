package fatfish

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type PublicIdentity struct {
	Kind        string  `json:"kind"`
	DisplayName string  `json:"display_name,omitempty"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
}
type LeaderboardRow struct {
	Rank         string         `json:"rank"`
	ScoreUnits   string         `json:"score_units"`
	AchievedAtMS int64          `json:"achieved_at_ms"`
	IsMe         bool           `json:"is_me"`
	Identity     PublicIdentity `json:"identity"`
}
type Leaderboard struct {
	PeriodID string           `json:"period_id"`
	NodeID   string           `json:"node_id,omitempty"`
	Final    bool             `json:"final"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Total    int64            `json:"total"`
	Rows     []LeaderboardRow `json:"rows"`
}

func boardIdentity(public bool, username, nick, discord, avatar, guild string) PublicIdentity {
	if !public {
		return PublicIdentity{Kind: "anonymous"}
	}
	if strings.TrimSpace(nick) == "" {
		nick = username
	}
	if !utf8.ValidString(nick) {
		return PublicIdentity{Kind: "anonymous"}
	}
	runes := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(nick)))
	if len(runes) > 128 {
		runes = runes[:128]
	}
	if len(runes) == 0 {
		return PublicIdentity{Kind: "anonymous"}
	}
	i := PublicIdentity{Kind: "public", DisplayName: string(runes)}
	if parsed, err := url.Parse(guild); err == nil && len(guild) <= 2048 && parsed.Scheme == "https" && parsed.User == nil && parsed.Port() == "" && (parsed.Hostname() == "cdn.discordapp.com" || parsed.Hostname() == "media.discordapp.net") {
		i.AvatarURL = &guild
	} else if safeAvatarAtom(discord) && safeAvatarAtom(avatar) {
		v := "https://cdn.discordapp.com/avatars/" + discord + "/" + avatar + ".png"
		i.AvatarURL = &v
	}
	return i
}
func safeAvatarAtom(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func (s *Service) Leaderboard(ctx context.Context, userID int64, periodID, nodeID string, page, pageSize int) (Leaderboard, error) {
	if !db.ValidateOpaqueID(periodID, "ffp_") || nodeID != "" && !db.ValidateOpaqueID(nodeID, "ffn_") || page < 1 || page > 1000000 || pageSize != 20 && pageSize != 50 && pageSize != 100 {
		return Leaderboard{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return Leaderboard{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Leaderboard{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, userID, nowMS, false); err != nil {
		return Leaderboard{}, err
	}
	var state string
	var visible, past int
	var end int64
	err = tx.QueryRowContext(ctx, `SELECT state,visible,past_public,ends_at FROM fatfish_periods WHERE id=?`, periodID).Scan(&state, &visible, &past, &end)
	if errors.Is(err, sql.ErrNoRows) {
		return Leaderboard{}, ErrNotFound
	}
	if err != nil {
		return Leaderboard{}, err
	}
	if state == "draft" || (state == "closed" || nowMS >= end*1000) && past != 1 {
		return Leaderboard{}, ErrNotFound
	}
	if nodeID != "" {
		node, readErr := readNodeSnapshotTx(ctx, tx, periodID, nodeID)
		if readErr != nil {
			return Leaderboard{}, readErr
		}
		if node.hidden {
			progress, readErr := readProgressTx(ctx, tx, userID, periodID, nodeID)
			if readErr != nil {
				return Leaderboard{}, readErr
			}
			if !progress.Unlocked {
				best, readErr := bestStarsTx(ctx, tx, userID, periodID)
				if readErr != nil {
					return Leaderboard{}, readErr
				}
				if !node.condition.Eligible(best) {
					return Leaderboard{}, ErrNotFound
				}
			}
		}
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM fatfish_challenges WHERE period_id=? AND state IN ('prepared','active','verifying')`, periodID).Scan(&active); err != nil {
		return Leaderboard{}, err
	}
	board := Leaderboard{PeriodID: periodID, NodeID: nodeID, Final: active == 0 && nowMS >= end*1000, Page: page, PageSize: pageSize, Rows: []LeaderboardRow{}}
	where := `p.period_id=? AND p.total_score_units>0`
	from := `fatfish_period_progress p`
	order := `p.total_score_units DESC,p.achieved_at_ms,p.public_tie_key`
	args := []any{periodID}
	if nodeID != "" {
		from = `fatfish_progress p JOIN fatfish_period_progress pp ON pp.user_id=p.user_id AND pp.period_id=p.period_id`
		where = `p.period_id=? AND p.node_id=? AND p.passed=1`
		order = `p.best_score_units DESC,p.best_at_ms,p.best_challenge_id`
		args = append(args, nodeID)
	}
	query := `WITH ranked AS (SELECT p.user_id,`
	if nodeID == "" {
		query += `p.total_score_units AS score,p.achieved_at_ms AS achieved,`
	} else {
		query += `p.best_score_units AS score,p.best_at_ms AS achieved,`
	}
	query += `ROW_NUMBER() OVER(ORDER BY ` + order + `) AS rank FROM ` + from + ` WHERE ` + where + `)
 SELECT r.user_id,r.score,r.achieved,r.rank,
 CASE WHEN u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) THEN 0 ELSE COALESCE(pref.game_profile_public,u.game_profile_public) END,
 u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url
 FROM ranked r JOIN users u ON u.id=r.user_id LEFT JOIN game_user_preferences pref ON pref.user_id=r.user_id
 WHERE r.rank>? AND r.rank<=? ORDER BY r.rank`
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+from+` WHERE `+where, args...).Scan(&board.Total); err != nil {
		return Leaderboard{}, err
	}
	start := int64(page-1) * int64(pageSize)
	queryArgs := append(append([]any{}, args...), nowMS/1000, start, start+int64(pageSize))
	rows, err := tx.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return Leaderboard{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var owner, score, at, rank int64
		var public int
		var username, nick, discord, avatar, guild string
		if err = rows.Scan(&owner, &score, &at, &rank, &public, &username, &nick, &discord, &avatar, &guild); err != nil {
			return Leaderboard{}, err
		}
		board.Rows = append(board.Rows, LeaderboardRow{Rank: strconv.FormatInt(rank, 10), ScoreUnits: strconv.FormatInt(score, 10), AchievedAtMS: at, IsMe: owner == userID,
			Identity: boardIdentity(public == 1, username, nick, discord, avatar, guild)})
	}
	if err = rows.Err(); err != nil {
		return Leaderboard{}, err
	}
	return board, tx.Commit()
}

func (s *Service) History(ctx context.Context, userID int64, limit, page int) (HistoryPage, error) {
	if (limit != 20 && limit != 50 && limit != 100) || !validCollectionPage(page) {
		return HistoryPage{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return HistoryPage{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HistoryPage{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, userID, nowMS, false); err != nil {
		return HistoryPage{}, err
	}
	cutoff := nowMS - int64(summaryLifetime/time.Millisecond)
	rows, err := tx.QueryContext(ctx, `SELECT id FROM fatfish_challenges WHERE user_id=? AND playtest=0 AND terminal_at_ms IS NOT NULL AND terminal_at_ms>=? ORDER BY terminal_at_ms DESC,id LIMIT ? OFFSET ?`, userID, cutoff, limit+1, (page-1)*limit)
	if err != nil {
		return HistoryPage{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return HistoryPage{}, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return HistoryPage{}, err
	}
	rows.Close()
	out := HistoryPage{Items: []ChallengeView{}, Page: page, PageSize: limit, HasMore: len(ids) > limit}
	if out.HasMore {
		ids = ids[:limit]
	}
	for _, id := range ids {
		c, err := readChallengeTx(ctx, tx, id)
		if err != nil {
			return HistoryPage{}, err
		}
		v, err := c.view(nowMS, "")
		if err != nil {
			return HistoryPage{}, err
		}
		out.Items = append(out.Items, v)
	}
	return out, tx.Commit()
}
