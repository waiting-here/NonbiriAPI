package whalehaven

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/assets"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/rules"
)

type actionRequest struct {
	Type             string `json:"type"`
	Name             string `json:"name,omitempty"`
	Quantity         *int64 `json:"quantity,omitempty"`
	FeedID           string `json:"feedId,omitempty"`
	DecorID          string `json:"decorId,omitempty"`
	OutfitID         string `json:"outfitId,omitempty"`
	Zone             string `json:"zone,omitempty"`
	Streak           int64  `json:"streak,omitempty"`
	Key              string `json:"key,omitempty"`
	Enabled          *bool  `json:"enabled,omitempty"`
	ForceSleep       *bool  `json:"forceSleep,omitempty"`
	Open             *bool  `json:"open,omitempty"`
	AllowHire        *bool  `json:"allowHire,omitempty"`
	Target           string `json:"target,omitempty"`
	NeighborID       string `json:"neighborId,omitempty"`
	Action           string `json:"action,omitempty"`
	PresetID         string `json:"presetId,omitempty"`
	ExpectedProfile  string `json:"expected_profile_revision"`
	ExpectedSettings string `json:"expected_settings_revision"`
}
type exchangeRequest struct {
	Quantity         string `json:"quantity"`
	ExpectedSettings string `json:"expected_settings_revision,omitempty"`
	ExpectedProfile  string `json:"expected_profile_revision,omitempty"`
}

func decodeBody(r *http.Request, dest any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if err != nil || len(raw) > 8192 || strictjson.ValidateObject(raw) != nil {
		return invalid("请求内容无效。")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(dest) != nil {
		return invalid("请检查请求中的字段。")
	}
	return nil
}
func count(s string, zero bool) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || (!zero && n == 0) || key(n) != s {
		return 0, invalid("数量或版本必须是有效的整数。")
	}
	return n, nil
}
func writeResponse(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func writeFailure(w http.ResponseWriter, err error) {
	p := &problem{"internal_error", "未能完成这次操作，请稍后再试。"}
	status := 500
	var e *problem
	switch {
	case errors.As(err, &e):
		p = e
		status = 400
		switch p.code {
		case "conflict":
			status = 409
		case "feature_disabled":
			status = 403
		case "not_found":
			status = 404
		}
	case errors.Is(err, authz.ErrUnauthorized):
		p = &problem{"unauthorized", "请重新登录。"}
		status = 401
	case errors.Is(err, authz.ErrForbidden):
		p = &problem{"forbidden", "当前账号无法执行此操作。"}
		status = 403
	case errors.Is(err, ledger.ErrInsufficientBalance):
		p = &problem{"insufficient_balance", "游戏积分不足，请调整购买数量。"}
		status = 409
	case errors.Is(err, assets.ErrCapacity):
		p = &problem{"resource_limit", "代币余额已到上限。"}
		status = 409
	default:
		slog.Error("Whale Haven request failed", "error", err)
	}
	writeResponse(w, status, map[string]any{"error": map[string]string{"code": p.code, "message": p.text}})
}

func (s *Service) Register(reg resources.UserRouteRegistrar) error {
	const prefix = "/api/games/whale-haven/"
	for _, route := range []struct{ method, path string }{{"GET", "availability"}, {"GET", "profile"}, {"GET", "neighbors/search"}, {"GET", "neighbors/house"}, {"POST", "exchange/quote"}, {"POST", "exchange"}, {"POST", "actions"}} {
		if err := reg.RegisterUserRoute(route.method, prefix+route.path, func(out http.ResponseWriter, req *http.Request, user resources.UserPrincipal) {
			value, err := s.handle(req, user.UserID, route.path)
			if err != nil {
				writeFailure(out, err)
				return
			}
			writeResponse(out, 200, value)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) handle(req *http.Request, id int64, route string) (any, error) {
	ctx := req.Context()
	isWrite := route == "actions" || route == "exchange"
	mutateDB := s.store.ReadDB()
	if isWrite {
		mutateDB = s.store.DB()
	}
	var action actionRequest
	var exchange exchangeRequest
	var lots int64
	var err error
	switch route {
	case "actions":
		if err = decodeBody(req, &action); err != nil {
			return nil, err
		}
	case "exchange", "exchange/quote":
		if err = decodeBody(req, &exchange); err != nil {
			return nil, err
		}
		if lots, err = count(exchange.Quantity, route == "exchange/quote"); err != nil {
			return nil, err
		}
	}
	tx, err := mutateDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: !isWrite})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if isWrite {
		if err = s.auth.AuthorizeUserMutation(ctx, tx, id); err != nil {
			return nil, err
		}
	}
	if route == "availability" {
		var enabled, exchange bool
		err := tx.QueryRowContext(ctx, `SELECT (SELECT value='1' FROM site_config WHERE key='games_enabled') AND COALESCE(json_extract(doc,'$.enabled'),0),
        COALESCE(json_extract(doc,'$.exchange_enabled'),0) FROM (SELECT (SELECT document FROM whale_haven_settings ORDER BY revision DESC LIMIT 1) AS doc)`).Scan(&enabled, &exchange)
		return map[string]bool{"enabled": enabled, "exchange_enabled": enabled && exchange}, err
	}
	w, err := openWorld(ctx, tx, s.now().UnixMilli())
	if errors.Is(err, db.ErrTimezoneUnavailable) {
		return nil, &problem{"feature_disabled", "鲸鱼湾尚未开放。管理员配置站点时区后可启用。"}
	}
	if err != nil {
		return nil, err
	}
	on := w.master && w.config.Enabled
	var kh, hash [32]byte
	var operation string
	if isWrite {
		if len(req.Header.Values("Idempotency-Key")) != 1 {
			return nil, invalid("请为本次操作提供一个幂等键。")
		}
		kh, err = idempotency.KeyHash(req.Header.Get("Idempotency-Key"))
		if err != nil {
			return nil, invalid("缺少或无法识别这次操作的幂等键。")
		}
		var input any = action
		if route == "exchange" {
			input = exchange
		}
		packed, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		hash = sha256.Sum256(append([]byte(route+"\n"), packed...))
		var previous []byte
		err = tx.QueryRowContext(ctx, "SELECT request_hash,operation_id FROM whale_haven_receipts WHERE user_id=? AND key_hash=? AND created_at>?", id, kh[:], w.now/1000-idempotency.ReplayWindowSeconds).Scan(&previous, &operation)
		if err == nil {
			if !bytes.Equal(hash[:], previous) {
				return nil, conflict("此操作号已用于另一项操作。")
			}
			r, e := w.load(id)
			if e != nil {
				return nil, e
			}
			if e = w.settle(r); e != nil {
				return nil, e
			}
			return w.profile(r, operation)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if !on {
		return nil, errClosed
	}
	r, err := w.load(id)
	if err != nil {
		return nil, err
	}
	if route == "neighbors/search" || route == "neighbors/house" {
		if !r.exists || !r.open {
			return nil, conflict("开放自己的小屋后才能查看邻居。")
		}
		query := req.URL.Query()
		if route == "neighbors/house" {
			return w.neighborHouse(r.id, query.Get("door"))
		}
		items, e := w.neighbors(r, query.Get("q"), query.Get("filter"))
		return map[string]any{"items": items}, e
	}
	if isWrite {
		observedProfile, observedSettings := action.ExpectedProfile, action.ExpectedSettings
		if route == "exchange" {
			observedProfile, observedSettings = exchange.ExpectedProfile, exchange.ExpectedSettings
		}
		if _, err = count(observedProfile, false); err != nil {
			return nil, err
		}
		if _, err = count(observedSettings, false); err != nil {
			return nil, err
		}
		if observedSettings != key(w.configRevision) {
			return nil, conflict("小筑的运营设置已有调整，本次操作未执行。请核对最新规则后重试。")
		}
		if observedProfile != key(r.revision) {
			return nil, conflict("小筑状态有变化，本次操作未执行。请核对最新的来往和余额后重试。")
		}
	}
	if r.exists {
		if err = w.settle(r); err != nil {
			return nil, err
		}
	}
	if err = w.setDay(r, w.day(w.now)); err != nil {
		return nil, err
	}
	switch route {
	case "profile":
		return w.profile(r, "")
	case "exchange/quote":
		q, e := w.quote(r, lots)
		if e != nil {
			return nil, e
		}
		return w.quoteView(r, q), nil
	}
	if operation, err = db.GenerateOpaqueID("op_"); err != nil {
		return nil, err
	}
	var facts activities.PublishFacts
	switch route {
	case "actions":
		err = s.apply(w, r, action, operation)
	case "exchange":
		facts, err = s.cashOut(w, r, lots, operation)
	default:
		return nil, errUnavailable
	}
	if err != nil {
		return nil, err
	}
	if err = db.FreezeSiteTimezoneTx(ctx, tx, w.now/1000); err != nil {
		return nil, err
	}
	reason := action.Type
	if route == "exchange" {
		reason = "exchange"
	}
	if err = w.save(reason, operation); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM whale_haven_receipts WHERE user_id=? AND created_at<=?", id, w.now/1000-idempotency.ReplayWindowSeconds)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO whale_haven_receipts(user_id,key_hash,request_hash,operation_id,created_at) VALUES(?,?,?,?,?)", id, kh[:], hash[:], operation, w.now/1000)
	if err != nil {
		return nil, err
	}
	// Projection reads the just-written event rows. Drop the in-memory copies to avoid duplicate timeline entries.
	w.events = nil
	value, err := w.profile(r, operation)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.pools.PublishCommittedFacts(ctx, facts)
	return value, nil
}

func (s *Service) apply(w *world, r *resident, a actionRequest, op string) error {
	if a.Type == "adopt" {
		if r.exists {
			return conflict("已经领养过小鲸。")
		}
		if a.Open == nil {
			return invalid("请选择是否开放小屋。")
		}
		var err error
		r.door, err = newDoor()
		if err != nil {
			return err
		}
		r.open = *a.Open
		_, err = r.state.Apply(rules.Action{Type: "rename", Name: a.Name}, w.now, w.config)
		if err != nil {
			return invalid(err.Error())
		}
		r.dirty = true
		w.remember(event{user: r.id, at: w.now, kind: "interact", text: "领养小鲸，入住小屋。"})
		return nil
	}
	if !r.exists {
		return conflict("先给小鲸取名并领养，再开始游戏。")
	}
	switch a.Type {
	case "set_open", "send_visit", "recall_home", "catch_guest", "toggle_pin_neighbor":
		return w.socialAction(r, a)
	}
	quantity := int64(1)
	if a.Quantity != nil {
		if a.Type != "buy_feed" || *a.Quantity < 1 || *a.Quantity > 99 {
			return invalid("每次可购买 1 至 99 份口粮。")
		}
		quantity = *a.Quantity
	}
	result, err := r.state.Apply(rules.Action{Type: a.Type, FeedID: a.FeedID, Quantity: quantity, DecorID: a.DecorID, OutfitID: a.OutfitID, Name: a.Name, Zone: a.Zone, Streak: a.Streak, Key: a.Key, Enabled: a.Enabled, ForceSleep: a.ForceSleep}, w.now, w.config)
	if err != nil {
		return invalid(err.Error())
	}
	if result.GameCostMilli > 0 {
		if err = purchase(w, r, op, result.GameCostMilli); err != nil {
			return err
		}
	}
	r.dirty = true
	category, note := "interact", ""
	switch a.Type {
	case "buy_feed", "use_feed":
		category = "feed"
		food := map[string]string{"silver_fish": "热腾腾大碗白米饭", "sweet_shrimp": "精炼 Token 盖浇白米饭", "berry_teacake": "海苔流心小饭团"}[a.FeedID]
		if a.Type == "buy_feed" {
			note = "购入口粮 · " + noteCount(food, quantity)
		} else {
			note = "投喂 · " + food
		}
	case "harvest_pearls":
		if result.Harvested > 0 {
			note = noteCount("收取小鲸币", result.Harvested)
		}
		category = "harvest"
	case "spin_conch_gacha":
		category = "gacha"
		note = "海螺扭蛋 · " + result.Reward
	case "select_outfit":
		note = "更换衣装"
	case "toggle_decor":
		note = "布置小屋"
	case "rename":
		note = "为小鲸取名"
	case "toggle_auto_refill":
		note = "调整自动续碗"
	case "activate_aroma":
		note = "使用增香包"
	}
	if strings.TrimSpace(note) != "" {
		w.remember(event{user: r.id, at: w.now, kind: category, text: note})
	}
	return nil
}
