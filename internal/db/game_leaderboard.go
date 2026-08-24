package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

type FishingLeaderboardBoard string

const (
	FishingLeaderboardSingle FishingLeaderboardBoard = "single"
	FishingLeaderboardTotal  FishingLeaderboardBoard = "total"
)

// FishingLeaderboardEntry is an ownership-safe internal projection. The API
// layer decides which profile fields to emit; UserID is never serialized.
type FishingLeaderboardEntry struct {
	UserID            int64
	Rank              int
	SpeciesKey        string
	SizeCM            int
	TotalCreditsMilli int64
	CaughtAt          time.Time
	SettledAt         time.Time
	Username          string
	GuildNick         string
	Avatar            string
	GuildAvatarURL    string
	DiscordID         string
	ProfilePublic     bool
	ManualLevel       *int
	AutoLevel         int
}

type FishingLeaderboard struct {
	Board       FishingLeaderboardBoard
	WindowStart *time.Time
	Entries     []FishingLeaderboardEntry
	Mine        *FishingLeaderboardEntry
}

// ListFishingLeaderboard returns Top 20 plus the requesting user's own rank.
// Ranking is performed in SQL with ROW_NUMBER over the complete eligible set,
// keeping tie-breakers deterministic and preventing a page-boundary race from
// changing the meaning of rank.
func (s *Store) ListFishingLeaderboard(ctx context.Context, userID int64, board FishingLeaderboardBoard, now time.Time) (FishingLeaderboard, error) {
	if s == nil || s.db == nil || ctx == nil || userID <= 0 || now.IsZero() {
		return FishingLeaderboard{}, ErrNotFound
	}
	if board != FishingLeaderboardSingle && board != FishingLeaderboardTotal {
		return FishingLeaderboard{}, ErrGameInvalid
	}
	var userExists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=?`, userID).Scan(&userExists); err != nil {
		return FishingLeaderboard{}, fmt.Errorf("game leaderboard user: %w", err)
	}
	if userExists == 0 {
		return FishingLeaderboard{}, ErrNotFound
	}

	result := FishingLeaderboard{Board: board, Entries: make([]FishingLeaderboardEntry, 0, 21)}
	args := []any{userID}
	query := ``
	if board == FishingLeaderboardSingle {
		query = `
WITH ranked AS (
 SELECT b.user_id,b.species_key,b.size_cm,b.caught_at,
        ROW_NUMBER() OVER (ORDER BY b.size_cm DESC,b.caught_at ASC,b.user_id ASC) AS rank_no
 FROM game_fishing_best b
 JOIN users u ON u.id=b.user_id
 WHERE u.is_admin=0 AND (u.is_banned=0 OR (u.banned_until IS NOT NULL AND u.banned_until<=?))
)
SELECT ranked.rank_no,ranked.user_id,ranked.species_key,ranked.size_cm,ranked.caught_at,
       u.username,u.guild_nick,u.avatar,u.guild_avatar_url,COALESCE(u.discord_id,''),
       u.game_profile_public,u.level,u.auto_level
FROM ranked JOIN users u ON u.id=ranked.user_id
WHERE ranked.rank_no<=20 OR ranked.user_id=?
ORDER BY ranked.rank_no`
		args = []any{now.Unix(), userID}
	} else {
		cutoff := now.Add(-GameRoundRetention)
		result.WindowStart = &cutoff
		query = `
WITH totals AS (
 SELECT s.user_id,SUM(s.payout_milli) AS total_credits,MAX(r.settled_at) AS settled_at
 FROM game_settlements s JOIN game_rounds r ON r.settlement_id=s.id
 JOIN users u ON u.id=s.user_id
 WHERE s.game_type=? AND s.game_version=? AND s.state='committed'
   AND r.settled_at IS NOT NULL AND r.settled_at>=?
   AND u.is_admin=0 AND (u.is_banned=0 OR (u.banned_until IS NOT NULL AND u.banned_until<=?))
 GROUP BY s.user_id
), ranked AS (
 SELECT user_id,total_credits,settled_at,
        ROW_NUMBER() OVER (ORDER BY total_credits DESC,settled_at ASC,user_id ASC) AS rank_no
 FROM totals
)
SELECT ranked.rank_no,ranked.user_id,ranked.total_credits,ranked.settled_at,
       u.username,u.guild_nick,u.avatar,u.guild_avatar_url,COALESCE(u.discord_id,''),
       u.game_profile_public,u.level,u.auto_level
FROM ranked JOIN users u ON u.id=ranked.user_id
WHERE ranked.rank_no<=20 OR ranked.user_id=?
ORDER BY ranked.rank_no`
		args = []any{game.FishingID, game.FishingVersion, cutoff.Unix(), now.Unix(), userID}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return FishingLeaderboard{}, fmt.Errorf("game leaderboard query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry FishingLeaderboardEntry
		var rank int64
		var caughtAt, settledAt sql.NullInt64
		var profilePublic int
		var manual sql.NullInt64
		if board == FishingLeaderboardSingle {
			if err := rows.Scan(&rank, &entry.UserID, &entry.SpeciesKey, &entry.SizeCM, &caughtAt,
				&entry.Username, &entry.GuildNick, &entry.Avatar, &entry.GuildAvatarURL, &entry.DiscordID,
				&profilePublic, &manual, &entry.AutoLevel); err != nil {
				return FishingLeaderboard{}, fmt.Errorf("game leaderboard single scan: %w", err)
			}
			if caughtAt.Valid {
				entry.CaughtAt = time.Unix(caughtAt.Int64, 0).UTC()
			}
		} else {
			if err := rows.Scan(&rank, &entry.UserID, &entry.TotalCreditsMilli, &settledAt,
				&entry.Username, &entry.GuildNick, &entry.Avatar, &entry.GuildAvatarURL, &entry.DiscordID,
				&profilePublic, &manual, &entry.AutoLevel); err != nil {
				return FishingLeaderboard{}, fmt.Errorf("game leaderboard total scan: %w", err)
			}
			if settledAt.Valid {
				entry.SettledAt = time.Unix(settledAt.Int64, 0).UTC()
			}
		}
		if rank <= 0 || rank > int64(^uint(0)>>1) {
			return FishingLeaderboard{}, ErrGameInternal
		}
		entry.Rank = int(rank)
		entry.ProfilePublic = profilePublic != 0
		if manual.Valid {
			value := int(manual.Int64)
			entry.ManualLevel = &value
		}
		if entry.UserID == userID {
			copy := entry
			result.Mine = &copy
		}
		if entry.Rank <= 20 {
			result.Entries = append(result.Entries, entry)
		}
	}
	if err := rows.Err(); err != nil {
		return FishingLeaderboard{}, fmt.Errorf("game leaderboard rows: %w", err)
	}
	return result, nil
}

// ValidateLeaderboardEntry is a narrow defensive check for API projections;
// it also keeps corrupt legacy rows from becoming an identity-bearing wire
// response.
func ValidateLeaderboardEntry(entry FishingLeaderboardEntry, board FishingLeaderboardBoard) error {
	if entry.UserID <= 0 || entry.Rank <= 0 || (board != FishingLeaderboardSingle && board != FishingLeaderboardTotal) {
		return ErrGameInternal
	}
	if board == FishingLeaderboardSingle && (entry.SpeciesKey == "" || entry.SizeCM <= 0) {
		return ErrGameInternal
	}
	if board == FishingLeaderboardTotal && entry.TotalCreditsMilli < 0 {
		return ErrGameInternal
	}
	return nil
}
