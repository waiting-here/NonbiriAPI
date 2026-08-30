package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (service *Service) GamesSnapshot(ctx context.Context, userID int64, now time.Time) (game.GamesSnapshot, error) {
	if service == nil || service.closed.Load() || userID <= 0 {
		return game.GamesSnapshot{}, ErrInvalidRequest
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return game.GamesSnapshot{}, classifyDB(err)
	}
	defer tx.Rollback()
	snapshot, _, err := service.readSnapshot(ctx, tx)
	if err != nil {
		return game.GamesSnapshot{}, err
	}
	account, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		return game.GamesSnapshot{}, ErrNotFound
	}
	var tutorial int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT tutorial_rps_seen FROM game_user_preferences WHERE user_id=?),0)`, userID).Scan(&tutorial); err != nil {
		return game.GamesSnapshot{}, classifyDB(err)
	}
	result := game.GamesSnapshot{ServerNow: now.UTC().Unix(), Balance: formatWideMilli(account.Balance.Big()), TutorialRPSSeen: tutorial == 1, GamesEnabled: snapshot.GamesEnabled}
	fishingAvailable := service.available(game.FishingID, "", "")
	result.Fishing = game.FishingSnapshotModule{Enabled: snapshot.FishingEnabled, Available: fishingAvailable, BaitPrices: game.FishingBaitPrices{Worm: game.FormatAmount(mustRulesEntry(snapshot.Rules, fishing.BaitWorm)), Lure: game.FormatAmount(mustRulesEntry(snapshot.Rules, fishing.BaitLure)), Premium: game.FormatAmount(mustRulesEntry(snapshot.Rules, fishing.BaitPremium))}}
	linkAvailable := false
	for _, spec := range []string{game.LinkLinkSpec6x8, game.LinkLinkSpec8x8, game.LinkLinkSpec10x10} {
		linkAvailable = linkAvailable || service.available(game.LinkLinkID, "", spec)
	}
	result.LinkLink = game.LinkLinkSnapshotModule{Enabled: snapshot.LinkLink.Enabled && linkAvailable, Specs: make(map[string]game.LinkLinkWireSpec, 3)}
	seconds := map[string]int{game.LinkLinkSpec6x8: 150, game.LinkLinkSpec8x8: 180, game.LinkLinkSpec10x10: 240}
	for _, spec := range []string{game.LinkLinkSpec6x8, game.LinkLinkSpec8x8, game.LinkLinkSpec10x10} {
		value := snapshot.LinkLink.Specs[spec]
		result.LinkLink.Specs[spec] = game.LinkLinkWireSpec{Enabled: value.Enabled && service.available(game.LinkLinkID, "", spec), Price: game.FormatAmount(value.PriceMilli), Seconds: seconds[spec]}
	}
	rpsAvailable := false
	for _, mode := range []string{game.RPSModeQuick, game.RPSModeStandard, game.RPSModeDeathmatch} {
		rpsAvailable = rpsAvailable || service.available(game.RPSID, mode, "")
	}
	result.RPS = game.RPSSnapshotModule{Enabled: snapshot.RPS.Enabled && rpsAvailable, Modes: make(map[string]game.RPSWireMode, 3)}
	for _, mode := range []string{game.RPSModeQuick, game.RPSModeStandard, game.RPSModeDeathmatch} {
		value := snapshot.RPS.Modes[mode]
		result.RPS.Modes[mode] = game.RPSWireMode{Enabled: value.Enabled && service.available(game.RPSID, mode, ""), Base: game.FormatAmount(value.BaseMilli), PumpsBP: value.PumpsBP, QueueSeconds: value.QueueSeconds, GestureSeconds: value.GestureSeconds, DealerSeconds: value.DealerSeconds, FollowerSeconds: value.FollowerSeconds, QueueCapacity: game.RPSQueueCapacity}
	}
	return result, nil
}

func mustRulesEntry(rules *fishing.Ruleset, bait fishing.Bait) int64 {
	value, _ := rules.EntryMilli(bait)
	return value
}
func (service *Service) available(gameID, mode, spec string) bool {
	if gameID == game.FishingID && service.capability == nil {
		return true
	}
	return service.capability != nil && service.capability.Available(gameID, mode, spec)
}

func (service *Service) ReadGamesConfig(ctx context.Context) (game.GamesConfig, error) {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return game.GamesConfig{}, classifyDB(err)
	}
	defer tx.Rollback()
	snapshot, revision, err := service.readSnapshot(ctx, tx)
	if err != nil {
		return game.GamesConfig{}, err
	}
	return snapshot.GamesConfig(strconv.FormatInt(revision, 10)), nil
}

func (service *Service) PatchGamesConfig(ctx context.Context, body []byte, idempotencyKey string) (game.GamesConfig, error) {
	if service.adminAuthorizer == nil {
		return game.GamesConfig{}, ErrForbidden
	}
	patch, err := game.DecodeGamesConfigPatch(body)
	if err != nil {
		return game.GamesConfig{}, ErrInvalidRequest
	}
	if _, err = idempotency.KeyHash(idempotencyKey); err != nil {
		return game.GamesConfig{}, ErrInvalidRequest
	}
	canonical, err := idempotency.CanonicalJSON(patch)
	if err != nil {
		return game.GamesConfig{}, ErrInvalidRequest
	}
	now := service.now().UTC().Unix()
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return game.GamesConfig{}, classifyDB(err)
	}
	defer tx.Rollback()
	if err = service.adminAuthorizer.AuthorizeAdminMutation(ctx, tx); err != nil {
		return game.GamesConfig{}, mapAuthorization(err)
	}
	var adminUserID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&adminUserID); errors.Is(err, sql.ErrNoRows) {
		return game.GamesConfig{}, ErrInvariant
	} else if err != nil {
		return game.GamesConfig{}, classifyDB(err)
	} else if adminUserID <= 0 {
		return game.GamesConfig{}, ErrInvariant
	}
	actorHash, err := idempotency.ActorScopeHash("admin", strconv.FormatInt(adminUserID, 10))
	if err != nil {
		return game.GamesConfig{}, ErrInvariant
	}
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actorHash,
		Method:         http.MethodPatch,
		Route:          RouteAdminGamesConfig,
		Query:          "",
		Body:           canonical,
	})
	if err != nil {
		return game.GamesConfig{}, ErrInvalidRequest
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope:       idempotency.ScopeControlMutation,
		ActorHash:   actorHash,
		Key:         idempotencyKey,
		RequestHash: requestHash,
		DecisionNow: now,
	})
	if err != nil {
		return game.GamesConfig{}, mapIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		var replay game.GamesConfig
		if decision.HTTPStatus != http.StatusOK || json.Unmarshal(decision.ResponseBody, &replay) != nil {
			return game.GamesConfig{}, ErrInvariant
		}
		if err = tx.Commit(); err != nil {
			return game.GamesConfig{}, classifyDB(err)
		}
		return replay, nil
	}
	snapshot, revision, err := service.readSnapshot(ctx, tx)
	if err != nil {
		return game.GamesConfig{}, err
	}
	current := snapshot.GamesConfig(strconv.FormatInt(revision, 10))
	merged, err := patch.Merge(current)
	if errors.Is(err, game.ErrRevisionConflict) {
		return game.GamesConfig{}, ErrConflict
	} else if err != nil {
		return game.GamesConfig{}, ErrInvalidRequest
	}
	compiled, raw, err := game.CompileGamesConfig(merged)
	if err != nil {
		return game.GamesConfig{}, ErrInvalidRequest
	}
	for _, mode := range []string{game.RPSModeQuick, game.RPSModeStandard, game.RPSModeDeathmatch} {
		if compiled.RPS.Modes[mode].Enabled && (service.rpsHealth == nil || !service.rpsHealth.Ready(ctx, tx)) {
			return game.GamesConfig{}, ErrServiceUnavailable
		}
	}
	for key, value := range raw {
		result, writeErr := tx.ExecContext(ctx, `UPDATE site_config SET value=?,updated_at=? WHERE key=?`, value, now, key)
		if writeErr != nil {
			return game.GamesConfig{}, classifyDB(writeErr)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return game.GamesConfig{}, ErrInvariant
		}
	}
	if revision == math.MaxInt64 {
		return game.GamesConfig{}, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE config_revisions SET revision=revision+1,updated_at=? WHERE domain='games' AND revision=?`, now, revision)
	if err != nil {
		return game.GamesConfig{}, classifyDB(err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return game.GamesConfig{}, ErrConflict
	}
	response := compiled.GamesConfig(strconv.FormatInt(revision+1, 10))
	responseBody, err := json.Marshal(response)
	if err != nil {
		return game.GamesConfig{}, ErrInvariant
	}
	if err = idempotency.Complete(ctx, tx, decision, http.StatusOK, responseBody); err != nil {
		return game.GamesConfig{}, mapIdempotencyComplete(err)
	}
	if err = tx.Commit(); err != nil {
		return game.GamesConfig{}, classifyDB(err)
	}
	return response, nil
}

func (service *Service) ActiveCounts(ctx context.Context) (AdminActiveCounts, error) {
	result := AdminActiveCounts{Games: []AdminGameCount{}, Queues: []AdminQueueCount{}}
	var count int64
	if err := service.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_fishing_batches WHERE state='reserved'`).Scan(&count); err != nil {
		return result, classifyDB(err)
	}
	if count > 0 {
		result.Games = append(result.Games, AdminGameCount{Game: game.FishingID, Count: strconv.FormatInt(count, 10)})
	}
	rows, err := service.database.QueryContext(ctx, `SELECT spec,COUNT(*) FROM game_linklink_sessions GROUP BY spec ORDER BY spec`)
	if err != nil {
		return result, classifyDB(err)
	}
	for rows.Next() {
		var spec string
		if err = rows.Scan(&spec, &count); err != nil {
			rows.Close()
			return result, classifyDB(err)
		}
		copy := spec
		result.Games = append(result.Games, AdminGameCount{Game: game.LinkLinkID, Spec: &copy, Count: strconv.FormatInt(count, 10)})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return result, classifyDB(err)
	}
	rows.Close()
	rows, err = service.database.QueryContext(ctx, `SELECT mode,phase,COUNT(*) FROM game_rps_sessions GROUP BY mode,phase ORDER BY mode,phase`)
	if err != nil {
		return result, classifyDB(err)
	}
	for rows.Next() {
		var mode, phase string
		if err = rows.Scan(&mode, &phase, &count); err != nil {
			rows.Close()
			return result, classifyDB(err)
		}
		modeCopy, phaseCopy := mode, phase
		result.Games = append(result.Games, AdminGameCount{Game: game.RPSID, Mode: &modeCopy, Phase: &phaseCopy, Count: strconv.FormatInt(count, 10)})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return result, classifyDB(err)
	}
	rows.Close()
	rows, err = service.database.QueryContext(ctx, `SELECT mode,COUNT(*) FROM game_rps_queue GROUP BY mode ORDER BY mode`)
	if err != nil {
		return result, classifyDB(err)
	}
	for rows.Next() {
		var mode string
		if err = rows.Scan(&mode, &count); err != nil {
			rows.Close()
			return result, classifyDB(err)
		}
		result.Queues = append(result.Queues, AdminQueueCount{Mode: mode, Count: strconv.FormatInt(count, 10)})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return result, classifyDB(err)
	}
	rows.Close()
	return result, nil
}
