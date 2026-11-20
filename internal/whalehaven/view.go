package whalehaven

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

const peerFrom = ` FROM whale_haven_profiles h JOIN users u ON u.id=h.user_id LEFT JOIN game_user_preferences pref ON pref.user_id=u.id `
const visiblePeer = ` (u.is_banned=0 OR (u.banned_until IS NOT NULL AND u.banned_until<=?)) AND NOT EXISTS(SELECT 1 FROM user_deletion_markers d WHERE d.user_id=u.id) `
const publicProfile = `COALESCE(pref.game_profile_public,u.game_profile_public)`

func (w *world) accessible(door string) (*resident, error) {
	var id int64
	err := w.tx.QueryRowContext(w.ctx, "SELECT h.user_id"+peerFrom+"WHERE h.door=? AND h.is_open=1 AND "+visiblePeer, door, w.now/1000).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errUnavailable
	}
	if err != nil {
		return nil, err
	}
	r, err := w.load(id)
	if err == nil && !r.open {
		return nil, errUnavailable
	}
	return r, err
}

type neighbor struct {
	account   int64
	UpdatedAt int64    `json:"updatedAt"`
	ID        string   `json:"id"`
	UID       int64    `json:"uid,omitempty"`
	Username  string   `json:"discordUsername,omitempty"`
	Nickname  string   `json:"publicNickname"`
	Door      string   `json:"anonymousCode"`
	Public    bool     `json:"gameProfilePublic"`
	Name      string   `json:"whaleName"`
	Outfit    string   `json:"whaleOutfit"`
	Title     string   `json:"oceanTitle"`
	Grams     float64  `json:"troughGrams"`
	Feed      string   `json:"troughFeedType"`
	Satiety   float64  `json:"whaleSatiety"`
	AtHome    bool     `json:"whaleAtHome"`
	Decor     []string `json:"equippedDecor"`
	Pinned    bool     `json:"pinned"`
}

func (n neighbor) label() string {
	if n.Nickname != "" {
		return n.Nickname
	}
	return "门牌 " + n.Door
}

const peerSelect = `SELECT h.user_id,h.door,` + publicProfile + `,u.username,COALESCE(NULLIF(u.guild_nick,''),u.username),h.whale_name,
 json_extract(h.snapshot,'$.whale.outfit'),json_extract(h.snapshot,'$.home.troughGrams'),json_extract(h.snapshot,'$.home.troughFeedType'),
 json_extract(h.snapshot,'$.whale.satiety'),NOT EXISTS(SELECT 1 FROM whale_haven_visits v WHERE v.guest_id=h.user_id),
 json_extract(h.snapshot,'$.home.equippedDecor'),p.peer_id IS NOT NULL,h.updated_at` + peerFrom +
	`LEFT JOIN whale_haven_pins p ON p.peer_id=h.user_id AND p.owner_id=? WHERE h.is_open=1 AND ` + visiblePeer

func (w *world) scanPeer(row interface{ Scan(...any) error }) (neighbor, error) {
	var n neighbor
	var id int64
	var name, nick, decor string
	err := row.Scan(&id, &n.Door, &n.Public, &name, &nick, &n.Name, &n.Outfit, &n.Grams, &n.Feed, &n.Satiety, &n.AtHome, &decor, &n.Pinned, &n.UpdatedAt)
	if err != nil {
		return n, err
	}
	n.account = id
	n.ID = n.Door
	if n.Public {
		n.Nickname = nick
		n.UID = id
		n.Username = name
	}
	if err = json.Unmarshal([]byte(decor), &n.Decor); err != nil {
		return n, err
	}
	if r := w.residents[id]; r != nil {
		n.project(r)
	}
	return n, nil
}
func (n *neighbor) project(r *resident) {
	n.Name = r.state.Whale.Name
	n.Outfit = r.state.Whale.Outfit
	n.AtHome = r.state.Whale.Location.Kind == "home"
	n.Grams = r.state.Home.TroughGrams
	n.Feed = r.state.Home.TroughFeedType
	n.Satiety = r.state.Whale.Satiety
	n.Decor = r.state.Home.EquippedDecor
	n.UpdatedAt = r.state.LastTickAt
}

func (w *world) neighbors(r *resident, q, filter string) ([]neighbor, error) {
	q = strings.TrimSpace(q)
	if !utf8.ValidString(q) || utf8.RuneCountInString(q) > 64 {
		return nil, invalid("搜索词过长。")
	}
	query := peerSelect + " AND h.user_id<>?"
	args := []any{r.id, w.now / 1000, r.id}
	switch filter {
	case "", "all":
	case "pinned":
		query += " AND p.peer_id IS NOT NULL"
	case "has_food":
		query += " AND (json_extract(h.snapshot,'$.home.troughGrams')>0 OR (json_extract(h.snapshot,'$.home.buffs.autoRefillEnabled')=1 AND json_extract(h.snapshot,'$.home.buffs.autoRefillTickets')>0))"
	case "visited":
		query += " AND EXISTS(SELECT 1 FROM whale_haven_footprints f WHERE f.user_id=? AND f.peer_id=h.user_id)"
		args = append(args, r.id)
	default:
		return nil, invalid("请选择有效的邻居筛选。")
	}
	if q != "" {
		query += " AND (instr(lower(h.door),lower(?))>0 OR instr(lower(h.whale_name),lower(?))>0 OR (" + publicProfile +
			"=1 AND (instr(lower(u.username),lower(?))>0 OR instr(lower(u.guild_nick),lower(?))>0 OR CAST(u.id AS TEXT)=?)))"
		args = append(args, q, q, q, q, q)
	}
	rows, err := w.tx.QueryContext(w.ctx, query+" ORDER BY h.door LIMIT 32", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []neighbor{}
	for rows.Next() {
		n, e := w.scanPeer(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, n)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if filter == "has_food" {
		available := items[:0]
		for _, n := range items {
			p, e := w.load(n.account)
			if e != nil {
				return nil, e
			}
			if e = w.settle(p); e != nil {
				return nil, e
			}
			n.project(p)
			if n.Grams > 0 {
				available = append(available, n)
			}
		}
		return available, nil
	}
	return items, nil
}

func (w *world) profile(r *resident, operation string) (map[string]any, error) {
	if err := w.setDay(r, w.day(w.now)); err != nil {
		return nil, err
	}
	var nickname string
	var public bool
	err := w.tx.QueryRowContext(w.ctx, "SELECT COALESCE(NULLIF(u.guild_nick,''),u.username),"+publicProfile+" FROM users u LEFT JOIN game_user_preferences pref ON pref.user_id=u.id WHERE u.id=?", r.id).Scan(&nickname, &public)
	if err != nil {
		return nil, err
	}
	general, err := ledger.UserAccount(w.ctx, w.tx, r.id)
	if err != nil {
		return nil, err
	}
	game, err := ledger.UserAssetAccount(w.ctx, w.tx, r.id, ledger.Game)
	if err != nil {
		return nil, err
	}
	wage, err := w.wageBalance(r.id)
	if err != nil {
		return nil, err
	}
	wallet := r.state.Wallet
	snap := map[string]any{"revision": r.revision, "lastTickAt": r.state.LastTickAt, "playerUid": r.id, "playerNickname": nickname, "playerAnonymousCode": r.door, "gameProfilePublic": public,
		"wallet": map[string]any{"pearls": r.original.Pearls, "conchShards": r.original.Shards, "pendingSocialPearls": wallet.Pearls - r.original.Pearls, "pendingConchShards": wallet.ConchShards - r.original.Shards, "wageProgress": wage / 1e6, "siteDayKey": wallet.SiteDayKey, "dailyPearlExchanged": wallet.DailyPearlExchanged,
			"dailySocialPearlsGained": wallet.DailySocialPearlsGained, "dailyConchShardsGained": wallet.DailyConchShardsGained, "generalMilli": general.Balance.Decimal(), "gameMilli": game.Balance.Decimal()},
		"inventory": r.state.Inventory, "whale": r.state.Whale, "home": r.state.Home}
	peers := []neighbor{}
	pins := []string{}
	guestViews := []map[string]any{}
	visitors := []map[string]any{}
	var firstGuest any
	var away any
	if r.exists && r.open {
		if err = w.loadVisits(r.id); err != nil {
			return nil, err
		}
		// Only the people in this home or hosting this whale need full detail on entry.
		// Search, including favourites, remains a separate interaction.
		ids := []int64{}
		for _, v := range w.visits {
			if !v.ended && (v.guest == r.id || v.host == r.id) {
				id := v.guest
				if id == r.id {
					id = v.host
				}
				if !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			n, e := w.scanPeer(w.tx.QueryRowContext(w.ctx, peerSelect+" AND h.user_id=?", r.id, w.now/1000, id))
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				return nil, e
			}
			peers = append(peers, n)
			if v := w.visits[r.id]; v != nil && !v.ended && v.host == id {
				away = map[string]any{"target": n.ID, "label": n.label(), "allow_hire": v.allowHire, "working": v.working, "started_at_ms": v.started}
			}
			if v := w.visits[id]; v != nil && !v.ended && v.host == r.id {
				stage := "eating"
				if v.working {
					stage = "working"
				}
				g := map[string]any{"slotIndex": len(guestViews), "neighborId": n.ID, "arrivedAt": v.started, "eatenGrams": v.eaten, "stage": stage}
				if v.working {
					g["workExpiresAt"] = w.now + v.workLeft
				}
				guestViews = append(guestViews, g)
				visitors = append(visitors, map[string]any{"neighbor_id": n.ID, "label": n.label(), "whale_name": n.Name, "whale_outfit": n.Outfit, "allow_hire": v.allowHire, "working": v.working, "started_at_ms": v.started})
			}
		}
		rows, e := w.tx.QueryContext(w.ctx, "SELECT h.door"+peerFrom+" JOIN whale_haven_pins p ON p.peer_id=h.user_id WHERE h.is_open=1 AND p.owner_id=? AND "+visiblePeer+" ORDER BY h.door LIMIT 64", r.id, w.now/1000)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var door string
			if e = rows.Scan(&door); e != nil {
				rows.Close()
				return nil, e
			}
			pins = append(pins, door)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	if len(guestViews) > 0 {
		firstGuest = guestViews[0]
	}
	snap["neighbors"] = peers
	snap["pinnedNeighborIds"] = pins
	snap["guestVisits"] = guestViews
	snap["guestVisit"] = firstGuest
	logs, prints, err := w.timeline(r)
	if err != nil {
		return nil, err
	}
	snap["logs"] = logs
	snap["footprints"] = prints
	cfg := w.config
	cfg.Enabled = cfg.Enabled && w.master
	cfg.ExchangeEnabled = cfg.Enabled && cfg.ExchangeEnabled
	value := map[string]any{"snapshot": snap, "settings": cfg, "settings_revision": key(w.configRevision), "timezone_offset_minutes": w.offset,
		"social": map[string]any{"open": r.open, "door": r.door, "accept_work": w.visits[r.id] != nil && !w.visits[r.id].ended && w.visits[r.id].allowHire, "guests": visitors, "away": away}}
	if operation != "" {
		value["operation_id"] = operation
	}
	return value, nil
}

func (w *world) timeline(r *resident) ([]map[string]any, map[string]any, error) {
	logs := []map[string]any{}
	prints := map[string]any{}
	if !r.exists {
		return logs, prints, nil
	}
	rows, err := w.tx.QueryContext(w.ctx, "SELECT id,created_at,kind,text,pearls,shards FROM whale_haven_events WHERE user_id=? ORDER BY id DESC LIMIT 40", r.id)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id, at int64
		var kind, note string
		var pearls, shards int64
		if err = rows.Scan(&id, &at, &kind, &note, &pearls, &shards); err != nil {
			rows.Close()
			return nil, nil, err
		}
		logs = append(logs, map[string]any{"id": key(id), "timestamp": at, "kind": kind, "title": note, "detail": movementDetail(pearls, shards)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(w.events) > 0 {
		upcoming := make([]map[string]any, 0, len(w.events))
		for i := len(w.events) - 1; i >= 0 && len(upcoming) < 40; i-- {
			e := w.events[i]
			if e.user != r.id {
				continue
			}
			label := "待结算 · " + e.text
			upcoming = append(upcoming, map[string]any{"id": "pending-" + key(e.at) + "-" + key(int64(i)), "timestamp": e.at, "kind": e.kind, "title": label, "detail": movementDetail(e.pearls, e.shards)})
		}
		logs = append(upcoming, logs...)
		logs = logs[:min(len(logs), 40)]
	}
	if !r.open {
		return logs, prints, nil
	}
	rows, err = w.tx.QueryContext(w.ctx, "SELECT h.door,f.outgoing,f.incoming,f.last_at,f.last_summary"+peerFrom+" JOIN whale_haven_footprints f ON f.peer_id=h.user_id WHERE h.is_open=1 AND f.user_id=? AND "+visiblePeer+" ORDER BY f.last_at DESC LIMIT 32", r.id, w.now/1000)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var door, note string
		var out, in, at int64
		if err = rows.Scan(&door, &out, &in, &at, &note); err != nil {
			return nil, nil, err
		}
		prints[door] = map[string]any{"neighborId": door, "ourVisitsCount": out, "theirVisitsCount": in, "lastInteractionAt": at, "lastSummary": note}
	}
	return logs, prints, rows.Err()
}

func movementDetail(pearls, shards int64) string {
	var parts []string
	for _, item := range []struct {
		name  string
		value int64
	}{{"小鲸币", pearls}, {"海螺碎片", shards}} {
		if item.value != 0 {
			sign := ""
			if item.value > 0 {
				sign = "+"
			}
			parts = append(parts, item.name+" "+sign+key(item.value))
		}
	}
	return strings.Join(parts, " · ")
}
