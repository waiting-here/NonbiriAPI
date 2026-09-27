package duel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const loadoutCapacity = 10

type LoadoutItem struct {
	Slot      int             `json:"slot"`
	Revision  string          `json:"revision"`
	Mode      string          `json:"mode"`
	Loadout   json.RawMessage `json:"loadout"`
	UpdatedAt int64           `json:"updated_at"`
}

type LoadoutList struct {
	Capacity int           `json:"capacity"`
	Slots    []LoadoutItem `json:"slots"`
}

type SaveLoadoutInput struct {
	Identity
	IdempotencyKey   string
	Slot             int
	ExpectedRevision string
	Mode             string
	Loadout          json.RawMessage
}

type saveLoadoutBody struct {
	ExpectedRevision string          `json:"expected_revision"`
	Mode             string          `json:"mode"`
	Loadout          json.RawMessage `json:"loadout"`
}

type storedLoadout struct {
	Role    string   `json:"role"`
	Harness *string  `json:"harness"`
	Skills  []string `json:"skills"`
}

func parseLoadoutRevision(value string) (int64, error) {
	if value == "" || len(value) > 19 {
		return 0, ErrInvalidRequest
	}
	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			return 0, ErrInvalidRequest
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != value {
		return 0, ErrInvalidRequest
	}
	return n, nil
}

// LoadoutsTx reads the owner's private saved presets in slot order. The caller
// owns the transaction and its authorization boundary.
func (s *Service) LoadoutsTx(ctx context.Context, tx *sql.Tx, userID int64) ([]LoadoutItem, error) {
	if s == nil || s.rules.ID() != "likes" || tx == nil || userID <= 0 {
		return nil, ErrInvalidRequest
	}
	rows, err := tx.QueryContext(ctx, `SELECT slot,revision,mode,loadout_json,updated_at FROM game_likes_loadouts WHERE user_id=? ORDER BY slot`, userID)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	items := []LoadoutItem{}
	for rows.Next() {
		var item LoadoutItem
		var revision int64
		var raw []byte
		if err := rows.Scan(&item.Slot, &revision, &item.Mode, &raw, &item.UpdatedAt); err != nil {
			return nil, classify(err)
		}
		var decoded storedLoadout
		if item.Slot < 1 || item.Slot > loadoutCapacity || revision < 1 || len(raw) > 4096 || !HasFields(raw, "role", "harness", "skills") || Decode(raw, &decoded) != nil || decoded.Role == "" || len(decoded.Skills) < 1 || len(decoded.Skills) > 6 || s.descriptor.ResolveMode(item.Mode) != nil || item.UpdatedAt < 0 || item.UpdatedAt > maxDecisionTime {
			return nil, ErrInvariant
		}
		item.Revision = strconv.FormatInt(revision, 10)
		item.Loadout = append(json.RawMessage(nil), raw...)
		items = append(items, item)
		if len(items) > loadoutCapacity {
			return nil, ErrInvariant
		}
	}
	if err := rows.Err(); err != nil {
		return nil, classify(err)
	}
	return items, nil
}

func (s *Service) Loadouts(ctx context.Context, identity Identity) (LoadoutList, error) {
	if s == nil || s.rules.ID() != "likes" {
		return LoadoutList{}, ErrNotFound
	}
	tx, _, err := s.beginRead(ctx)
	if err != nil {
		return LoadoutList{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return LoadoutList{}, err
	}
	items, err := s.LoadoutsTx(ctx, tx, identity.UserID)
	return LoadoutList{Capacity: loadoutCapacity, Slots: items}, err
}

func (s *Service) SaveLoadout(ctx context.Context, in SaveLoadoutInput) (MutationResult, error) {
	if s == nil || s.rules.ID() != "likes" {
		return MutationResult{}, ErrNotFound
	}
	if in.Slot < 1 || in.Slot > loadoutCapacity || s.descriptor.ResolveMode(in.Mode) != nil {
		return MutationResult{}, ErrInvalidRequest
	}
	expected, err := parseLoadoutRevision(in.ExpectedRevision)
	if err != nil {
		return MutationResult{}, err
	}
	if _, err := idempotency.KeyHash(in.IdempotencyKey); err != nil {
		return MutationResult{}, ErrInvalidRequest
	}
	canonical, err := s.rules.Loadout(in.Mode, in.Loadout)
	if err != nil || len(canonical) > 4096 {
		return MutationResult{}, ErrInvalidRequest
	}
	body := saveLoadoutBody{ExpectedRevision: in.ExpectedRevision, Mode: in.Mode, Loadout: canonical}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, in.Identity); err != nil {
		return MutationResult{}, err
	}
	route := "/api/games/likes/loadouts/{slot}"
	decision, err := s.replay(ctx, tx, in.Identity, in.IdempotencyKey, http.MethodPut, route, strconv.Itoa(in.Slot), body, now)
	if err != nil {
		return MutationResult{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayResult(decision), nil
	}
	var revision, updatedAt int64
	err = tx.QueryRowContext(ctx, `SELECT revision,updated_at FROM game_likes_loadouts WHERE user_id=? AND slot=?`, in.UserID, in.Slot).Scan(&revision, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if expected != 0 {
			return MutationResult{}, ErrConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO game_likes_loadouts(user_id,slot,revision,mode,loadout_json,updated_at) VALUES(?,?,1,?,?,?)`, in.UserID, in.Slot, in.Mode, string(canonical), now)
		if err != nil {
			return MutationResult{}, classify(err)
		}
		revision, updatedAt = 1, now
	} else if err != nil {
		return MutationResult{}, classify(err)
	} else {
		if expected != revision || revision == math.MaxInt64 {
			return MutationResult{}, ErrConflict
		}
		if now > updatedAt {
			updatedAt = now
		}
		result, err := tx.ExecContext(ctx, `UPDATE game_likes_loadouts SET revision=?,mode=?,loadout_json=?,updated_at=? WHERE user_id=? AND slot=? AND revision=?`, revision+1, in.Mode, string(canonical), updatedAt, in.UserID, in.Slot, revision)
		if err != nil {
			return MutationResult{}, classify(err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return MutationResult{}, classify(err)
		}
		if affected != 1 {
			return MutationResult{}, ErrConflict
		}
		revision++
	}
	return finish(ctx, tx, decision, 200, LoadoutItem{Slot: in.Slot, Revision: strconv.FormatInt(revision, 10), Mode: in.Mode, Loadout: canonical, UpdatedAt: updatedAt})
}

func (s *Service) registerLoadoutRoutes(user resources.UserRouteRegistrar) error {
	if s.rules.ID() != "likes" {
		return nil
	}
	const base = "/api/games/likes/loadouts"
	if err := user.RegisterUserRoute(http.MethodGet, base, func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
		if r.URL.RawQuery != "" || !noBody(w, r) {
			if r.URL.RawQuery != "" {
				writeError(w, ErrInvalidRequest)
			}
			return
		}
		value, err := s.Loadouts(r.Context(), Identity{UserID: p.UserID})
		writeValue(w, value, err)
	}); err != nil {
		return err
	}
	return user.RegisterUserRoute(http.MethodPut, base+"/{slot}", func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
		if r.URL.RawQuery != "" {
			writeError(w, ErrInvalidRequest)
			return
		}
		slot, err := strconv.Atoi(r.PathValue("slot"))
		if err != nil || slot < 1 || slot > loadoutCapacity || strconv.Itoa(slot) != r.PathValue("slot") {
			writeError(w, ErrInvalidRequest)
			return
		}
		key, ok := requestKey(w, r)
		if !ok {
			return
		}
		var body saveLoadoutBody
		if !readJSON(w, r, &body) {
			return
		}
		if !HasFields(body.Loadout, "role", "harness", "skills") {
			writeError(w, ErrInvalidRequest)
			return
		}
		result, err := s.SaveLoadout(r.Context(), SaveLoadoutInput{Identity: Identity{UserID: p.UserID}, IdempotencyKey: key, Slot: slot, ExpectedRevision: body.ExpectedRevision, Mode: body.Mode, Loadout: body.Loadout})
		writeMutation(w, result, err)
	})
}
