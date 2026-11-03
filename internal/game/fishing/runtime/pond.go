package runtime

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

const (
	RouteFishingPondBatchReturn = "/api/games/fishing/batches/{id}/return"
	RouteFishingPondReturn      = "/api/games/fishing/batches/{id}/outcomes/{ordinal}/return"
	RouteFishingPondLeaderboard = "/api/games/fishing/pond/leaderboard"
)

type PondOutcome struct {
	ReturnState string    `json:"return_state"`
	Gift        *Identity `json:"gift"`
	ReturnedAt  *int64    `json:"returned_at,omitempty"`
}

type PondReturnReceipt struct {
	BatchID     string `json:"batch_id"`
	Ordinal     int    `json:"ordinal"`
	ReturnState string `json:"return_state"`
	ReturnedAt  int64  `json:"returned_at"`
}

type PondBatchReturnReceipt struct {
	BatchID  string              `json:"batch_id"`
	Receipts []PondReturnReceipt `json:"receipts"`
}

type PondLeaderboardRow struct {
	Rank      string   `json:"rank"`
	ItemCount string   `json:"item_count"`
	Identity  Identity `json:"identity"`
	IsMe      bool     `json:"is_me"`
}

type PondLeaderboard struct {
	Board       string               `json:"board"`
	Period      string               `json:"period"`
	WindowStart *int64               `json:"window_start"`
	Entries     []PondLeaderboardRow `json:"entries"`
	Me          *PondLeaderboardRow  `json:"me"`
}

func projectPondOutcome(species string, returned sql.NullInt64, gift, donor, avatar sql.NullString) *PondOutcome {
	out := &PondOutcome{ReturnState: "available"}
	if gift.Valid {
		out.ReturnState = "gift"
		identity := Identity{Kind: "anonymous"}
		if donor.Valid {
			identity = Identity{Kind: "public", DisplayName: donor.String}
			if avatar.Valid {
				identity.AvatarURL = &avatar.String
			}
		}
		out.Gift = &identity
	} else if returned.Valid {
		out.ReturnState = "returned"
		out.ReturnedAt = &returned.Int64
		if species == "fry" {
			out.ReturnState = "released"
		}
	}
	return out
}

// The first K eligible queue entries can only belong to the first K donor
// heads. Reading K entries per donor bounds the candidate set by K*K, even
// when every earlier queue entry belongs to the recipient. CROSS JOIN keeps
// the bounded donor set outside the indexed queue lookups.
const pondGiftCandidatesSQL = `SELECT q.id,q.donor_user_id,q.species_key,q.donor_display_name,q.donor_avatar_url
FROM (SELECT donor_user_id FROM game_fishing_pond_queue_heads WHERE donor_user_id<>? ORDER BY queue_id LIMIT ?) h
CROSS JOIN game_fishing_pond_queue q ON q.id IN (
 SELECT id FROM game_fishing_pond_queue WHERE donor_user_id=h.donor_user_id ORDER BY id LIMIT ?)
ORDER BY q.id LIMIT ?`

// settlePondTx runs only on the reserved -> committed transition, after the
// complete ordinary batch has been drawn, decorated and economically fixed.
// Queue consumption, gift snapshots and counts share that terminal transaction.
func (service *Service) settlePondTx(ctx context.Context, tx *sql.Tx, record batchRecord, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT o.ordinal,o.species_key FROM game_fishing_pond_outcomes p
JOIN game_fishing_outcomes o ON o.batch_id=p.batch_id AND o.ordinal=p.ordinal
WHERE p.batch_id=? AND o.tier='junk' AND o.payout_milli=0 ORDER BY o.ordinal`, record.ID)
	if err != nil {
		return classifyDB(err)
	}
	type catch struct {
		ordinal int
		species string
	}
	catches := make([]catch, 0, record.Count)
	for rows.Next() {
		var value catch
		if err = rows.Scan(&value.ordinal, &value.species); err != nil {
			rows.Close()
			return classifyDB(err)
		}
		catches = append(catches, value)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return classifyDB(err)
	}
	if err = rows.Close(); err != nil {
		return classifyDB(err)
	}
	if len(catches) == 0 {
		return nil
	}
	var queueSize uint64
	if err = tx.QueryRowContext(ctx, `SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`).Scan(&queueSize); err != nil {
		return classifyDB(err)
	}
	type queuedGift struct {
		id, donorID   int64
		species, name string
		avatar        sql.NullString
	}
	queue := make([]queuedGift, 0, len(catches))
	if queueSize > 0 {
		rows, err = tx.QueryContext(ctx, pondGiftCandidatesSQL, record.UserID, len(catches), len(catches), len(catches))
		if err != nil {
			return classifyDB(err)
		}
		for rows.Next() {
			var value queuedGift
			if err = rows.Scan(&value.id, &value.donorID, &value.species, &value.name, &value.avatar); err != nil {
				rows.Close()
				return classifyDB(err)
			}
			queue = append(queue, value)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return classifyDB(err)
		}
		if err = rows.Close(); err != nil {
			return classifyDB(err)
		}
		if uint64(len(queue)) > min(queueSize, uint64(len(catches))) {
			return ErrInvariant
		}
	}
	consumed := 0
	var cleanupCount int64
	for _, value := range catches {
		service.pondRNGMu.Lock()
		hit, randomErr := fishing.RollPondGift(queueSize, service.pondRandom)
		service.pondRNGMu.Unlock()
		if randomErr != nil {
			return ErrServiceUnavailable
		}
		if hit && consumed < len(queue) {
			gift := queue[consumed]
			value.species = gift.species
			if _, err = tx.ExecContext(ctx, `UPDATE game_fishing_pond_outcomes SET gift_species_key=?,donor_user_id=?,donor_display_name=?,donor_avatar_url=? WHERE batch_id=? AND ordinal=?`, gift.species, gift.donorID, gift.name, gift.avatar, record.ID, value.ordinal); err != nil {
				return classifyDB(err)
			}
			consumed++
			queueSize--
		}
		if value.species != "fry" {
			cleanupCount++
		}
	}
	if consumed > 0 {
		ids := make([]any, consumed)
		for i := range ids {
			ids[i] = queue[i].id
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM game_fishing_pond_queue WHERE id IN (`+placeholders(consumed)+`)`, ids...)
		if err != nil {
			return classifyDB(err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != int64(consumed) {
			return ErrInvariant
		}
	}
	return service.addPondCountTx(ctx, tx, record.UserID, "cleanup", now, cleanupCount)
}

func (service *Service) addPondCountTx(ctx context.Context, tx *sql.Tx, userID int64, board string, now, count int64) error {
	if count == 0 {
		return nil
	}
	tie, err := db.ComputeGameLeaderboardTieKeyFromDerivedKey(service.leaderboardTieKey, game.FishingID, board, "", userID)
	if err != nil {
		return ErrInvariant
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_fishing_pond_counts(user_id,board,occurred_at,item_count) VALUES(?,?,?,?)
ON CONFLICT(user_id,board,occurred_at) DO UPDATE SET item_count=item_count+excluded.item_count`, userID, board, now, count); err != nil {
		return classifyDB(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO game_fishing_pond_daily(user_id,board,day_start,item_count,last_occurred_at) VALUES(?,?,?,?,?)
ON CONFLICT(user_id,board,day_start) DO UPDATE SET item_count=item_count+excluded.item_count,last_occurred_at=MAX(last_occurred_at,excluded.last_occurred_at)`, userID, board, pondDayStart(now), count, now); err != nil {
		return classifyDB(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_fishing_pond_totals(user_id,board,item_count,achieved_at,public_tie_key) VALUES(?,?,?,?,?)
ON CONFLICT(user_id,board) DO UPDATE SET item_count=item_count+excluded.item_count,achieved_at=excluded.achieved_at`, userID, board, count, now, tie[:])
	return classifyDB(err)
}

// ReturnPondOutcome uses the owned catch as its idempotency resource. A repeat
// returns the original receipt even after a later preference change.
func (service *Service) ReturnPondOutcome(ctx context.Context, userID int64, batchID string, ordinal int) (PondReturnReceipt, error) {
	if service == nil || service.closed.Load() {
		return PondReturnReceipt{}, ErrClosed
	}
	if ordinal < 0 || ordinal > 9 {
		return PondReturnReceipt{}, ErrInvalidRequest
	}
	result, err := service.returnPondOutcomes(ctx, userID, batchID, &ordinal)
	if err != nil {
		return PondReturnReceipt{}, err
	}
	return result.Receipts[0], nil
}

// ReturnPondBatch processes only this batch's eligible catches. Previously
// returned catches keep their original receipts; gifts and valuable fish stay.
func (service *Service) ReturnPondBatch(ctx context.Context, userID int64, batchID string) (PondBatchReturnReceipt, error) {
	return service.returnPondOutcomes(ctx, userID, batchID, nil)
}

func (service *Service) returnPondOutcomes(ctx context.Context, userID int64, batchID string, ordinal *int) (PondBatchReturnReceipt, error) {
	if service == nil || service.closed.Load() {
		return PondBatchReturnReceipt{}, ErrClosed
	}
	if userID <= 0 {
		return PondBatchReturnReceipt{}, ErrInvalidRequest
	}
	if !db.ValidateOpaqueID(batchID, "fb_") {
		return PondBatchReturnReceipt{}, ErrNotFound
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return PondBatchReturnReceipt{}, classifyDB(err)
	}
	defer tx.Rollback()
	if err = service.userAuthorizer.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return PondBatchReturnReceipt{}, mapAuthorization(err)
	}
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM game_fishing_batches WHERE id=? AND user_id=?`, batchID, userID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return PondBatchReturnReceipt{}, ErrNotFound
	}
	if err != nil {
		return PondBatchReturnReceipt{}, classifyDB(err)
	}
	if state != "committed" && ordinal == nil {
		return PondBatchReturnReceipt{}, ErrConflict
	}
	query := `SELECT o.ordinal,o.species_key,o.tier,o.payout_milli,p.returned_at,p.gift_species_key
FROM game_fishing_outcomes o JOIN game_fishing_pond_outcomes p ON p.batch_id=o.batch_id AND p.ordinal=o.ordinal
WHERE o.batch_id=?`
	args := []any{batchID}
	if ordinal != nil {
		query += " AND o.ordinal=?"
		args = append(args, *ordinal)
	}
	query += " ORDER BY o.ordinal LIMIT 10"
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return PondBatchReturnReceipt{}, classifyDB(err)
	}
	result := PondBatchReturnReceipt{BatchID: batchID, Receipts: make([]PondReturnReceipt, 0, 10)}
	type catch struct {
		receipt int
		species string
	}
	pending := make([]catch, 0, 10)
	for rows.Next() {
		var value PondReturnReceipt
		var species, tier string
		var payout int64
		var returned sql.NullInt64
		var gift sql.NullString
		if err = rows.Scan(&value.Ordinal, &species, &tier, &payout, &returned, &gift); err != nil {
			rows.Close()
			return PondBatchReturnReceipt{}, classifyDB(err)
		}
		if state != "committed" || tier != "junk" || payout != 0 || gift.Valid {
			if ordinal != nil {
				rows.Close()
				return PondBatchReturnReceipt{}, ErrConflict
			}
			continue
		}
		value.BatchID = batchID
		value.ReturnState = "returned"
		if species == "fry" {
			value.ReturnState = "released"
		}
		if returned.Valid {
			value.ReturnedAt = returned.Int64
		} else {
			pending = append(pending, catch{receipt: len(result.Receipts), species: species})
		}
		result.Receipts = append(result.Receipts, value)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return PondBatchReturnReceipt{}, classifyDB(err)
	}
	if err = rows.Close(); err != nil {
		return PondBatchReturnReceipt{}, classifyDB(err)
	}
	if ordinal != nil && len(result.Receipts) == 0 {
		return PondBatchReturnReceipt{}, ErrNotFound
	}
	if len(pending) == 0 {
		return result, nil
	}
	var identity rankIdentity
	var public int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(p.game_profile_public,u.game_profile_public),u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url
FROM users u LEFT JOIN game_user_preferences p ON p.user_id=u.id WHERE u.id=?`, userID).Scan(&public, &identity.Username, &identity.GuildNick, &identity.DiscordID, &identity.Avatar, &identity.GuildAvatar)
	if err != nil {
		return PondBatchReturnReceipt{}, classifyDB(err)
	}
	identity.Public = public == 1
	snapshot := projectIdentity(identity)
	if snapshot.Kind != "public" {
		return PondBatchReturnReceipt{}, ErrForbidden
	}
	insert, err := tx.PrepareContext(ctx, `INSERT INTO game_fishing_pond_queue(donor_user_id,source_batch_id,source_ordinal,species_key,donor_display_name,donor_avatar_url,returned_at) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return PondBatchReturnReceipt{}, classifyDB(err)
	}
	defer insert.Close()
	update, err := tx.PrepareContext(ctx, `UPDATE game_fishing_pond_outcomes SET returned_at=? WHERE batch_id=? AND ordinal=? AND returned_at IS NULL AND gift_species_key IS NULL`)
	if err != nil {
		return PondBatchReturnReceipt{}, classifyDB(err)
	}
	defer update.Close()
	now := service.now().UTC().Unix()
	var count int64
	for _, value := range pending {
		receipt := &result.Receipts[value.receipt]
		if value.species != "fry" {
			if _, err = insert.ExecContext(ctx, userID, batchID, receipt.Ordinal, value.species, snapshot.DisplayName, snapshot.AvatarURL, now); err != nil {
				return PondBatchReturnReceipt{}, classifyDB(err)
			}
			count++
		}
		changed, err := update.ExecContext(ctx, now, batchID, receipt.Ordinal)
		if err != nil {
			return PondBatchReturnReceipt{}, classifyDB(err)
		}
		affected, rowsErr := changed.RowsAffected()
		if rowsErr != nil || affected != 1 {
			return PondBatchReturnReceipt{}, ErrConflict
		}
		receipt.ReturnedAt = now
	}
	if err = service.addPondCountTx(ctx, tx, userID, "gifts", now, count); err != nil {
		return PondBatchReturnReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return PondBatchReturnReceipt{}, classifyDB(err)
	}
	return result, nil
}

func pondWindow(period string, now int64) (*int64, error) {
	var seconds int64
	switch period {
	case "7d":
		seconds = 7 * 24 * 60 * 60
	case "30d":
		seconds = 30 * 24 * 60 * 60
	case "all":
		return nil, nil
	default:
		return nil, ErrInvalidRequest
	}
	cutoff := now - seconds
	return &cutoff, nil
}

func (service *Service) PondLeaderboard(ctx context.Context, userID int64, board, period string) (_ PondLeaderboard, resultErr error) {
	defer func() {
		if errors.Is(resultErr, context.DeadlineExceeded) && ctx.Err() == nil {
			resultErr = ErrServiceUnavailable
		}
	}()
	if userID <= 0 || board != "cleanup" && board != "gifts" {
		return PondLeaderboard{}, ErrInvalidRequest
	}
	if period == "" {
		period = "7d"
	}
	now := service.now().UTC().Unix()
	window, err := pondWindow(period, now)
	if err != nil {
		return PondLeaderboard{}, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, rankBudget)
	defer cancel()
	tx, err := service.readDatabase.BeginTx(queryCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PondLeaderboard{}, classifyDB(err)
	}
	defer tx.Rollback()
	scores, args := pondScoresSQL(board, window, now, 0)
	args = append(args, now, userID)
	rows, err := tx.QueryContext(queryCtx, `WITH scores AS (`+scores+`), ranked AS (
SELECT s.user_id,s.item_count,ROW_NUMBER() OVER(ORDER BY s.item_count DESC,s.achieved_at ASC,s.public_tie_key ASC) AS rank,
CASE WHEN u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) THEN 0 ELSE COALESCE(p.game_profile_public,u.game_profile_public) END,
u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url
FROM scores s JOIN users u ON u.id=s.user_id LEFT JOIN game_user_preferences p ON p.user_id=u.id WHERE u.is_admin=0)
SELECT * FROM ranked WHERE rank<=20 OR user_id=? ORDER BY rank`, args...)
	if err != nil {
		return PondLeaderboard{}, classifyDB(err)
	}
	defer rows.Close()
	result := PondLeaderboard{Board: board, Period: period, WindowStart: window, Entries: []PondLeaderboardRow{}}
	for rows.Next() {
		var identity rankIdentity
		var count, rank int64
		var public int
		if err = rows.Scan(&identity.UserID, &count, &rank, &public, &identity.Username, &identity.GuildNick, &identity.DiscordID, &identity.Avatar, &identity.GuildAvatar); err != nil {
			return PondLeaderboard{}, classifyDB(err)
		}
		identity.Public = public == 1
		row := PondLeaderboardRow{Rank: strconv.FormatInt(rank, 10), ItemCount: strconv.FormatInt(count, 10), Identity: projectIdentity(identity), IsMe: identity.UserID == userID}
		if rank <= 20 {
			result.Entries = append(result.Entries, row)
		} else if row.IsMe {
			result.Me = &row
		}
	}
	return result, classifyDB(rows.Err())
}

func deletePondCountsTx(ctx context.Context, tx *sql.Tx, now int64, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM game_fishing_pond_counts WHERE (user_id,board,occurred_at) IN (
SELECT user_id,board,occurred_at FROM game_fishing_pond_counts WHERE occurred_at<=? ORDER BY occurred_at,user_id,board LIMIT ?)`, now-int64(rankWindow/time.Second), limit)
	if err != nil {
		return 0, classifyDB(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, classifyDB(err)
	}
	if count < int64(limit) {
		result, err = tx.ExecContext(ctx, `DELETE FROM game_fishing_pond_daily WHERE (user_id,board,day_start) IN (
SELECT user_id,board,day_start FROM game_fishing_pond_daily WHERE day_start<=? ORDER BY day_start,user_id,board LIMIT ?)`, pondDayStart(now-int64(rankWindow/time.Second))-pondDaySeconds, int64(limit)-count)
		if err != nil {
			return 0, classifyDB(err)
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return 0, classifyDB(err)
		}
		count += deleted
	}
	return int(count), nil
}

func (service *Service) cleanupPondCounts(ctx context.Context, now int64) (int, error) {
	var due bool
	cutoff := now - int64(rankWindow/time.Second)
	if err := service.readDatabase.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_fishing_pond_counts WHERE occurred_at<=?) OR EXISTS(SELECT 1 FROM game_fishing_pond_daily WHERE day_start<=?)`, cutoff, pondDayStart(cutoff)-pondDaySeconds).Scan(&due); err != nil {
		return 0, classifyDB(err)
	}
	if !due {
		return 0, nil
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, classifyDB(err)
	}
	defer tx.Rollback()
	count, err := deletePondCountsTx(ctx, tx, now, workerBatchSize)
	if err != nil {
		return 0, err
	}
	return count, classifyDB(tx.Commit())
}
