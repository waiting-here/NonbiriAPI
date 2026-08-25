// Package gameapi exposes the fishing user and administrator API.
// Authentication middleware is supplied by the integration rail; handlers
// still check station and principal kind on every request.
package gameapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
)

const (
	maxGameJSONBodyBytes = 64 << 10
	// These bounds apply to the complete JSON value, not only the fields the
	// endpoint eventually decodes. They keep malformed nested values bounded
	// before json.Unmarshal builds a second representation.
	maxGameJSONDepth  = 32
	maxGameJSONFields = 256
)

type HandlerDeps struct {
	Store      *db.Store
	Settlement *db.GameSettlementService
}

type Deps = HandlerDeps

type Handler struct {
	store      *db.Store
	settlement *db.GameSettlementService
	mux        *http.ServeMux
}

func NewHandler(deps HandlerDeps) http.Handler {
	h := &Handler{store: deps.Store, settlement: deps.Settlement, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /api/games", h.getGames)
	h.mux.HandleFunc("POST /api/games/fishing/rounds", h.startFishing)
	h.mux.HandleFunc("GET /api/games/fishing/state", h.getFishingState)
	h.mux.HandleFunc("POST /api/games/fishing/rounds/{round_id}/settle", h.settleFishing)
	h.mux.HandleFunc("POST /api/games/fishing/rounds/{round_id}/ack", h.ackFishing)
	h.mux.HandleFunc("GET /api/games/fishing/leaderboard", h.getLeaderboard)
	h.mux.HandleFunc("GET /admin/api/games/config", h.getGamesConfig)
	h.mux.HandleFunc("PATCH /admin/api/games/config", h.patchGamesConfig)
	return httpmw.API(h.mux)
}

func New(deps HandlerDeps) http.Handler { return NewHandler(deps) }

func (h *Handler) requireUser(w http.ResponseWriter, r *http.Request) (*db.User, bool) {
	if r == nil || httpmw.StationOf(r) != host.StationUser {
		writeGameErr(w, httperr.New(httperr.CodeForbidden, "station authorization required"))
		return nil, false
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok || user == nil || user.ID <= 0 {
		if _, anyPrincipal := auth.PrincipalFromContext(r.Context()); anyPrincipal {
			writeGameErr(w, httperr.New(httperr.CodeForbidden, "user authorization required"))
		} else {
			writeGameErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		}
		return nil, false
	}
	if h.store == nil {
		writeGameErr(w, httperr.New(httperr.CodeServiceUnavailable, "game service unavailable"))
		return nil, false
	}
	current, err := h.store.GetUserByID(user.ID)
	if err != nil {
		writeGameErr(w, httperr.New(httperr.CodeInternal, "game service unavailable"))
		return nil, false
	}
	if current == nil || !current.IsActive() {
		writeGameErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return nil, false
	}
	return current, true
}

func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) (*db.User, bool) {
	if r == nil || httpmw.StationOf(r) != host.StationAdmin {
		writeGameErr(w, httperr.New(httperr.CodeForbidden, "station authorization required"))
		return nil, false
	}
	admin, ok := auth.AdminFromContext(r.Context())
	if !ok || admin == nil || !admin.IsAdmin || admin.ID <= 0 {
		if _, anyPrincipal := auth.PrincipalFromContext(r.Context()); anyPrincipal {
			writeGameErr(w, httperr.New(httperr.CodeForbidden, "administrator authorization required"))
		} else {
			writeGameErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		}
		return nil, false
	}
	if h.store == nil {
		writeGameErr(w, httperr.New(httperr.CodeServiceUnavailable, "game service unavailable"))
		return nil, false
	}
	return admin, true
}

type gamesResponse struct {
	MasterEnabled     bool          `json:"master_enabled"`
	Credits           string        `json:"credits"`
	GameProfilePublic bool          `json:"game_profile_public"`
	Games             []gameSummary `json:"games"`
}

type gameSummary struct {
	ID      string        `json:"id"`
	Version int           `json:"version"`
	Enabled bool          `json:"enabled"`
	Params  fishingParams `json:"params"`
}

type fishingParams struct {
	Baits               []baitSummary   `json:"baits"`
	RTPPercent          rtpSummary      `json:"rtp_percent"`
	TreasureMultipliers treasureSummary `json:"treasure_multipliers"`
}

type baitSummary struct {
	ID    string `json:"id"`
	Price string `json:"price"`
}

type rtpSummary struct {
	Standard int `json:"standard"`
	Premium  int `json:"premium"`
}

type treasureSummary struct {
	Bottle int `json:"bottle"`
	Clover int `json:"clover"`
	Shell  int `json:"shell"`
}

func (h *Handler) getGames(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid query parameter"))
		return
	}
	snapshot, err := h.store.GetGameConfigSnapshot(r.Context())
	if err != nil {
		writeGameErr(w, httperr.New(httperr.CodeInternal, "game configuration unavailable"))
		return
	}
	wireUser, err := h.store.GetUserByID(user.ID)
	if err != nil || wireUser == nil {
		writeGameErr(w, httperr.New(httperr.CodeInternal, "game profile unavailable"))
		return
	}
	httperr.WriteJSON(w, http.StatusOK, gamesResponse{
		MasterEnabled: snapshot.GamesEnabled,
		Credits:       credits.FormatAmount(wireUser.Credits), GameProfilePublic: wireUser.GameProfilePublic,
		Games: []gameSummary{{ID: game.FishingID, Version: game.FishingVersion,
			Enabled: snapshot.GamesEnabled && snapshot.FishingEnabled,
			Params:  fishingParamsFromSnapshot(snapshot)}},
	})
}

func fishingParamsFromSnapshot(snapshot game.ConfigSnapshot) fishingParams {
	return fishingParams{
		Baits: []baitSummary{
			{ID: string(fishing.BaitWorm), Price: snapshot.Fishing.BaitPricesMilli[fishing.BaitWorm]},
			{ID: string(fishing.BaitLure), Price: snapshot.Fishing.BaitPricesMilli[fishing.BaitLure]},
			{ID: string(fishing.BaitPremium), Price: snapshot.Fishing.BaitPricesMilli[fishing.BaitPremium]},
		},
		RTPPercent: rtpSummary{Standard: snapshot.Fishing.StandardRTPPercent, Premium: snapshot.Fishing.PremiumRTPPercent},
		TreasureMultipliers: treasureSummary{
			Bottle: snapshot.Fishing.TreasureMultipliers["bottle"],
			Clover: snapshot.Fishing.TreasureMultipliers["clover"],
			Shell:  snapshot.Fishing.TreasureMultipliers["shell"],
		},
	}
}

type startFishingRequest struct {
	Bait string `json:"bait"`
}

type startFishingResponse struct {
	RoundID          string `json:"round_id"`
	GameID           string `json:"game_id"`
	GameVersion      int    `json:"game_version"`
	Bait             string `json:"bait"`
	Price            string `json:"price"`
	Credits          string `json:"credits"`
	State            string `json:"state"`
	CreatedAt        int64  `json:"created_at"`
	AutoSettleAt     int64  `json:"auto_settle_at"`
	IdempotentReplay bool   `json:"idempotent_replay"`
}

func (h *Handler) startFishing(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid query parameter"))
		return
	}
	if h.settlement == nil {
		writeGameErr(w, httperr.New(httperr.CodeServiceUnavailable, "game service unavailable"))
		return
	}
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !db.ValidGameIdempotencyKey(values[0]) {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid idempotency key"))
		return
	}
	var request startFishingRequest
	object, err := decodeStrictObject(r)
	if err != nil || len(object) != 1 {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	if err := unmarshalOnlyString(object, "bait", &request.Bait); err != nil {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	result, err := h.settlement.StartFishingRound(r.Context(), db.StartFishingInput{
		UserID: user.ID, Bait: fishing.Bait(request.Bait), IdempotencyKey: values[0],
	})
	if err != nil {
		writeGameRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, startFishingResponse{
		RoundID: result.RoundID, GameID: game.FishingID, GameVersion: game.FishingVersion,
		Bait: string(result.Bait), Price: credits.FormatAmount(result.EntryMilli),
		Credits: credits.FormatAmount(result.BalanceMilli), State: startWireState(result.State),
		CreatedAt: result.CreatedAt.Unix(), AutoSettleAt: result.AutoSettleAt.Unix(),
		IdempotentReplay: result.Replayed,
	})
}

func startWireState(state db.GameSettlementState) string {
	if state == db.GameReserved {
		return "pending"
	}
	return string(state)
}

type pendingRoundResponse struct {
	RoundID      string `json:"round_id"`
	Bait         string `json:"bait"`
	Price        string `json:"price"`
	CreatedAt    int64  `json:"created_at"`
	AutoSettleAt int64  `json:"auto_settle_at"`
}

type fishingResultResponse struct {
	RoundID          string `json:"round_id"`
	GameID           string `json:"game_id"`
	GameVersion      int    `json:"game_version"`
	Bait             string `json:"bait"`
	Price            string `json:"price"`
	SpeciesKey       string `json:"species_key"`
	Tier             string `json:"tier"`
	SizeCM           int    `json:"size_cm"`
	IsJunk           bool   `json:"is_junk"`
	IsTreasure       bool   `json:"is_treasure"`
	Meter            bool   `json:"meter"`
	CreditsWon       string `json:"credits_won"`
	Credits          string `json:"credits"`
	SettledAt        int64  `json:"settled_at"`
	IdempotentReplay *bool  `json:"idempotent_replay,omitempty"`
}

type fishingStateResponse struct {
	PendingRound      *pendingRoundResponse  `json:"pending_round"`
	UnrevealedResult  *fishingResultResponse `json:"unrevealed_result"`
	HasMoreUnrevealed bool                   `json:"has_more_unrevealed"`
}

func (h *Handler) getFishingState(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid query parameter"))
		return
	}
	state, err := h.store.GetFishingState(r.Context(), user.ID)
	if err != nil {
		writeGameRepoErr(w, err)
		return
	}
	response := fishingStateResponse{HasMoreUnrevealed: state.HasMoreUnrevealed}
	if state.Pending != nil {
		response.PendingRound = &pendingRoundResponse{RoundID: state.Pending.RoundID,
			Bait: string(state.Pending.Bait), Price: credits.FormatAmount(state.Pending.EntryMilli),
			CreatedAt: state.Pending.CreatedAt.Unix(), AutoSettleAt: state.Pending.AutoSettleAt.Unix()}
	}
	if state.Unrevealed != nil {
		response.UnrevealedResult, err = h.stateResultResponse(r.Context(), *state.Unrevealed)
		if err != nil {
			writeGameRepoErr(w, err)
			return
		}
	}
	httperr.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) settleFishing(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid query parameter"))
		return
	}
	if h.settlement == nil || !emptyBody(r) {
		if h.settlement == nil {
			writeGameErr(w, httperr.New(httperr.CodeServiceUnavailable, "game service unavailable"))
		} else {
			writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		}
		return
	}
	roundID := r.PathValue("round_id")
	if !db.ValidGameSettlementID(roundID) {
		writeGameErr(w, httperr.New(httperr.CodeNotFound, "round not found"))
		return
	}
	result, err := h.settlement.SettleFishingRound(r.Context(), db.SettleFishingInput{UserID: user.ID, RoundID: roundID})
	if err != nil {
		writeGameRepoErr(w, err)
		return
	}
	if result.State != db.GameCommitted || result.Outcome == nil {
		writeGameErr(w, httperr.New(httperr.CodeConflict, "round is not committed"))
		return
	}
	response, err := h.resultResponse(r.Context(), result, result.Replayed)
	if err != nil {
		writeGameRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) ackFishing(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid query parameter"))
		return
	}
	if !emptyBody(r) {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	roundID := r.PathValue("round_id")
	if !db.ValidGameSettlementID(roundID) {
		writeGameErr(w, httperr.New(httperr.CodeNotFound, "round not found"))
		return
	}
	if err := h.store.AckFishingRound(r.Context(), user.ID, roundID); err != nil {
		writeGameRepoErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) resultResponse(ctx context.Context, round db.FishingRound, replay bool) (*fishingResultResponse, error) {
	if round.Outcome == nil || round.State != db.GameCommitted || round.SettledAt == nil {
		return nil, db.ErrGameInternal
	}
	outcome := round.Outcome
	replayValue := replay
	return &fishingResultResponse{
		RoundID: round.RoundID, GameID: game.FishingID, GameVersion: game.FishingVersion,
		Bait: string(round.Bait), Price: credits.FormatAmount(round.EntryMilli),
		SpeciesKey: outcome.Key, Tier: string(outcome.Tier), SizeCM: outcome.SizeCentimetre,
		IsJunk: outcome.Tier == fishing.TierJunk, IsTreasure: outcome.Tier == fishing.TierTreasure,
		Meter:      outcome.Tier != fishing.TierJunk && outcome.Tier != fishing.TierTreasure && outcome.SizeCentimetre >= 100,
		CreditsWon: credits.FormatAmount(round.PayoutMilli), Credits: credits.FormatAmount(round.BalanceMilli),
		SettledAt: round.SettledAt.Unix(), IdempotentReplay: &replayValue,
	}, nil
}

func (h *Handler) stateResultResponse(ctx context.Context, round db.FishingRound) (*fishingResultResponse, error) {
	result, err := h.resultResponse(ctx, round, false)
	if err != nil {
		return nil, err
	}
	result.IdempotentReplay = nil
	return result, nil
}

type leaderboardResponse struct {
	Board       string                     `json:"board"`
	WindowStart *int64                     `json:"window_start"`
	Entries     []leaderboardEntryResponse `json:"entries"`
	Mine        *leaderboardEntryResponse  `json:"me"`
}

type leaderboardEntryResponse struct {
	Rank         int    `json:"rank"`
	SpeciesKey   string `json:"species_key,omitempty"`
	SizeCM       int    `json:"size_cm,omitempty"`
	TotalCredits string `json:"total_credits,omitempty"`
	DisplayName  string `json:"display_name,omitempty"`
	AvatarURL    string `json:"avatar_url,omitempty"`
	Level4Badge  bool   `json:"level4_badge,omitempty"`
	IsMe         bool   `json:"is_me"`
}

func (h *Handler) getLeaderboard(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	board, valid := strictBoardQuery(r)
	if !valid {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid query parameter"))
		return
	}
	data, err := h.store.ListFishingLeaderboard(r.Context(), user.ID, board, time.Now().UTC())
	if err != nil {
		writeGameRepoErr(w, err)
		return
	}
	response := leaderboardResponse{Board: string(data.Board), Entries: make([]leaderboardEntryResponse, 0, len(data.Entries))}
	if data.WindowStart != nil {
		value := data.WindowStart.Unix()
		response.WindowStart = &value
	}
	for _, entry := range data.Entries {
		projected, err := h.projectLeaderboardEntry(r.Context(), entry, board, user.ID)
		if err != nil {
			writeGameRepoErr(w, err)
			return
		}
		response.Entries = append(response.Entries, projected)
	}
	if data.Mine != nil {
		projected, err := h.projectLeaderboardEntry(r.Context(), *data.Mine, board, user.ID)
		if err != nil {
			writeGameRepoErr(w, err)
			return
		}
		response.Mine = &projected
	}
	httperr.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) projectLeaderboardEntry(ctx context.Context, entry db.FishingLeaderboardEntry, board db.FishingLeaderboardBoard, userID int64) (leaderboardEntryResponse, error) {
	if err := db.ValidateLeaderboardEntry(entry, board); err != nil {
		return leaderboardEntryResponse{}, err
	}
	response := leaderboardEntryResponse{Rank: entry.Rank, IsMe: entry.UserID == userID}
	if board == db.FishingLeaderboardSingle {
		response.SpeciesKey, response.SizeCM = entry.SpeciesKey, entry.SizeCM
	} else {
		response.TotalCredits = credits.FormatAmount(entry.TotalCreditsMilli)
	}
	if !entry.ProfilePublic {
		return response, nil
	}
	response.DisplayName = entry.GuildNick
	if response.DisplayName == "" {
		response.DisplayName = entry.Username
	}
	response.AvatarURL = safeLeaderboardAvatar(entry.DiscordID, entry.Avatar, entry.GuildAvatarURL)
	level := entry.AutoLevel
	if entry.ManualLevel != nil {
		level = *entry.ManualLevel
	} else if h.store != nil {
		resolved, err := h.store.ResolveEffectiveLevel(ctx, entry.UserID)
		if err != nil && !errors.Is(err, db.ErrLevelAdminExcluded) {
			return leaderboardEntryResponse{}, err
		}
		if err == nil {
			level = resolved
		}
	}
	response.Level4Badge = level >= db.LevelBanner+2
	return response, nil
}

type adminGamesConfigResponse struct {
	MasterEnabled bool               `json:"master_enabled"`
	Fishing       adminFishingConfig `json:"fishing"`
}

type adminFishingConfig struct {
	Enabled             bool            `json:"enabled"`
	BaitPrices          adminBaitPrices `json:"bait_prices"`
	RTPPercent          rtpSummary      `json:"rtp_percent"`
	TreasureMultipliers treasureSummary `json:"treasure_multipliers"`
}

type adminBaitPrices struct {
	Worm    string `json:"worm"`
	Lure    string `json:"lure"`
	Premium string `json:"premium"`
}

func adminConfigResponse(snapshot game.ConfigSnapshot) adminGamesConfigResponse {
	return adminGamesConfigResponse{MasterEnabled: snapshot.GamesEnabled, Fishing: adminFishingConfig{
		Enabled:             snapshot.FishingEnabled,
		BaitPrices:          adminBaitPrices{Worm: snapshot.Fishing.BaitPricesMilli[fishing.BaitWorm], Lure: snapshot.Fishing.BaitPricesMilli[fishing.BaitLure], Premium: snapshot.Fishing.BaitPricesMilli[fishing.BaitPremium]},
		RTPPercent:          rtpSummary{Standard: snapshot.Fishing.StandardRTPPercent, Premium: snapshot.Fishing.PremiumRTPPercent},
		TreasureMultipliers: treasureSummary{Bottle: snapshot.Fishing.TreasureMultipliers["bottle"], Clover: snapshot.Fishing.TreasureMultipliers["clover"], Shell: snapshot.Fishing.TreasureMultipliers["shell"]},
	}}
}

func (h *Handler) getGamesConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid query parameter"))
		return
	}
	snapshot, err := h.store.GetGameConfigSnapshot(r.Context())
	if err != nil {
		writeGameErr(w, httperr.New(httperr.CodeInternal, "game configuration unavailable"))
		return
	}
	httperr.WriteJSON(w, http.StatusOK, adminConfigResponse(snapshot))
}

func (h *Handler) patchGamesConfig(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid query parameter"))
		return
	}
	object, err := decodeStrictObject(r)
	if err != nil {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	changes, err := parseGamesConfigPatch(object)
	if err != nil || len(changes) == 0 {
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	if err := h.store.PatchGameConfig(r.Context(), admin.ID, changes, time.Now().UTC()); err != nil {
		if errors.Is(err, db.ErrInvalidSiteConfig) || errors.Is(err, db.ErrConflict) || errors.Is(err, db.ErrInvalidValue) {
			writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
			return
		}
		writeGameErr(w, httperr.New(httperr.CodeInternal, "game configuration unavailable"))
		return
	}
	snapshot, err := h.store.GetGameConfigSnapshot(r.Context())
	if err != nil {
		writeGameErr(w, httperr.New(httperr.CodeInternal, "game configuration unavailable"))
		return
	}
	httperr.WriteJSON(w, http.StatusOK, adminConfigResponse(snapshot))
}

func parseGamesConfigPatch(object map[string]json.RawMessage) (map[string]string, error) {
	if len(object) == 0 {
		return nil, errors.New("empty patch")
	}
	changes := make(map[string]string)
	for key, raw := range object {
		switch key {
		case "master_enabled":
			value, err := parseJSONBool(raw)
			if err != nil {
				return nil, err
			}
			changes[game.GamesEnabledKey] = boolStorage(value)
		case "fishing":
			nested, err := parseJSONObject(raw)
			if err != nil || len(nested) == 0 {
				return nil, errors.New("invalid fishing patch")
			}
			if err := parseFishingPatch(nested, changes); err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("unknown game config field")
		}
	}
	return changes, nil
}

func parseFishingPatch(object map[string]json.RawMessage, changes map[string]string) error {
	for key, raw := range object {
		switch key {
		case "enabled":
			value, err := parseJSONBool(raw)
			if err != nil {
				return err
			}
			changes[game.FishingEnabledKey] = boolStorage(value)
		case "bait_prices":
			nested, err := parseJSONObject(raw)
			if err != nil || len(nested) == 0 {
				return errors.New("invalid bait prices")
			}
			for name, amount := range nested {
				keyName := map[string]string{"worm": game.FishingWormPriceMilliKey, "lure": game.FishingLurePriceMilliKey, "premium": game.FishingPremiumPriceMilliKey}[name]
				if keyName == "" {
					return errors.New("unknown bait price")
				}
				value, err := parseJSONAmount(amount)
				if err != nil {
					return err
				}
				changes[keyName] = value
			}
		case "rtp_percent":
			nested, err := parseJSONObject(raw)
			if err != nil || len(nested) == 0 {
				return errors.New("invalid rtp")
			}
			for name, valueRaw := range nested {
				keyName := map[string]string{"standard": game.FishingStandardRTPKey, "premium": game.FishingPremiumRTPKey}[name]
				if keyName == "" {
					return errors.New("unknown rtp")
				}
				value, err := parseJSONInteger(valueRaw)
				if err != nil {
					return err
				}
				changes[keyName] = value
			}
		case "treasure_multipliers":
			nested, err := parseJSONObject(raw)
			if err != nil || len(nested) == 0 {
				return errors.New("invalid treasure multipliers")
			}
			for name, valueRaw := range nested {
				keyName := map[string]string{"bottle": game.FishingTreasureBottleMultiplierKey, "clover": game.FishingTreasureCloverMultiplierKey, "shell": game.FishingTreasureShellMultiplierKey}[name]
				if keyName == "" {
					return errors.New("unknown multiplier")
				}
				value, err := parseJSONInteger(valueRaw)
				if err != nil {
					return err
				}
				changes[keyName] = value
			}
		default:
			return errors.New("unknown fishing config field")
		}
	}
	return nil
}

func boolStorage(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func parseJSONBool(raw json.RawMessage) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("true")) {
		return true, nil
	}
	if bytes.Equal(trimmed, []byte("false")) {
		return false, nil
	}
	return false, errors.New("boolean required")
}

func parseJSONInteger(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || strings.ContainsAny(trimmed, ".eE") {
		return "", errors.New("integer required")
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || strconv.FormatInt(value, 10) != trimmed {
		return "", errors.New("integer required")
	}
	return trimmed, nil
}

func parseJSONAmount(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || !utf8.ValidString(value) {
		return "", errors.New("amount required")
	}
	parsed, err := credits.ParseAmount(value)
	if err != nil || parsed < 1 || credits.FormatAmount(parsed) != value {
		return "", errors.New("amount required")
	}
	return value, nil
}

func parseJSONObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("object required")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, errors.New("object required")
	}
	return object, nil
}

func decodeStrictObject(r *http.Request) (map[string]json.RawMessage, error) {
	if r == nil || r.Body == nil {
		return nil, errors.New("body required")
	}
	limited := io.LimitReader(r.Body, maxGameJSONBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) == 0 || len(body) > maxGameJSONBodyBytes {
		return nil, errors.New("body limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := scanJSONValue(decoder); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing json")
	}
	object, err := parseJSONObject(body)
	if err != nil {
		return nil, err
	}
	return object, nil
}

func scanJSONValue(decoder *json.Decoder) error {
	state := jsonScanState{}
	return scanJSONValueBounded(decoder, &state, 0)
}

type jsonScanState struct {
	depth  int
	fields int
}

func scanJSONValueBounded(decoder *json.Decoder, state *jsonScanState, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		if depth >= maxGameJSONDepth {
			return errors.New("json nesting limit")
		}
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("object key required")
				}
				if _, exists := seen[key]; exists {
					return errors.New("duplicate field")
				}
				seen[key] = struct{}{}
				state.fields++
				if state.fields > maxGameJSONFields {
					return errors.New("json field limit")
				}
				if err := scanJSONValueBounded(decoder, state, depth+1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("object terminator required")
			}
		case '[':
			for decoder.More() {
				if err := scanJSONValueBounded(decoder, state, depth+1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("array terminator required")
			}
		default:
			return errors.New("invalid json delimiter")
		}
	}
	return nil
}

func unmarshalOnlyString(object map[string]json.RawMessage, name string, target *string) error {
	raw, ok := object[name]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("field required")
	}
	if err := json.Unmarshal(raw, target); err != nil || *target == "" {
		return errors.New("string required")
	}
	return nil
}

func emptyBody(r *http.Request) bool {
	if r == nil || r.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxGameJSONBodyBytes+1))
	return err == nil && len(body) == 0
}

func strictBoardQuery(r *http.Request) (db.FishingLeaderboardBoard, bool) {
	if r == nil || r.URL == nil {
		return "", false
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", false
	}
	values := query["board"]
	if len(values) == 0 {
		if len(query) == 0 {
			return db.FishingLeaderboardSingle, true
		}
		return "", false
	}
	if len(values) != 1 || len(query) != 1 {
		return "", false
	}
	switch values[0] {
	case string(db.FishingLeaderboardSingle):
		return db.FishingLeaderboardSingle, true
	case string(db.FishingLeaderboardTotal):
		return db.FishingLeaderboardTotal, true
	default:
		return "", false
	}
}

func safeLeaderboardAvatar(discordID, rawHash, guildURL string) string {
	if value := safeGuildAvatar(discordID, guildURL); value != "" {
		return value
	}
	if !safeDiscordToken(discordID) || !safeDiscordToken(rawHash) {
		return ""
	}
	return "https://cdn.discordapp.com/avatars/" + discordID + "/" + rawHash + ".png?size=64"
}

func safeGuildAvatar(discordID, raw string) string {
	if raw == "" || !safeDiscordToken(discordID) {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "cdn.discordapp.com" || u.User != nil || u.Fragment != "" {
		return ""
	}
	if u.RawQuery != "size=64" || u.Path == "" || strings.Contains(u.Path, "%") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 6 || parts[0] != "guilds" || parts[2] != "users" || parts[3] != discordID || parts[4] != "avatars" {
		return ""
	}
	if !safeDiscordToken(parts[1]) {
		return ""
	}
	extension := ""
	switch {
	case strings.HasSuffix(parts[5], ".png"):
		extension = ".png"
	case strings.HasSuffix(parts[5], ".gif"):
		extension = ".gif"
	default:
		return ""
	}
	hash := strings.TrimSuffix(parts[5], extension)
	if !safeDiscordToken(hash) {
		return ""
	}
	return "https://cdn.discordapp.com/guilds/" + parts[1] + "/users/" + discordID + "/avatars/" + hash + ".png?size=64"
}

func safeDiscordToken(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, c := range value {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func writeGameRepoErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeGameErr(w, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, db.ErrGameDisabled):
		writeGameErr(w, httperr.New(httperr.CodeFeatureDisabled, "游戏暂未开放"))
	case errors.Is(err, db.ErrInsufficientCredits):
		writeGameErr(w, httperr.New(httperr.CodeInsufficientCredits, "悠哉积分不足"))
	case errors.Is(err, db.ErrGamePending), errors.Is(err, db.ErrConflict):
		writeGameErr(w, httperr.New(httperr.CodeConflict, "conflict"))
	case errors.Is(err, db.ErrInvalidValue), errors.Is(err, db.ErrGameInvalid):
		writeGameErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
	case errors.Is(err, db.ErrBanned), errors.Is(err, db.ErrAdminProtected):
		writeGameErr(w, httperr.New(httperr.CodeForbidden, "forbidden"))
	case isGameRateLimitError(err):
		writeGameRateLimit(w, err)
	default:
		writeGameErr(w, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

func isGameRateLimitError(err error) bool {
	var rate *db.GameRateLimitError
	return errors.As(err, &rate) && rate != nil
}

func writeGameRateLimit(w http.ResponseWriter, err error) {
	var rate *db.GameRateLimitError
	if !errors.As(err, &rate) || rate == nil {
		writeGameErr(w, httperr.New(httperr.CodeInternal, "internal error"))
		return
	}
	if !rate.Capacity {
		seconds := int(rate.RetryAfter / time.Second)
		if rate.RetryAfter%time.Second != 0 {
			seconds++
		}
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	writeGameErr(w, httperr.New(httperr.CodeRateLimited, "game start rate limited"))
}

func writeGameErr(w http.ResponseWriter, err httperr.Error) { httperr.WriteError(w, err) }
