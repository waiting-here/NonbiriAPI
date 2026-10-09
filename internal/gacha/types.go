package gacha

import (
	"encoding/json"
	"errors"
)

const maxBalance = int64(9007199254740991)
const historyDays = 180

var (
	ErrConflict    = errors.New("gacha: conflict")
	ErrNotFound    = errors.New("gacha: not found")
	ErrCapacity    = errors.New("gacha: capacity")
	ErrExportLimit = errors.New("gacha: export limit")
)

type Rejection string

func (e Rejection) Error() string { return string(e) }

const (
	ErrTickets   Rejection = "tickets-insufficient"
	ErrCredits   Rejection = "credits-insufficient"
	ErrFragments Rejection = "fragments-insufficient"
	ErrMonthly   Rejection = "monthly-limit"
	ErrOwned     Rejection = "already-owned"
	ErrClaimed   Rejection = "already-claimed"
	ErrClosed    Rejection = "pool-closed"
	ErrPrice     Rejection = "price-changed"
	ErrTheme     Rejection = "theme-incomplete"
)

type Pool struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	NameEN    string     `json:"name_en,omitempty"`
	Cover     string     `json:"cover"`
	Kind      string     `json:"kind"`
	Group     string     `json:"group"`
	Rules     Rules      `json:"rules"`
	Featured  []string   `json:"featured"`
	StartsAt  *int64     `json:"starts_at"`
	EndsAt    *int64     `json:"ends_at"`
	Paused    bool       `json:"paused"`
	Published bool       `json:"published"`
	Revision  int64      `json:"revision"`
	Rates     [4]float64 `json:"rates"`
}

func (p Pool) Open(at int64) bool {
	return p.Kind == "permanent" || p.Published && !p.Paused && p.StartsAt != nil && p.EndsAt != nil && at >= *p.StartsAt && at < *p.EndsAt
}

type ShopSettings struct {
	PermanentPrice string `json:"permanent_price"`
	LimitedPrice   string `json:"limited_price"`
	NTicket        int64  `json:"n_ticket"`
	RTicket        int64  `json:"r_ticket"`
	SRTicket       int64  `json:"sr_ticket"`
	SSRTickets     int64  `json:"ssr_tickets"`
	PermanentCap   int64  `json:"permanent_cap"`
	LimitedCap     int64  `json:"limited_cap"`
	SRCard         int64  `json:"sr_card"`
	SSRCard        int64  `json:"ssr_card"`
	ChibiN         int64  `json:"chibi_n"`
	ChibiR         int64  `json:"chibi_r"`
	Revision       int64  `json:"revision"`
}

func defaultShop() ShopSettings {
	return ShopSettings{"10000", "10000", 20, 10, 5, 1, 20, 10, 10, 3, 30, 15, 1}
}

type Owned struct {
	At     int64  `json:"at"`
	Source string `json:"source"`
}
type Credits struct {
	General string `json:"general"`
	Game    string `json:"game"`
}
type Snapshot struct {
	Tickets    map[string]int64 `json:"tickets"`
	Fragments  map[Rarity]int64 `json:"fragments"`
	Credits    Credits          `json:"credits"`
	Owned      map[string]Owned `json:"owned"`
	Pity       map[string]Pity  `json:"pity"`
	Newcomer   bool             `json:"newcomer"`
	CheckedIn  bool             `json:"checked_in"`
	Claimed    []string         `json:"claimed"`
	Redeemed   []string         `json:"redeemed"`
	MonthUsed  map[string]int64 `json:"month_used"`
	Donation   string           `json:"donation"`
	Pools      []Pool           `json:"pools"`
	Shop       ShopSettings     `json:"shop"`
	ServerTime int64            `json:"server_time"`
	TimeZone   string           `json:"time_zone"`
}
type Action struct {
	Action   string `json:"action"`
	PoolID   string `json:"pool_id,omitempty"`
	Quantity int    `json:"quantity,omitempty"`
	Ticket   string `json:"ticket,omitempty"`
	Revision int64  `json:"revision,omitempty"`
	Product  string `json:"product,omitempty"`
	Theme    string `json:"theme,omitempty"`
}
type DrawReceipt struct {
	ID        string     `json:"id"`
	PoolID    string     `json:"pool_id"`
	PoolName  string     `json:"pool_name"`
	CreatedAt int64      `json:"created_at"`
	Items     []DrawItem `json:"items"`
}
type Outcome struct {
	Snapshot Snapshot     `json:"snapshot"`
	Draw     *DrawReceipt `json:"draw,omitempty"`
	Message  string       `json:"message,omitempty"`
}
type HistoryPage struct {
	Items      []DrawReceipt `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}
type Preset struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Pool     Pool   `json:"pool"`
	Revision int64  `json:"revision"`
}
type Configuration struct {
	Rules      Rules        `json:"rules"`
	Shop       ShopSettings `json:"shop"`
	Pools      []Pool       `json:"pools"`
	Presets    []Preset     `json:"presets"`
	Revision   int64        `json:"revision"`
	ServerTime int64        `json:"server_time"`
	TimeZone   string       `json:"time_zone"`
}
type AdminInput struct {
	Kind             string        `json:"kind"`
	ExpectedRevision int64         `json:"expected_revision"`
	Rules            *Rules        `json:"rules,omitempty"`
	Shop             *ShopSettings `json:"shop,omitempty"`
	Pool             *Pool         `json:"pool,omitempty"`
	Preset           *Preset       `json:"preset,omitempty"`
}

func jsonText(value any) string { b, _ := json.Marshal(value); return string(b) }
