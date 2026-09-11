package host

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const RouteAdminGamesConfig = "/admin/api/games/config"
const RouteAdminActiveCounts = "/admin/api/games/active-counts"
const RouteGames = "/api/games"
const RouteHomeSummary = "/api/home/game-summary"

func (service *Service) ReadGamesConfig(ctx context.Context) (game.WireConfiguration, error) {
	if service == nil || ctx == nil {
		return game.WireConfiguration{}, ErrInvalidRequest
	}
	if service.closed.Load() {
		return game.WireConfiguration{}, ErrClosed
	}
	tx, err := service.services.Database.BeginTx(ctx, nil)
	if err != nil {
		return game.WireConfiguration{}, classifyDB(err)
	}
	defer tx.Rollback()
	snapshot, revision, err := service.readSnapshot(ctx, tx)
	if err != nil {
		return game.WireConfiguration{}, err
	}
	return snapshot.Wire(strconv.FormatInt(revision, 10)), nil
}

func (service *Service) PatchGamesConfig(ctx context.Context, body []byte, idempotencyKey string) (game.WireConfiguration, error) {
	if service == nil || ctx == nil {
		return game.WireConfiguration{}, ErrInvalidRequest
	}
	if service.closed.Load() {
		return game.WireConfiguration{}, ErrClosed
	}
	if service.services.AdminAuthorizer == nil {
		return game.WireConfiguration{}, ErrForbidden
	}
	patch, err := service.registry.DecodePatch(body)
	if err != nil {
		return game.WireConfiguration{}, ErrInvalidRequest
	}
	if _, err = idempotency.KeyHash(idempotencyKey); err != nil {
		return game.WireConfiguration{}, ErrInvalidRequest
	}
	canonical, err := idempotency.CanonicalJSON(patch)
	if err != nil {
		return game.WireConfiguration{}, ErrInvalidRequest
	}
	now := service.services.Now().UTC().Unix()
	tx, err := service.services.Database.BeginTx(ctx, nil)
	if err != nil {
		return game.WireConfiguration{}, classifyDB(err)
	}
	defer tx.Rollback()
	if err = service.services.AdminAuthorizer.AuthorizeAdminMutation(ctx, tx); err != nil {
		return game.WireConfiguration{}, mapAuthorization(err)
	}
	var adminUserID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&adminUserID); errors.Is(err, sql.ErrNoRows) {
		return game.WireConfiguration{}, ErrInvariant
	} else if err != nil {
		return game.WireConfiguration{}, classifyDB(err)
	} else if adminUserID <= 0 {
		return game.WireConfiguration{}, ErrInvariant
	}
	actorHash, err := idempotency.ActorScopeHash("admin", strconv.FormatInt(adminUserID, 10))
	if err != nil {
		return game.WireConfiguration{}, ErrInvariant
	}
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actorHash,
		Method:         http.MethodPatch,
		Route:          RouteAdminGamesConfig,
		Query:          "",
		Body:           canonical,
	})
	if err != nil {
		return game.WireConfiguration{}, ErrInvalidRequest
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope:       idempotency.ScopeControlMutation,
		ActorHash:   actorHash,
		Key:         idempotencyKey,
		RequestHash: requestHash,
		DecisionNow: now,
	})
	if err != nil {
		return game.WireConfiguration{}, mapIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		var replay game.WireConfiguration
		if decision.HTTPStatus != http.StatusOK || json.Unmarshal(decision.ResponseBody, &replay) != nil {
			return game.WireConfiguration{}, ErrInvariant
		}
		if err = tx.Commit(); err != nil {
			return game.WireConfiguration{}, classifyDB(err)
		}
		return replay, nil
	}
	snapshot, revision, err := service.readSnapshot(ctx, tx)
	if err != nil {
		return game.WireConfiguration{}, err
	}

	compiled, err := snapshot.Patch(patch, strconv.FormatInt(revision, 10))
	if errors.Is(err, game.ErrRevisionConflict) {
		return game.WireConfiguration{}, ErrConflict
	} else if err != nil {
		return game.WireConfiguration{}, ErrInvalidRequest
	}
	for _, descriptor := range service.registry.Descriptors() {
		value, _ := compiled.Value(descriptor.ID)
		if value.NeedsReady() && !service.modules[descriptor.ID].ReadyTx(ctx, tx) {
			return game.WireConfiguration{}, ErrServiceUnavailable
		}
	}
	raw := compiled.Raw()
	for key, value := range raw {
		result, writeErr := tx.ExecContext(ctx, `UPDATE site_config SET value=?,updated_at=? WHERE key=?`, value, now, key)
		if writeErr != nil {
			return game.WireConfiguration{}, classifyDB(writeErr)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return game.WireConfiguration{}, ErrInvariant
		}
	}
	if revision == math.MaxInt64 {
		return game.WireConfiguration{}, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE config_revisions SET revision=revision+1,updated_at=? WHERE domain='games' AND revision=?`, now, revision)
	if err != nil {
		return game.WireConfiguration{}, classifyDB(err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return game.WireConfiguration{}, ErrConflict
	}
	response := compiled.Wire(strconv.FormatInt(revision+1, 10))
	responseBody, err := json.Marshal(response)
	if err != nil {
		return game.WireConfiguration{}, ErrInvariant
	}
	if err = idempotency.Complete(ctx, tx, decision, http.StatusOK, responseBody); err != nil {
		return game.WireConfiguration{}, mapIdempotencyComplete(err)
	}
	if err = tx.Commit(); err != nil {
		return game.WireConfiguration{}, classifyDB(err)
	}
	return response, nil
}

func (service *Service) readSnapshot(ctx context.Context, tx *sql.Tx) (game.Configuration, int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM site_config WHERE key IN (`+placeholders(len(service.registry.ConfigKeys()))+`)`, stringArgs(service.registry.ConfigKeys())...)
	if err != nil {
		return game.Configuration{}, 0, classifyDB(err)
	}
	values := make(map[string]string, len(service.registry.ConfigKeys()))
	for rows.Next() {
		var key string
		var value sql.NullString
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return game.Configuration{}, 0, classifyDB(err)
		}
		if !value.Valid {
			rows.Close()
			return game.Configuration{}, 0, ErrInvariant
		}
		values[key] = value.String
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return game.Configuration{}, 0, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return game.Configuration{}, 0, classifyDB(err)
	}
	if len(values) != len(service.registry.ConfigKeys()) {
		return game.Configuration{}, 0, ErrInvariant
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM config_revisions WHERE domain='games'`).Scan(&revision); err != nil {
		return game.Configuration{}, 0, classifyDB(err)
	}
	snapshot, err := game.CompileConfiguration(service.registry, values)
	if err != nil {
		return game.Configuration{}, 0, ErrInvariant
	}
	return snapshot, revision, nil
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	result := "?"
	for index := 1; index < count; index++ {
		result += ",?"
	}
	return result
}

func stringArgs(values []string) []any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
}
