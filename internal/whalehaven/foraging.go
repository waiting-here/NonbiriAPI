package whalehaven

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"log/slog"
	randv2 "math/rand/v2"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const forageStep = 30 * time.Second
const forageBatchSize = 16

type foodVisit struct {
	guest, host string
}

// RunForaging advances open homes even when neither keeper has a browser open.
// Larger populations are visited in door order, one bounded page at a time.
func (s *Service) RunForaging(ctx context.Context) {
	var seed [32]byte
	rand.Read(seed[:])
	random := randv2.New(randv2.NewChaCha8(seed))
	timer := time.NewTimer(forageStep)
	defer timer.Stop()
	var cursor string
	var started time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if cursor == "" {
			started = time.Now()
		}
		batchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		next, err := s.forageBatch(batchCtx, cursor, random.IntN)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Error("whale foraging batch failed", "err", err)
		}
		cursor = next
		delay := time.Second
		if cursor == "" {
			delay = max(delay, time.Until(started.Add(forageStep)))
		}
		timer.Reset(delay)
	}
}

func (s *Service) forageBatch(ctx context.Context, cursor string, draw func(int) int) (string, error) {
	next, pairs, err := s.findFoodVisits(ctx, cursor, draw)
	if err != nil {
		return next, err
	}
	for _, pair := range pairs {
		if err := s.commitFoodVisit(ctx, pair); err != nil {
			return next, err
		}
	}
	return next, nil
}

// Use the open-door index for both the guest sweep and a random neighborhood
// of potential hosts. Wrap the host sample to include doors before the pivot.
func forageDoors(ctx context.Context, tx *sql.Tx, after string, wrap bool) ([]string, error) {
	query := "SELECT door FROM whale_haven_profiles WHERE is_open=1 AND door>? ORDER BY door LIMIT ?"
	doors := make([]string, 0, forageBatchSize)
	read := func(query string) error {
		rows, err := tx.QueryContext(ctx, query, after, forageBatchSize-len(doors))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var door string
			if err := rows.Scan(&door); err != nil {
				return err
			}
			doors = append(doors, door)
		}
		return rows.Err()
	}
	if err := read(query); err != nil {
		return nil, err
	}
	if wrap && len(doors) < forageBatchSize {
		if err := read("SELECT door FROM whale_haven_profiles WHERE is_open=1 AND door<=? ORDER BY door LIMIT ?"); err != nil {
			return nil, err
		}
	}
	return doors, nil
}

func forageEnabled(ctx context.Context, tx *sql.Tx) (bool, error) {
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT (SELECT enabled=0 FROM maintenance_state WHERE id=1)
		AND (SELECT value='1' FROM site_config WHERE key='games_enabled')
		AND COALESCE((SELECT json_extract(document,'$.enabled') FROM whale_haven_settings ORDER BY revision DESC LIMIT 1),0)`).Scan(&enabled)
	return enabled, err
}

func (w *world) openForager(door string) (*resident, error) {
	r, err := w.accessible(door)
	if errors.Is(err, errUnavailable) {
		return nil, nil
	}
	return r, err
}

func (w *world) wantsFood(r *resident) bool {
	return r != nil && r.state.Whale.HasCompletedNaming && r.state.Whale.Location.Kind == "home" &&
		!r.state.Whale.IsSleeping && r.state.Whale.Satiety <= 24 && r.state.Home.TroughGrams == 0 && !r.state.CanRefill(w.config)
}

func (w *world) canShareFood(r *resident) bool {
	return r != nil && r.state.Whale.HasCompletedNaming && r.state.Home.TroughGrams >= 50 &&
		r.state.Home.BankRoom(r.state.BankCapacity(w.config)) > 0 && w.guestCount(r.id) < 2
}

func (s *Service) findFoodVisits(ctx context.Context, cursor string, draw func(int) int) (string, []foodVisit, error) {
	tx, err := s.store.ReadDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	if on, err := forageEnabled(ctx, tx); err != nil || !on {
		return "", nil, err
	}
	doors, err := forageDoors(ctx, tx, cursor, false)
	if err != nil || len(doors) == 0 {
		return "", nil, err
	}
	var next string
	if len(doors) == forageBatchSize {
		next = doors[len(doors)-1]
	}
	w, err := openWorld(ctx, tx, s.now().UnixMilli())
	if err != nil {
		return next, nil, err
	}
	var guests []*resident
	for _, door := range doors {
		roll := draw(100)
		if roll >= 38 {
			continue
		}
		r, err := w.openForager(door)
		if err != nil {
			return next, nil, err
		}
		if r == nil {
			continue
		}
		from := r.savedTick
		if from >= w.now {
			continue
		}
		if err := w.settle(r); err != nil {
			return next, nil, err
		}
		elapsed := w.now - max(from, w.lastPauseEnd)
		if w.wantsFood(r) && elapsed > 0 && (elapsed >= 20_000 || roll < 3) {
			guests = append(guests, r)
		}
	}
	if len(guests) == 0 {
		return next, nil, nil
	}
	pivot, err := newDoor()
	if err != nil {
		return next, nil, err
	}
	doors, err = forageDoors(ctx, tx, pivot, true)
	if err != nil {
		return next, nil, err
	}
	var hosts []*resident
	for _, door := range doors {
		h, err := w.openForager(door)
		if err != nil {
			return next, nil, err
		}
		// An enabled refill could have filled the bowl under an earlier serving size.
		if h == nil || (h.state.Home.TroughGrams < 50 && !h.state.Home.Buffs.AutoRefillEnabled) {
			continue
		}
		if err := w.settle(h); err != nil {
			return next, nil, err
		}
		if w.canShareFood(h) {
			hosts = append(hosts, h)
		}
	}
	var pairs []foodVisit
	for _, g := range guests {
		if len(hosts) == 0 {
			break
		}
		i := draw(len(hosts))
		h := hosts[i]
		pairs = append(pairs, foodVisit{g.door, h.door})
		// Reserve this slot in the projection for the rest of this batch.
		w.visits[g.id] = &visit{guest: g.id, host: h.id}
		if w.guestCount(h.id) >= 2 {
			hosts[i] = hosts[len(hosts)-1]
			hosts = hosts[:len(hosts)-1]
		}
	}
	return next, pairs, nil
}

func (s *Service) commitFoodVisit(ctx context.Context, pair foodVisit) error {
	tx, err := s.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if on, err := forageEnabled(ctx, tx); err != nil || !on {
		return err
	}
	w, err := openWorld(ctx, tx, s.now().UnixMilli())
	if err != nil {
		return err
	}
	g, err := w.openForager(pair.guest)
	if err != nil || g == nil {
		return err
	}
	if err := w.settle(g); err != nil || !w.wantsFood(g) {
		return err
	}
	h, err := w.openForager(pair.host)
	if err != nil || h == nil {
		return err
	}
	if err := w.settle(h); err != nil || !w.canShareFood(h) {
		return err
	}
	w.startVisit(g, h, false, "家里断粮，小鲸自己游去邻居家蹭饭了", "一只饿着肚子的小鲸闻着饭香游来了")
	op, err := db.GenerateOpaqueID("op_")
	if err != nil {
		return err
	}
	if err := w.save("auto_visit", op); err != nil {
		return err
	}
	return tx.Commit()
}
