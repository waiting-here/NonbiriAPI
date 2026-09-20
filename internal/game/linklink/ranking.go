package linklink

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Identity struct {
	Kind        string
	DisplayName string
	AvatarURL   *string
}

func (identity Identity) MarshalJSON() ([]byte, error) {
	if identity.Kind == "anonymous" {
		return json.Marshal(struct {
			Kind string `json:"kind"`
		}{identity.Kind})
	}
	return json.Marshal(struct {
		Kind        string  `json:"kind"`
		DisplayName string  `json:"display_name"`
		AvatarURL   *string `json:"avatar_url"`
	}{identity.Kind, identity.DisplayName, identity.AvatarURL})
}

type LeaderboardRow struct {
	Rank       string   `json:"rank"`
	Score      string   `json:"score"`
	AchievedAt int64    `json:"achieved_at"`
	Identity   Identity `json:"identity"`
	IsMe       bool     `json:"is_me"`
}

type Leaderboard struct {
	Spec         string           `json:"spec"`
	WindowDays   int              `json:"window_days"`
	WindowStart  int64            `json:"window_start"`
	AsOf         int64            `json:"as_of"`
	RulesVersion int              `json:"rules_version"`
	Rows         []LeaderboardRow `json:"rows"`
	Me           *LeaderboardRow  `json:"me"`
}

func ensureLeaderboardTieKey(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	// This is a public tie breaker, not a credential. Create it only once and
	// retain it when game history expires or the profile visibility changes.
	_, err := tx.ExecContext(ctx, `
INSERT INTO game_user_preferences(user_id,linklink_public_tie_key,game_profile_public,updated_at)
SELECT ?,randomblob(32),u.game_profile_public,? FROM users u
WHERE u.id=? AND NOT EXISTS(SELECT 1 FROM game_user_preferences WHERE user_id=? AND linklink_public_tie_key IS NOT NULL)
ON CONFLICT(user_id) DO UPDATE SET linklink_public_tie_key=excluded.linklink_public_tie_key,updated_at=excluded.updated_at
WHERE game_user_preferences.linklink_public_tie_key IS NULL`, userID, now, userID, userID)
	return classifyDB(err)
}

func (service *Service) Leaderboard(ctx context.Context, userID int64, spec, window string) (Leaderboard, error) {
	if service == nil || service.closed.Load() {
		return Leaderboard{}, ErrClosed
	}
	_, validSpec := resolveSpec(spec)
	if userID <= 0 || !validSpec || window != "7d" && window != "30d" {
		return Leaderboard{}, ErrInvalidRequest
	}
	now, err := service.decisionNow()
	if err != nil {
		return Leaderboard{}, err
	}
	days := 7
	if window == "30d" {
		days = 30
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
	return queryLeaderboard(ctx, tx, userID, spec, days, now)
}

const leaderboardSQL = `
WITH candidates AS (
	SELECT user_id,score,terminal_at,
		ROW_NUMBER() OVER(PARTITION BY user_id ORDER BY score DESC,terminal_at ASC,session_id ASC) AS personal_rank
	FROM game_linklink_summaries
	WHERE spec=? AND rules_version=2 AND terminal_reason='completed' AND terminal_at>? AND terminal_at<=?
), ranked AS (
	SELECT c.user_id,c.score,c.terminal_at,
		ROW_NUMBER() OVER(ORDER BY c.score DESC,c.terminal_at ASC,p.linklink_public_tie_key ASC) AS rank
	FROM candidates c JOIN users u ON u.id=c.user_id
	JOIN game_user_preferences p ON p.user_id=c.user_id
	WHERE c.personal_rank=1 AND u.is_admin=0
)
SELECT r.user_id,r.score,r.terminal_at,r.rank,CASE WHEN u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) THEN 0 ELSE COALESCE(p.game_profile_public,u.game_profile_public) END,
	u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url
FROM ranked r JOIN users u ON u.id=r.user_id LEFT JOIN game_user_preferences p ON p.user_id=r.user_id
WHERE r.rank<=20 OR r.user_id=? ORDER BY r.rank`

func queryLeaderboard(ctx context.Context, tx *sql.Tx, userID int64, spec string, days int, now int64) (Leaderboard, error) {
	start := max(int64(0), now-int64(days)*86400)
	result := Leaderboard{Spec: spec, WindowDays: days, WindowStart: start, AsOf: now, RulesVersion: 2, Rows: make([]LeaderboardRow, 0, 20)}
	rows, err := tx.QueryContext(ctx, leaderboardSQL, spec, start, now, now, userID)
	if err != nil {
		return Leaderboard{}, classifyDB(err)
	}
	defer rows.Close()
	for rows.Next() {
		var owner, score, achieved, rank int64
		var public int
		var username, nick, discordID, avatar, guildAvatar string
		if err := rows.Scan(&owner, &score, &achieved, &rank, &public, &username, &nick, &discordID, &avatar, &guildAvatar); err != nil {
			return Leaderboard{}, classifyDB(err)
		}
		row := LeaderboardRow{
			Rank: strconv.FormatInt(rank, 10), Score: strconv.FormatInt(score, 10), AchievedAt: achieved,
			Identity: publicIdentity(public == 1, username, nick, discordID, avatar, guildAvatar), IsMe: owner == userID,
		}
		if rank <= 20 {
			result.Rows = append(result.Rows, row)
		} else if row.IsMe {
			result.Me = &row
		}
	}
	if err := rows.Err(); err != nil {
		return Leaderboard{}, classifyDB(err)
	}
	return result, nil
}

func publicIdentity(public bool, username, nick, discordID, avatar, guildAvatar string) Identity {
	if !public {
		return Identity{Kind: "anonymous"}
	}
	if nick == "" {
		nick = username
	}
	if !utf8.ValidString(nick) {
		return Identity{Kind: "anonymous"}
	}
	display := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(nick))
	runes := []rune(display)
	if len(runes) > 128 {
		runes = runes[:128]
	}
	if len(runes) == 0 {
		return Identity{Kind: "anonymous"}
	}
	var picture *string
	if parsed, err := url.Parse(guildAvatar); err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil {
		picture = &guildAvatar
	} else if safePathAtom(discordID) && safePathAtom(avatar) {
		value := "https://cdn.discordapp.com/avatars/" + discordID + "/" + avatar + ".png"
		picture = &value
	}
	return Identity{Kind: "public", DisplayName: string(runes), AvatarURL: picture}
}

func safePathAtom(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || strings.ContainsRune("_-.", r)) {
			return false
		}
	}
	return true
}
