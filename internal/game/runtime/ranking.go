package runtime

import (
	"context"
	"database/sql"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type expiringFact struct {
	BatchID   string
	UserID    int64
	ExpiresAt int64
	Payout    db.U128
}

// expireRankFacts catches up only facts due at the frozen queryNow. It is
// exact-once and deterministic even when the worker runs late.
func (service *Service) expireRankFacts(ctx context.Context, queryNow int64, deadline time.Time) error {
	for {
		if !service.budgetNow().Before(deadline) {
			var due int
			if err := service.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_fishing_rank_facts WHERE aggregate_applied=1 AND expires_at<=?)`, queryNow).Scan(&due); err != nil {
				return classifyDB(err)
			}
			if due == 1 {
				return ErrServiceUnavailable
			}
			return nil
		}
		tx, err := service.database.BeginTx(ctx, nil)
		if err != nil {
			return classifyDB(err)
		}
		rows, err := tx.QueryContext(ctx, `SELECT batch_id_text,user_id,expires_at,payout_total FROM game_fishing_rank_facts WHERE aggregate_applied=1 AND expires_at<=? ORDER BY expires_at,batch_id_text LIMIT ?`, queryNow, rankBatchSize)
		if err != nil {
			tx.Rollback()
			return classifyDB(err)
		}
		facts := make([]expiringFact, 0, rankBatchSize)
		for rows.Next() {
			var fact expiringFact
			var raw []byte
			if err = rows.Scan(&fact.BatchID, &fact.UserID, &fact.ExpiresAt, &raw); err != nil {
				rows.Close()
				tx.Rollback()
				return classifyDB(err)
			}
			fact.Payout, err = db.DecodeU128(raw)
			if err != nil {
				rows.Close()
				tx.Rollback()
				return ErrInvariant
			}
			facts = append(facts, fact)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			tx.Rollback()
			return classifyDB(err)
		}
		rows.Close()
		for _, fact := range facts {
			if err = expireOneFact(ctx, tx, fact); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err = tx.Commit(); err != nil {
			return classifyDB(err)
		}
		if len(facts) < rankBatchSize {
			return nil
		}
	}
}

// expireRankFactsInTx is the caller-owned transaction variant used by the
// lifecycle export rail. It applies the same frozen query_now and reducer as
// leaderboard reads without opening or committing an independent transaction.
func (service *Service) expireRankFactsInTx(ctx context.Context, tx *sql.Tx, queryNow int64, deadline time.Time) error {
	if tx == nil {
		return ErrInvariant
	}
	for {
		if !service.budgetNow().Before(deadline) {
			var due int
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_fishing_rank_facts WHERE aggregate_applied=1 AND expires_at<=?)`, queryNow).Scan(&due); err != nil {
				return classifyDB(err)
			}
			if due == 1 {
				return ErrServiceUnavailable
			}
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT batch_id_text,user_id,expires_at,payout_total FROM game_fishing_rank_facts WHERE aggregate_applied=1 AND expires_at<=? ORDER BY expires_at,batch_id_text LIMIT ?`, queryNow, rankBatchSize)
		if err != nil {
			return classifyDB(err)
		}
		facts := make([]expiringFact, 0, rankBatchSize)
		for rows.Next() {
			var fact expiringFact
			var raw []byte
			if err = rows.Scan(&fact.BatchID, &fact.UserID, &fact.ExpiresAt, &raw); err != nil {
				rows.Close()
				return classifyDB(err)
			}
			fact.Payout, err = db.DecodeU128(raw)
			if err != nil {
				rows.Close()
				return ErrInvariant
			}
			facts = append(facts, fact)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return classifyDB(err)
		}
		if err = rows.Close(); err != nil {
			return classifyDB(err)
		}
		for _, fact := range facts {
			if err = expireOneFact(ctx, tx, fact); err != nil {
				return err
			}
		}
		if len(facts) < rankBatchSize {
			return nil
		}
	}
}

// cleanupTerminalBatches enforces the independent 30-day batch/outcome
// retention window. Reserved batches are never selected; best rows retain
// their denormalized safe snapshot through the schema's composite SET NULL.
func (service *Service) cleanupTerminalBatches(ctx context.Context, now int64) (int, error) {
	cutoff := now - int64(rankWindow/time.Second)
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, classifyDB(err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM game_fishing_batches WHERE state IN ('committed','released') AND settled_at<=? ORDER BY settled_at,id LIMIT ?`, cutoff, workerBatchSize)
	if err != nil {
		return 0, classifyDB(err)
	}
	ids := make([]string, 0, workerBatchSize)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, classifyDB(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, classifyDB(err)
	}
	if err = rows.Close(); err != nil {
		return 0, classifyDB(err)
	}
	for _, id := range ids {
		result, deleteErr := tx.ExecContext(ctx, `DELETE FROM game_fishing_batches WHERE id=? AND state IN ('committed','released') AND settled_at<=?`, id, cutoff)
		if deleteErr != nil {
			return 0, classifyDB(deleteErr)
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return 0, classifyDB(rowsErr)
		}
		if changed != 1 {
			return 0, ErrConflict
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, classifyDB(err)
	}
	return len(ids), nil
}

func expireOneFact(ctx context.Context, tx *sql.Tx, fact expiringFact) error {
	var countRaw, totalRaw, revisionRaw []byte
	var achieved int64
	err := tx.QueryRowContext(ctx, `SELECT batch_count,total_payout,score_achieved_at,revision FROM game_fishing_rank_aggregates WHERE user_id=?`, fact.UserID).Scan(&countRaw, &totalRaw, &achieved, &revisionRaw)
	if err != nil {
		return ErrInvariant
	}
	count, err1 := db.DecodeU128(countRaw)
	total, err2 := db.DecodeU128(totalRaw)
	revision, err3 := db.DecodeU128(revisionRaw)
	if err1 != nil || err2 != nil || err3 != nil || count.Big().Sign() <= 0 || total.Big().Cmp(fact.Payout.Big()) < 0 {
		return ErrInvariant
	}
	if count.Big().Cmp(big.NewInt(1)) == 0 {
		if total.Big().Cmp(fact.Payout.Big()) != 0 {
			return ErrInvariant
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM game_fishing_rank_aggregates WHERE user_id=? AND revision=?`, fact.UserID, revisionRaw)
		if err != nil {
			return classifyDB(err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return ErrConflict
		}
	} else {
		nextCount, _ := db.U128FromBig(new(big.Int).Sub(count.Big(), big.NewInt(1)))
		nextTotal, _ := db.U128FromBig(new(big.Int).Sub(total.Big(), fact.Payout.Big()))
		nextRevision, revisionErr := db.U128FromBig(new(big.Int).Add(revision.Big(), big.NewInt(1)))
		if revisionErr != nil {
			return ErrInvariant
		}
		if fact.Payout.Big().Sign() != 0 {
			achieved = fact.ExpiresAt
		}
		result, writeErr := tx.ExecContext(ctx, `UPDATE game_fishing_rank_aggregates SET batch_count=?,total_payout=?,score_achieved_at=?,revision=?,updated_at=? WHERE user_id=? AND revision=?`, db.EncodeU128(nextCount), db.EncodeU128(nextTotal), achieved, db.EncodeU128(nextRevision), fact.ExpiresAt, fact.UserID, revisionRaw)
		if writeErr != nil {
			return classifyDB(writeErr)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return ErrConflict
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM game_fishing_rank_facts WHERE batch_id_text=? AND aggregate_applied=1`, fact.BatchID)
	if err != nil {
		return classifyDB(err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

func (service *Service) FishingLeaderboard(ctx context.Context, userID int64, board string) (FishingLeaderboard, error) {
	if userID <= 0 || board != "single" && board != "total" {
		return FishingLeaderboard{}, ErrInvalidRequest
	}
	queryNow := service.now().UTC().Unix()
	if board == "total" {
		if err := service.expireRankFacts(ctx, queryNow, service.budgetNow().Add(rankBudget)); err != nil {
			return FishingLeaderboard{}, err
		}
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return FishingLeaderboard{}, classifyDB(err)
	}
	defer tx.Rollback()
	if board == "single" {
		return querySingleLeaderboard(ctx, tx, userID, queryNow)
	}
	return queryTotalLeaderboard(ctx, tx, userID, queryNow)
}

type rankIdentity struct {
	UserID                                              int64
	Public                                              bool
	Username, GuildNick, DiscordID, Avatar, GuildAvatar string
}

func querySingleLeaderboard(ctx context.Context, tx *sql.Tx, userID, queryNow int64) (FishingLeaderboard, error) {
	rows, err := tx.QueryContext(ctx, `WITH ranked AS (
SELECT b.user_id,b.species_key,b.size_cm,ROW_NUMBER() OVER(ORDER BY b.size_cm DESC,b.caught_at ASC,b.public_tie_key ASC) AS rank,
COALESCE(p.game_profile_public,u.game_profile_public) AS game_profile_public,u.username,u.guild_nick,COALESCE(u.discord_id,'') AS discord_id,u.avatar,u.guild_avatar_url
FROM game_fishing_best b JOIN users u ON u.id=b.user_id LEFT JOIN game_user_preferences p ON p.user_id=u.id
WHERE u.is_admin=0 AND u.is_banned=0)
SELECT user_id,species_key,size_cm,rank,game_profile_public,username,guild_nick,discord_id,avatar,guild_avatar_url FROM ranked WHERE rank<=20 OR user_id=? ORDER BY rank`, userID)
	if err != nil {
		return FishingLeaderboard{}, classifyDB(err)
	}
	defer rows.Close()
	result := FishingLeaderboard{Board: "single", Entries: make([]FishingLeaderboardRow, 0, 20)}
	for rows.Next() {
		var identity rankIdentity
		var species string
		var size int
		var rank int64
		var public int
		if err = rows.Scan(&identity.UserID, &species, &size, &rank, &public, &identity.Username, &identity.GuildNick, &identity.DiscordID, &identity.Avatar, &identity.GuildAvatar); err != nil {
			return FishingLeaderboard{}, classifyDB(err)
		}
		identity.Public = public == 1
		row := FishingLeaderboardRow{Rank: strconv.FormatInt(rank, 10), SpeciesKey: species, SizeCM: size, Identity: projectIdentity(identity), IsMe: identity.UserID == userID}
		placeRankRow(&result, row, rank)
	}
	if err = rows.Err(); err != nil {
		return FishingLeaderboard{}, classifyDB(err)
	}
	return result, nil
}

func queryTotalLeaderboard(ctx context.Context, tx *sql.Tx, userID, queryNow int64) (FishingLeaderboard, error) {
	rows, err := tx.QueryContext(ctx, `WITH ranked AS (
SELECT a.user_id,a.total_payout,ROW_NUMBER() OVER(ORDER BY a.total_payout DESC,a.score_achieved_at ASC,a.public_tie_key ASC) AS rank,
COALESCE(p.game_profile_public,u.game_profile_public) AS game_profile_public,u.username,u.guild_nick,COALESCE(u.discord_id,'') AS discord_id,u.avatar,u.guild_avatar_url
FROM game_fishing_rank_aggregates a JOIN users u ON u.id=a.user_id LEFT JOIN game_user_preferences p ON p.user_id=u.id
WHERE u.is_admin=0 AND u.is_banned=0)
SELECT user_id,total_payout,rank,game_profile_public,username,guild_nick,discord_id,avatar,guild_avatar_url FROM ranked WHERE rank<=20 OR user_id=? ORDER BY rank`, userID)
	if err != nil {
		return FishingLeaderboard{}, classifyDB(err)
	}
	defer rows.Close()
	window := queryNow - int64(rankWindow/time.Second)
	result := FishingLeaderboard{Board: "total", WindowStart: &window, Entries: make([]FishingLeaderboardRow, 0, 20)}
	for rows.Next() {
		var identity rankIdentity
		var totalRaw []byte
		var rank int64
		var public int
		if err = rows.Scan(&identity.UserID, &totalRaw, &rank, &public, &identity.Username, &identity.GuildNick, &identity.DiscordID, &identity.Avatar, &identity.GuildAvatar); err != nil {
			return FishingLeaderboard{}, classifyDB(err)
		}
		total, decodeErr := db.DecodeU128(totalRaw)
		if decodeErr != nil {
			return FishingLeaderboard{}, ErrInvariant
		}
		identity.Public = public == 1
		row := FishingLeaderboardRow{Rank: strconv.FormatInt(rank, 10), TotalCredits: formatWideMilli(total.Big()), Identity: projectIdentity(identity), IsMe: identity.UserID == userID}
		placeRankRow(&result, row, rank)
	}
	if err = rows.Err(); err != nil {
		return FishingLeaderboard{}, classifyDB(err)
	}
	return result, nil
}

func placeRankRow(result *FishingLeaderboard, row FishingLeaderboardRow, rank int64) {
	if rank <= 20 {
		result.Entries = append(result.Entries, row)
	} else if row.IsMe {
		copy := row
		result.Me = &copy
	}
}

func projectIdentity(value rankIdentity) Identity {
	if !value.Public {
		return Identity{Kind: "anonymous"}
	}
	display := value.GuildNick
	if display == "" {
		display = value.Username
	}
	display = truncateRunes(display, 128)
	if display == "" {
		return Identity{Kind: "anonymous"}
	}
	var avatar *string
	if safeHTTPS(value.GuildAvatar) {
		copy := value.GuildAvatar
		avatar = &copy
	} else if value.DiscordID != "" && safePathAtom(value.DiscordID) && safePathAtom(value.Avatar) && value.Avatar != "" {
		candidate := "https://cdn.discordapp.com/avatars/" + value.DiscordID + "/" + value.Avatar + ".png"
		avatar = &candidate
	}
	return Identity{Kind: "public", DisplayName: display, AvatarURL: avatar}
}

func truncateRunes(value string, limit int) string {
	if !utf8.ValidString(value) {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}
func safeHTTPS(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
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
