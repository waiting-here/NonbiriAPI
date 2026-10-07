package rating

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
)

var ErrWindow = errors.New("rating: unknown ranking window")

type Row struct {
	Rank     string           `json:"rank"`
	Wins     string           `json:"wins"`
	Played   string           `json:"played"`
	Draws    string           `json:"draws"`
	IsMe     bool             `json:"is_me"`
	Identity ranking.Identity `json:"identity"`
}

type Board struct {
	AsOf   int64  `json:"as_of"`
	Window string `json:"window"`
	Rows   []Row  `json:"rows"`
	Me     *Row   `json:"me"`
}

// ReadTx projects only match results and current public identity preferences.
// Rating values and internal account IDs never enter the leaderboard DTO.
func ReadTx(ctx context.Context, tx *sql.Tx, game string, user int64, window string, now int64) (Board, error) {
	var days int64
	switch window {
	case "7d":
		days = 7
	case "30d":
		days = 30
	default:
		return Board{}, ErrWindow
	}
	result := Board{AsOf: now, Window: window, Rows: []Row{}}
	rows, err := tx.QueryContext(ctx, `WITH scores AS (
 SELECT user_id,sum(result=2) wins,count(*) played,sum(result=1) draws,max(CASE WHEN result=2 THEN settled_at END) achieved_at
 FROM game_duel_results WHERE game_key=? AND settled_at>? AND settled_at<=? GROUP BY user_id HAVING wins>0
), ranked AS (
 SELECT *,ROW_NUMBER() OVER(ORDER BY wins DESC,played,achieved_at,user_id) rank FROM scores
)
SELECT r.user_id,r.rank,r.wins,r.played,r.draws,
 CASE WHEN u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) THEN 0 ELSE COALESCE(p.game_profile_public,u.game_profile_public) END,
 u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url
FROM ranked r JOIN users u ON u.id=r.user_id LEFT JOIN game_user_preferences p ON p.user_id=r.user_id
WHERE u.is_admin=0 AND (r.rank<=20 OR r.user_id=?) ORDER BY r.rank`, game, now-days*86400, now, now, user)
	if err != nil {
		return Board{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var owner, place, wins, played, draws int64
		var public bool
		var username, nick, discord, avatar, guild string
		if err := rows.Scan(&owner, &place, &wins, &played, &draws, &public, &username, &nick, &discord, &avatar, &guild); err != nil {
			return Board{}, err
		}
		row := Row{Rank: strconv.FormatInt(place, 10), Wins: strconv.FormatInt(wins, 10), Played: strconv.FormatInt(played, 10), Draws: strconv.FormatInt(draws, 10), IsMe: owner == user, Identity: ranking.ProfileIdentity(public, username, nick, discord, avatar, guild)}
		if place > 20 {
			result.Me = &row
		} else {
			result.Rows = append(result.Rows, row)
		}
	}
	return result, rows.Err()
}

type Export struct {
	Rating    int   `json:"rating"`
	Played    int64 `json:"played"`
	UpdatedAt int64 `json:"updated_at"`
}

type MatchResult struct {
	Result       int   `json:"result"`
	RatingBefore int   `json:"rating_before"`
	RatingAfter  int   `json:"rating_after"`
	SettledAt    int64 `json:"settled_at"`
}

func MatchTx(ctx context.Context, tx *sql.Tx, game, session string, user int64) (*MatchResult, error) {
	var out MatchResult
	err := tx.QueryRowContext(ctx, `SELECT result,rating_before,rating_after,settled_at FROM game_duel_results WHERE game_key=? AND session_id=? AND user_id=?`, game, session, user).Scan(&out.Result, &out.RatingBefore, &out.RatingAfter, &out.SettledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &out, err
}

func DeleteTx(ctx context.Context, tx *sql.Tx, game string, user int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_duel_results WHERE game_key=? AND user_id=?`, game, user); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM game_duel_ratings WHERE game_key=? AND user_id=?`, game, user)
	return err
}

func ExportTx(ctx context.Context, tx *sql.Tx, game string, user int64) (*Export, error) {
	var out Export
	err := tx.QueryRowContext(ctx, `SELECT rating,played,updated_at FROM game_duel_ratings WHERE game_key=? AND user_id=?`, game, user).Scan(&out.Rating, &out.Played, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &out, err
}
