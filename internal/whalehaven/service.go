// Package whalehaven owns the shared, server-authoritative Whale Haven game.
package whalehaven

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/assets"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/config"
	"github.com/waiting-here/NonbiriAPI/internal/whalehaven/rules"
)

type problem struct {
	code string
	text string
}

func (p *problem) Error() string { return p.text }
func invalid(text string) error  { return &problem{"invalid_request", text} }
func conflict(text string) error { return &problem{"conflict", text} }

var errClosed = &problem{"feature_disabled", "鲸鱼湾当前未开放。已有存档会保留。"}
var errUnavailable = &problem{"not_found", "小屋未开放或已无法访问。"}

type Service struct {
	store *db.Store
	auth  resources.FinalTxAuthorizer
	pools *activities.Service
	now   func() time.Time
}

func New(store *db.Store, auth resources.FinalTxAuthorizer, pools *activities.Service, now func() time.Time) *Service {
	return &Service{store, auth, pools, now}
}

type resident struct {
	id, revision        int64
	door                string
	open, exists, dirty bool
	state               *rules.State
	original            assets.Balance
}
type visit struct {
	guest, host, started, workLeft int64
	allowHire, working, ended      bool
	eaten, silverEaten             float64
}
type counters struct{ exchanged, social, shards int64 }
type dayKey struct {
	user int64
	day  string
}
type event struct {
	// Only earned wages and social grants post separately from the triggering action.
	movementReason string
	user, peer, at int64
	kind, text     string
	pearls, shards int64
}
type world struct {
	ctx            context.Context
	tx             *sql.Tx
	now            int64
	offset         int
	location       *time.Location
	config         config.Settings
	configRevision int64
	master         bool
	residents      map[int64]*resident
	visits         map[int64]*visit
	days           map[dayKey]counters
	wages          map[int64]float64
	events         []event
}

func openWorld(ctx context.Context, tx *sql.Tx, ms int64) (*world, error) {
	cfg, rev, err := config.Read(ctx, tx)
	if err != nil {
		return nil, err
	}
	offset, err := db.ResolveSiteTimezoneTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	w := &world{ctx: ctx, tx: tx, now: ms, offset: offset, location: time.FixedZone("site", offset*60), config: cfg, configRevision: rev,
		residents: map[int64]*resident{}, visits: map[int64]*visit{}, days: map[dayKey]counters{}, wages: map[int64]float64{}}
	err = tx.QueryRowContext(ctx, "SELECT value='1' FROM site_config WHERE key='games_enabled'").Scan(&w.master)
	return w, err
}
func (w *world) day(ms int64) string { return time.UnixMilli(ms).In(w.location).Format(time.DateOnly) }
func (w *world) midnight(ms int64) int64 {
	t := time.UnixMilli(ms).In(w.location)
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, w.location).UnixMilli()
}
func (w *world) daily(id int64, day string) (counters, error) {
	k := dayKey{id, day}
	if v, ok := w.days[k]; ok {
		return v, nil
	}
	var v counters
	err := w.tx.QueryRowContext(w.ctx, "SELECT exchanged,social,shards FROM whale_haven_daily WHERE user_id=? AND site_day=?", id, day).Scan(&v.exchanged, &v.social, &v.shards)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return v, err
}
func (w *world) setDay(r *resident, day string) error {
	v, err := w.daily(r.id, day)
	if err != nil {
		return err
	}
	r.state.ResetDay(day)
	wallet := &r.state.Wallet
	wallet.DailyPearlExchanged = v.exchanged
	wallet.DailySocialPearlsGained = v.social
	wallet.DailyConchShardsGained = v.shards
	return nil
}
func (w *world) keepDay(r *resident) {
	v := r.state.Wallet
	w.days[dayKey{r.id, v.SiteDayKey}] = counters{v.DailyPearlExchanged, v.DailySocialPearlsGained, v.DailyConchShardsGained}
}
func (w *world) load(id int64) (*resident, error) {
	if p, ok := w.residents[id]; ok {
		return p, nil
	}
	r := &resident{id: id, revision: 1, state: rules.NewState(w.now, w.day(w.now))}
	var raw string
	err := w.tx.QueryRowContext(w.ctx, "SELECT door,is_open,revision,snapshot FROM whale_haven_profiles WHERE user_id=?", id).Scan(&r.door, &r.open, &r.revision, &raw)
	if err == nil {
		r.exists = true
		if err = json.Unmarshal([]byte(raw), r.state); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	r.original, err = assets.Read(w.ctx, w.tx, id)
	if err != nil {
		return nil, err
	}
	r.state.Wallet.Pearls = r.original.Pearls
	r.state.Wallet.ConchShards = r.original.Shards
	w.residents[id] = r
	if err = w.setDay(r, w.day(w.now)); err != nil {
		return nil, err
	}
	return r, nil
}
func newDoor() (string, error) {
	var bytes [7]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(bytes[:]), nil
}
func (w *world) remember(e event) { w.events = append(w.events, e) }
func (w *world) save(reason, operation string) error {
	posted := make(map[int64]assets.Balance)
	for _, e := range w.events {
		if e.movementReason == "" || (e.pearls == 0 && e.shards == 0) {
			continue
		}
		grant := assets.Balance{Pearls: e.pearls, Shards: e.shards}
		if err := assets.MoveTx(w.ctx, w.tx, e.user, e.at, grant, e.movementReason, operation); err != nil {
			return err
		}
		cumulative := posted[e.user]
		cumulative.Pearls += grant.Pearls
		cumulative.Shards += grant.Shards
		posted[e.user] = cumulative
	}
	for _, r := range w.residents {
		if !r.dirty {
			continue
		}
		if err := w.setDay(r, w.day(w.now)); err != nil {
			return err
		}
		// Transferable currencies have one authoritative balance, even before adoption.
		wallet := r.state.Wallet
		r.state.Wallet.Pearls = 0
		r.state.Wallet.ConchShards = 0
		raw, err := json.Marshal(r.state)
		r.state.Wallet = wallet
		if err != nil {
			return err
		}
		r.revision++
		_, err = w.tx.ExecContext(w.ctx, `INSERT INTO whale_haven_profiles(user_id,door,is_open,whale_name,revision,snapshot,updated_at) VALUES(?,?,?,?,?,?,?)
   ON CONFLICT(user_id) DO UPDATE SET is_open=excluded.is_open,whale_name=excluded.whale_name,revision=excluded.revision,snapshot=excluded.snapshot,updated_at=excluded.updated_at`, r.id, r.door, r.open, r.state.Whale.Name, r.revision, string(raw), w.now)
		if err != nil {
			return err
		}
		residual := assets.Balance{
			Pearls: wallet.Pearls - r.original.Pearls - posted[r.id].Pearls,
			Shards: wallet.ConchShards - r.original.Shards - posted[r.id].Shards,
		}
		err = assets.MoveTx(w.ctx, w.tx, r.id, w.now, residual, reason, operation)
		if err != nil {
			return err
		}
		r.exists = true
	}
	for k, v := range w.days {
		_, err := w.tx.ExecContext(w.ctx, `INSERT INTO whale_haven_daily(user_id,site_day,exchanged,social,shards) VALUES(?,?,?,?,?)
  ON CONFLICT(user_id,site_day) DO UPDATE SET exchanged=excluded.exchanged,social=excluded.social,shards=excluded.shards`, k.user, k.day, v.exchanged, v.social, v.shards)
		if err != nil {
			return err
		}
	}
	for guestID, remainder := range w.wages {
		_, err := w.tx.ExecContext(w.ctx, `INSERT INTO whale_haven_wage_fractions(guest_id,microcoins) VALUES(?,?)
  ON CONFLICT(guest_id) DO UPDATE SET microcoins=excluded.microcoins`, guestID, remainder)
		if err != nil {
			return err
		}
	}
	for _, v := range w.visits {
		if v.ended {
			if _, err := w.tx.ExecContext(w.ctx, "DELETE FROM whale_haven_visits WHERE guest_id=?", v.guest); err != nil {
				return err
			}
			continue
		}
		_, err := w.tx.ExecContext(w.ctx, `INSERT INTO whale_haven_visits(guest_id,host_id,started_at,allow_hire,working,work_left_ms,eaten_grams,silver_eaten_grams) VALUES(?,?,?,?,?,?,?,?)
  ON CONFLICT(guest_id) DO UPDATE SET host_id=excluded.host_id,started_at=excluded.started_at,allow_hire=excluded.allow_hire,working=excluded.working,work_left_ms=excluded.work_left_ms,eaten_grams=excluded.eaten_grams,silver_eaten_grams=excluded.silver_eaten_grams`, v.guest, v.host, v.started, v.allowHire, v.working, v.workLeft, v.eaten, v.silverEaten)
		if err != nil {
			return err
		}
	}
	for _, e := range w.events {
		var peer any
		if e.peer > 0 {
			peer = e.peer
		}
		_, err := w.tx.ExecContext(w.ctx, `INSERT INTO whale_haven_events(user_id,peer_id,created_at,kind,text,pearls,shards,operation_id) VALUES(?,?,?,?,?,?,?,?)`, e.user, peer, e.at, e.kind, e.text, e.pearls, e.shards, operation)
		if err != nil {
			return err
		}
	}
	for _, r := range w.residents {
		if r.dirty {
			r.original = assets.Balance{Pearls: r.state.Wallet.Pearls, Shards: r.state.Wallet.ConchShards}
		}
	}
	return nil
}
func key(n int64) string                    { return strconv.FormatInt(n, 10) }
func noteCount(name string, n int64) string { return fmt.Sprintf("%s × %d", name, n) }
