package whalehaven

import (
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/assets"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/config"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/rules"
)

func (w *world) loadVisits(id int64) error {
	return w.visitRows(id, func(*visit) {})
}

// Both predicates use indexes. The transaction's cached visit wins over the
// database copy after a recall, new visit, or change of job.
func (w *world) visitRows(id int64, f func(*visit)) error {
	rows, err := w.tx.QueryContext(w.ctx, `SELECT guest_id,host_id,started_at,allow_hire,working,work_left_ms,eaten_grams,silver_eaten_grams FROM whale_haven_visits WHERE guest_id=? OR host_id=?`, id, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		v := &visit{}
		if err = rows.Scan(&v.guest, &v.host, &v.started, &v.allowHire, &v.working, &v.workLeft, &v.eaten, &v.silverEaten); err != nil {
			return err
		}
		if cached, ok := w.visits[v.guest]; ok {
			v = cached
		} else {
			w.visits[v.guest] = v
		}
		if !v.ended {
			f(v)
		}
	}
	return rows.Err()
}

// Only active relationships can affect a settlement. An explicit queue
// visits each participating home once, including a cycle of visiting whales.
func (w *world) participants(r *resident) ([]*resident, map[int64][]*visit, error) {
	incoming := map[int64][]*visit{}
	adjacent := map[int64][]*visit{}
	seenVisit := map[int64]bool{}
	add := func(v *visit) {
		if v.ended || seenVisit[v.guest] {
			return
		}
		seenVisit[v.guest] = true
		incoming[v.host] = append(incoming[v.host], v)
		adjacent[v.host] = append(adjacent[v.host], v)
		adjacent[v.guest] = append(adjacent[v.guest], v)
	}
	for _, v := range w.visits {
		add(v)
	}
	list := []*resident{r}
	seen := map[int64]bool{r.id: true}
	for i := 0; i < len(list); i++ {
		cur := list[i]
		if err := w.visitRows(cur.id, add); err != nil {
			return nil, nil, err
		}
		slices.SortFunc(incoming[cur.id], func(a, b *visit) int { return cmpID(a.guest, b.guest) })
		if len(incoming[cur.id]) > 2 {
			return nil, nil, errors.New("too many active whale visitors")
		}
		if cur.state.LastTickAt >= w.now {
			continue
		}
		for _, v := range adjacent[cur.id] {
			peer := v.guest
			if peer == cur.id {
				peer = v.host
			}
			if seen[peer] {
				continue
			}
			p, err := w.load(peer)
			if err != nil {
				return nil, nil, err
			}
			if !p.exists {
				return nil, nil, errors.New("missing whale resident")
			}
			seen[peer] = true
			list = append(list, p)
		}
	}
	slices.SortFunc(list, func(a, b *resident) int { return cmpID(a.id, b.id) })
	return list, incoming, nil
}

func cmpID(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func (w *world) settle(r *resident) error {
	if !r.exists {
		return nil
	}
	if r.state.LastTickAt >= w.now {
		return w.loadVisits(r.id)
	}
	people, incoming, err := w.participants(r)
	if err != nil {
		return err
	}
	from := r.state.LastTickAt
	for _, p := range people {
		from = min(from, p.state.LastTickAt)
		if p.state.Whale.Location.Kind == "visiting" {
			if v := w.visits[p.id]; v == nil || v.ended {
				p.state.Whale.Location = rules.Location{Kind: "home"}
				p.dirty = true
			}
		}
	}
	epochs, err := config.History(w.ctx, w.tx, from, w.now)
	if err != nil {
		return err
	}
	for i, e := range epochs {
		until := w.now
		if i+1 < len(epochs) {
			until = epochs[i+1].AtMS
		}
		for t := e.AtMS; t < until; {
			next, err := w.settleInterval(people, incoming, t, until, e)
			if err != nil {
				return err
			}
			if next <= t {
				return errors.New("whale settlement made no progress")
			}
			t = next
		}
	}
	return nil
}

type houseInterval struct {
	owner  *resident
	guests []*visit
	policy []rules.Guest
}

func (w *world) wageBalance(guestID int64) (float64, error) {
	if balance, ok := w.wages[guestID]; ok {
		return balance, nil
	}
	var balance float64
	err := w.tx.QueryRowContext(w.ctx, `SELECT microcoins FROM whale_haven_wage_fractions WHERE guest_id=?`, guestID).Scan(&balance)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	w.wages[guestID] = balance
	return balance, nil
}

func (w *world) settleInterval(people []*resident, incoming map[int64][]*visit, at, until int64, e config.Epoch) (int64, error) {
	cfg := e.Settings
	enabled := e.Master && cfg.Enabled
	end := until
	houses := make([]houseInterval, 0, len(people))
	for _, p := range people {
		if p.state.LastTickAt > at {
			end = min(end, p.state.LastTickAt)
			continue
		}
		h := houseInterval{owner: p}
		for _, v := range incoming[p.id] {
			if v.ended {
				continue
			}
			if v.started > at {
				end = min(end, v.started)
				continue
			}
			g := rules.Guest{Working: v.working, RemainingGrams: max(0, float64(cfg.Value("guest_full_grams"))-v.eaten)}
			if enabled {
				if v.working {
					guest := w.residents[v.guest]
					if v.workLeft <= 0 {
						if err := w.farewell(v, at, cfg, "shift_end"); err != nil {
							return 0, err
						}
						continue
					}
					if bp := cfg.Value("guest_wage_bp"); bp > 0 {
						if err := w.setDay(guest, w.day(at)); err != nil {
							return 0, err
						}
						room := wageRoom(guest, cfg)
						if room == 0 {
							if err := w.farewell(v, at, cfg, wageLimitReason(guest, cfg)); err != nil {
								return 0, err
							}
							continue
						}
						fraction, err := w.wageBalance(guest.id)
						if err != nil {
							return 0, err
						}
						g.MaxPearls = (float64(room) - fraction/1e6) * 10000 / float64(bp)
						end = min(end, w.midnight(at))
					}
					end = min(end, at+v.workLeft)
				} else if p.state.Home.TroughGrams == 0 && !p.state.CanRefill() {
					if err := w.farewell(v, at, cfg, "empty"); err != nil {
						return 0, err
					}
					continue
				} else if g.RemainingGrams <= 1e-8 {
					if err := w.farewell(v, at, cfg, "full"); err != nil {
						return 0, err
					}
					continue
				}
			}
			h.guests = append(h.guests, v)
			h.policy = append(h.policy, g)
		}
		houses = append(houses, h)
	}
	// A returning whale starts eating at its own home at the instant its
	// meal or shift ends. Preview only houses with guests to find that instant.
	if enabled {
		for _, h := range houses {
			if len(h.guests) == 0 {
				continue
			}
			copy := *h.owner.state
			copy.Inventory = maps.Clone(copy.Inventory)
			copy.Home.RecentFedHistory = slices.Clone(copy.Home.RecentFedHistory)
			contrib := copy.Advance(end, cfg, true, h.policy)
			for _, c := range contrib.Guests {
				if c.AtLimitMS > at {
					end = min(end, c.AtLimitMS)
				}
			}
		}
	}
	type departure struct {
		v      *visit
		reason string
	}
	var departures []departure
	for _, h := range houses {
		p := h.owner
		flow := p.state.Advance(end, cfg, enabled, h.policy)
		p.dirty = true
		if !enabled {
			continue
		}
		for i, v := range h.guests {
			g := w.residents[v.guest]
			c := flow.Guests[i]
			if v.working {
				v.workLeft = max(0, v.workLeft-(end-at))
				if err := w.payWage(p, g, c.Pearls, end, cfg); err != nil {
					return 0, err
				}
				if v.workLeft == 0 {
					departures = append(departures, departure{v, "shift_end"})
				} else if cfg.Value("guest_wage_bp") > 0 && wageRoom(g, cfg) == 0 {
					departures = append(departures, departure{v, wageLimitReason(g, cfg)})
				}
			} else {
				v.eaten += c.Grams
				v.silverEaten += c.SilverGrams
				g.state.Whale.Satiety = min(100, g.state.Whale.Satiety+c.Grams*2.25)
				g.state.Whale.Location.EatenGrams = v.eaten
				g.state.Whale.Location.PendingPearls, g.state.Whale.Location.PendingConchShards = mealRewards(v.eaten, cfg)
				if c.LeftEmpty {
					departures = append(departures, departure{v, "empty"})
				} else if v.eaten+1e-8 >= float64(cfg.Value("guest_full_grams")) {
					departures = append(departures, departure{v, "full"})
				}
			}
			g.dirty = true
		}
	}
	for _, d := range departures {
		if err := w.farewell(d.v, end, cfg, d.reason); err != nil {
			return 0, err
		}
	}
	return end, nil
}

func wageRoom(r *resident, cfg config.Settings) int64 {
	return min(r.state.SocialRoom(cfg), assets.MaxBalance-r.state.Wallet.Pearls)
}

func wageLimitReason(r *resident, cfg config.Settings) string {
	if r.state.SocialRoom(cfg) == 0 {
		return "quota"
	}
	return "wallet_full"
}

func (w *world) hiringLimit(guest *resident) (string, error) {
	if w.config.Value("guest_wage_bp") == 0 {
		return "", nil
	}
	if err := w.setDay(guest, w.day(w.now)); err != nil {
		return "", err
	}
	if wageRoom(guest, w.config) == 0 {
		return wageLimitReason(guest, w.config), nil
	}
	return "", nil
}

func (w *world) payWage(h, g *resident, gross float64, end int64, cfg config.Settings) error {
	bp := cfg.Value("guest_wage_bp")
	if bp == 0 || gross == 0 {
		return nil
	}
	old, err := w.wageBalance(g.id)
	if err != nil {
		return err
	}
	share := gross * float64(bp) / 10000
	h.state.ReserveWage(share)
	available := old + share*1e6
	paid := min(wageRoom(g, cfg), int64(math.Floor((available+1e-6)/1e6)))
	w.wages[g.id] = max(0, available-float64(paid)*1e6)
	if paid > 0 {
		g.state.Wallet.DailySocialPearlsGained += paid
		g.state.Wallet.Pearls += paid
		w.keepDay(g)
		w.remember(event{user: g.id, peer: h.id, at: end, kind: "wage", text: "客鲸帮工工资", movementReason: "wage", pearls: paid})
		w.remember(event{user: h.id, peer: g.id, at: end, kind: "wage", text: fmt.Sprintf("从帮工产出分给客鲸 %d 枚小鲸币", paid)})
	}
	return nil
}

func mealRewards(grams float64, cfg config.Settings) (int64, int64) {
	coins := int64(math.Floor((grams + 1e-9) / float64(cfg.Value("visit_grams_per_pearl"))))
	shards := int64(math.Floor((grams + 1e-9) / float64(cfg.Value("visit_grams_per_shard"))))
	return min(coins, cfg.Value("visit_max_pearls")), min(shards, cfg.Value("visit_max_shards"))
}

func (w *world) reward(r *resident, peer, at, coins, shards int64, cfg config.Settings, kind, text string) error {
	if err := w.setDay(r, w.day(at)); err != nil {
		return err
	}
	p, s := r.state.GrantSocial(coins, shards, at, cfg)
	wallet := &r.state.Wallet
	if overflow := wallet.Pearls - assets.MaxBalance; overflow > 0 {
		p -= overflow
		wallet.Pearls -= overflow
		wallet.DailySocialPearlsGained -= overflow
	}
	if overflow := wallet.ConchShards - assets.MaxBalance; overflow > 0 {
		s -= overflow
		wallet.ConchShards -= overflow
		wallet.DailyConchShardsGained -= overflow
	}
	w.keepDay(r)
	r.dirty = true
	w.remember(event{user: r.id, peer: peer, at: at, kind: kind, text: text, movementReason: "social_reward", pearls: p, shards: s})
	return nil
}

func (w *world) farewell(v *visit, at int64, cfg config.Settings, reason string) error {
	if v.ended {
		return nil
	}
	h, err := w.load(v.host)
	if err != nil {
		return err
	}
	g, err := w.load(v.guest)
	if err != nil {
		return err
	}
	v.ended = true
	h.dirty = true
	g.dirty = true
	g.state.Whale.Location = rules.Location{Kind: "home"}
	coins, shards := mealRewards(v.eaten, cfg)
	guestNote, hostNote := "小鲸回家，本次串门结束", "客鲸离开，本次串门结束"
	switch reason {
	case "recall":
		guestNote, hostNote = "你已召回小鲸，本次来往结算", "对方召回了客鲸，本次来往结算"
	case "gentle", "flick", "treat":
		guestNote, hostNote = "屋主送小鲸回家，本次来往结算", "你已送客鲸回家，本次来往结算"
	case "closed":
		guestNote, hostNote = "小筑关闭，小鲸回家结算", "小筑关闭，客鲸离开结算"
	case "shift_end":
		guestNote, hostNote = "班次到点，小鲸已收工回家", "班次到点，客鲸已收工回家"
	case "quota":
		guestNote, hostNote = "今日社交鲸币已达上限，小鲸收工回家", "客鲸今日社交鲸币已达上限，本次帮工结束"
	case "wallet_full":
		guestNote, hostNote = "小鲸币余额已满，小鲸收工回家", "客鲸无法继续收取工资，本次帮工结束"
	case "empty":
		guestNote, hostNote = "邻居的饭碗已空，小鲸回家", "饭碗已空，客鲸回家"
	case "full":
		guestNote, hostNote = "小鲸吃饱回家，本次串门结算", "客鲸吃饱离开，留下了谢礼"
	}
	if err = w.reward(g, h.id, at, coins, shards, cfg, "settlement", guestNote); err != nil {
		return err
	}
	if reason == "full" {
		return w.reward(h, g.id, at, 1, 1, cfg, "settlement", hostNote)
	}
	w.remember(event{user: h.id, peer: g.id, at: at, kind: "settlement", text: hostNote})
	return nil
}

func (w *world) socialAction(r *resident, a actionRequest) error {
	switch a.Type {
	case "set_open":
		if a.Open == nil {
			return invalid("请选择小屋是否开放。")
		}
		if r.open == *a.Open {
			return nil
		}
		if !*a.Open {
			if err := w.loadVisits(r.id); err != nil {
				return err
			}
			for _, v := range w.visits {
				if !v.ended && (v.host == r.id || v.guest == r.id) {
					if err := w.farewell(v, w.now, w.config, "closed"); err != nil {
						return err
					}
				}
			}
		}
		r.open = *a.Open
		r.dirty = true
	case "send_visit":
		return w.sendVisit(r, a)
	case "recall_home":
		v := w.visits[r.id]
		if v == nil || v.ended {
			return conflict("小鲸已经在家了。")
		}
		return w.farewell(v, w.now, w.config, "recall")
	case "catch_guest":
		return w.catchGuest(r, a)
	case "toggle_pin_neighbor":
		door := strings.TrimSpace(a.NeighborID)
		// A once-open favorite can still be removed after its door closes.
		row, err := w.tx.ExecContext(w.ctx, `DELETE FROM whale_haven_pins WHERE owner_id=? AND peer_id=(SELECT user_id FROM whale_haven_profiles WHERE door=?)`, r.id, door)
		if err != nil {
			return err
		}
		n, err := row.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			peer, err := w.accessible(door)
			if err != nil {
				return err
			}
			if peer.id == r.id {
				return invalid("不能收藏自己的门牌。")
			}
			if _, err = w.tx.ExecContext(w.ctx, "INSERT INTO whale_haven_pins(owner_id,peer_id) VALUES(?,?)", r.id, peer.id); err != nil {
				return err
			}
		}
		r.dirty = true
	default:
		return invalid("不支持这项串门操作。")
	}
	return nil
}

func (w *world) sendVisit(g *resident, a actionRequest) error {
	if !g.open {
		return conflict("先开放自己的小屋再串门吧。")
	}
	preset, err := visitMessage("visit", a.PresetID)
	if err != nil {
		return err
	}
	h, err := w.accessible(a.Target)
	if err != nil {
		return err
	}
	if h.id == g.id {
		return invalid("不能串门到自己家。")
	}
	if err = w.settle(h); err != nil {
		return err
	}
	if existing := w.visits[g.id]; existing != nil && !existing.ended {
		return conflict("小鲸正在串门，先召回她吧。")
	}
	if g.state.Home.TroughGrams > 0 && g.state.Whale.Satiety >= float64(w.config.Value("visit_satiety_threshold")) {
		return conflict("家里有粮，小鲸吃饱后想在家休息。")
	}
	if h.state.Home.TroughGrams <= 0 {
		return conflict("这户饭碗里已经没有余粮了。")
	}
	count := 0
	for _, v := range w.visits {
		if !v.ended && v.host == h.id {
			count++
		}
	}
	if count >= 2 {
		return conflict("这户的两个客位已经坐满了。")
	}
	w.visits[g.id] = &visit{guest: g.id, host: h.id, started: w.now, allowHire: a.AllowHire != nil && *a.AllowHire}
	g.state.Whale.Location = rules.Location{Kind: "visiting", NeighborID: h.door, StartedAt: w.now}
	g.state.Whale.IsSleeping = false
	g.state.Whale.SleepingSince = 0
	g.state.Whale.TempEmotion = nil
	g.dirty = true
	h.dirty = true
	w.remember(event{user: g.id, peer: h.id, at: w.now, kind: "visit_out", text: "小鲸出发串门" + preset})
	w.remember(event{user: h.id, peer: g.id, at: w.now, kind: "guest_in", text: "客鲸来小屋串门" + preset})
	return nil
}

func (w *world) catchGuest(h *resident, a actionRequest) error {
	if !h.open {
		return errUnavailable
	}
	g, err := w.accessible(a.NeighborID)
	if err != nil {
		return err
	}
	v := w.visits[g.id]
	if v == nil || v.ended || v.host != h.id {
		return conflict("这只客鲸已经离开了。")
	}
	preset, err := visitMessage(a.Action, a.PresetID)
	if err != nil {
		return err
	}
	cfg := w.config
	if v.working && a.Action != "gentle" {
		return conflict("客鲸正在工作，可先结束班次。")
	}
	var text string
	var coins, shards int64
	switch a.Action {
	case "hire":
		if !v.allowHire {
			return conflict("客鲸主人没有同意本次打工。")
		}
		limit, err := w.hiringLimit(g)
		if err != nil {
			return err
		}
		switch limit {
		case "wallet_full":
			return conflict("客鲸暂时无法继续收取工资。")
		case "quota":
			return conflict("客鲸主人今日的社交鲸币已达上限。")
		}
		v.working = true
		v.workLeft = cfg.Value("guest_shift_seconds") * 1000
		boost(h, "hire", cfg)
		text = "客鲸开始了打工班次"
	case "gentle":
		if !v.working {
			boost(h, "gentle", cfg)
		}
		text = "已送客鲸回家"
	case "flick":
		food := v.silverEaten
		if h.state.Home.TroughFeedType == "sweet_shrimp" {
			food = max(0, v.eaten-v.silverEaten)
		}
		recover := min(max(0, float64(cfg.Value("trough_grams"))-h.state.Home.TroughGrams), food*float64(cfg.Value("flick_recover_bp"))/10000)
		h.state.Home.TroughGrams += recover
		v.eaten = max(0, v.eaten-recover)
		g.state.Whale.Satiety = max(0, g.state.Whale.Satiety-2.25*recover)
		coins, shards = cfg.Value("flick_pearls"), cfg.Value("flick_shards")
		text = fmt.Sprintf("抓住偷吃的客鲸，收回 %.1f 克同种口粮", recover)
	case "treat":
		meal := float64(cfg.Value("treat_cost_grams"))
		if h.state.Home.TroughGrams < meal {
			return conflict("饭碗余粮不足，添饭后再招待客鲸吧。")
		}
		h.state.Home.TroughGrams -= meal
		v.eaten += meal
		g.state.Whale.Satiety = min(100, g.state.Whale.Satiety+2.25*meal)
		boost(h, "treat", cfg)
		coins, shards = cfg.Value("treat_pearls"), cfg.Value("treat_shards")
		if slices.Contains(h.state.Home.EquippedDecor, "coral_tea") {
			shards += cfg.Value("tea_bonus_shards")
		}
		text = fmt.Sprintf("分出 %.0f 克口粮招待客鲸", meal)
	default:
		return invalid("请选择有效的客鲸互动。")
	}
	if err = w.reward(h, g.id, w.now, coins, shards, cfg, "catch", text+preset); err != nil {
		return err
	}
	w.remember(event{user: g.id, peer: h.id, at: w.now, kind: "catch", text: text + preset})
	if a.Action == "treat" {
		h.state.RecordSharedMeal(w.now, float64(cfg.Value("treat_cost_grams")))
	}
	g.dirty = true
	h.dirty = true
	if a.Action != "hire" {
		return w.farewell(v, w.now, cfg, a.Action)
	}
	return nil
}

func boost(r *resident, prefix string, cfg config.Settings) {
	r.state.Whale.Mood = min(100, r.state.Whale.Mood+float64(cfg.Value(prefix+"_mood")))
	r.state.Whale.Affection += min(cfg.Value(prefix+"_affection"), math.MaxInt64-r.state.Whale.Affection)
}

func visitMessage(action, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	if text, ok := presetMessages[id]; ok && strings.HasPrefix(id, action+"_") {
		return " · " + text, nil
	}
	return "", invalid("请从本次互动的预设口信中选择。")
}

var presetMessages = map[string]string{
	"gentle_1": "我家电饭煲也快见底啦，快顺着网线回你家饲养员那要白米饭吧～",
	"gentle_2": "蹭饱半肚子就乖乖游回本机节点吧，路上别撞上丢包漩涡哦！",
	"gentle_3": "摸摸头不打你，嘴角还粘着我家大米粒呢，快游回去擦擦嘴吧～",
	"gentle_4": "连接超时警告！再不回家你家饲养员要以为鲸鱼娘丢包啦！",
	"gentle_5": "今天白米饭配额刚好够我家这只干饭鲸，下次煮双倍再喊你来炫！",
	"flick_1":  "抓包现行！偷炫我家白米饭被弹了脑瓜崩，快领回去拿饭团哄哄！",
	"flick_2":  "你家蓝色大肥鱼把小肚皮都吃圆啦，扣下两枚小鲸币抵米饭钱！",
	"flick_3":  "叮！检测到不明干饭进程入侵青花碗，已执行物理脑瓜崩中断！",
	"flick_4":  "头顶呆毛都弹歪咯！谁让你家小馋鲸连锅巴都要刮干净的！",
	"flick_5":  "人赃并获！抱着碗跑得比光纤还快，掉出来的两枚小鲸币归我啦～",
	"treat_1":  "看她捧着空碗眼泪汪汪太可爱了，特意多盛了一大勺白米饭加 Token 请她吃！",
	"treat_2":  "悠哉数据海一家亲，慢点炫别噎着，欢迎常顺着网线来串门～",
	"treat_3":  "看把孩子饿得小尾巴都蔫了，再加一颗海苔饭团，吃饱了再游回去！",
	"treat_4":  "已开启「算力与碳水共享协议」，两只小鲸鱼排排坐干饭香翻倍～",
	"treat_5":  "投喂成功！你家鲸鱼娘吃得腮帮子鼓鼓的，像只圆滚滚的蓝海豹～",
	"hire_1":   "看你家小馋鲸这么能炫白米饭，特聘留店穿工作服帮忙干饭攒币，下班结小鲸币工资！",
	"hire_2":   "临时干饭专员已上岗！管饱大碗白米饭，产出的小鲸币按劳发薪捎回去～",
	"hire_3":   "抓壮丁咯！白吃白喝可不行，换上打工小围裙帮我家存钱罐冲个业绩再回家！",
	"hire_4":   "恭喜贵鲸通过《深海碳水转化工程师》面试，包吃包住不加班，下班带薪回港～",
	"visit_1":  "咕噜噜……我家鲸鱼娘饿报服务器繁忙了，顺着网线来借碗白米饭，多谢款待！",
	"visit_2":  "路过贵节点顺便趴着摸会儿鱼，你家深海小筑布置得真好看！",
	"visit_3":  "叮咚！检测到贵屋飘出极品大米香，我家小馋鲸自动寻路游过来啦～",
	"visit_4":  "俗话说远亲不如近邻节点，借贵宝地青花碗炫两口，回头请你吃小饭团！",
	"visit_5":  "我家电饭煲还在读条中，先派小鲸鱼来贵海湾挂机蹭口热乎的～",
}
