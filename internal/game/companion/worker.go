package companion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/companion/historyio"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/program"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
)

const executionBudget = 20 * time.Second

// Each isolated execution reserves one slot. The process limit and WASI memory cap also apply to uploaded programs.
const workers = 2

type programHost interface {
	ResolveTx(context.Context, *sql.Tx, int64, string, string, int) (program.Binding, error)
	Decide(context.Context, program.Binding, json.RawMessage, json.RawMessage) (json.RawMessage, error)
	RegisterRoutes(string, host.Registrars) error
	ExportTx(context.Context, *sql.Tx, int64, string) (any, error)
	Retain(context.Context, string, int64, int) (int, error)
}
type Service struct {
	shared       host.Services
	programs     programHost
	wake         chan struct{}
	start, close sync.Once
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.Mutex
	running      map[string]context.CancelFunc
}

func New(shared host.Services) *Service {
	return &Service{shared: shared, programs: program.New(shared), wake: make(chan struct{}, workers), running: make(map[string]context.CancelFunc)}
}
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) startWorkers(ctx context.Context) {
	s.start.Do(func() {
		ctx, s.cancel = context.WithCancel(ctx)
		for range workers {
			s.wg.Add(1)
			go s.work(ctx)
		}
	})
}
func (s *Service) stopWorkers() {
	s.close.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.wg.Wait()
	})
}
func (s *Service) cancelGame(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fn := s.running[id]; fn != nil {
		fn()
	}
}
func (s *Service) work(ctx context.Context) {
	defer s.wg.Done()
	for ctx.Err() == nil {
		// Check the durable queue without occupying the single writer when idle.
		var pending bool
		err := s.shared.ReadDatabase.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM companion_matches WHERE status='active' AND ai_state='queued')`).Scan(&pending)
		if err == nil && pending {
			err = s.runOne(ctx)
			if err == nil {
				continue
			}
		}
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		}
	}
}
func (s *Service) claim(ctx context.Context) (m match, err error) {
	tx, err := s.shared.Database.BeginTx(ctx, nil)
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	var id, game string
	var user int64
	err = tx.QueryRowContext(ctx, `UPDATE companion_matches SET ai_state='thinking',queue_ms=?-queued_at WHERE id=(SELECT id FROM companion_matches WHERE status='active' AND ai_state='queued' ORDER BY queued_at,id LIMIT 1) RETURNING id,game,user_id`, s.shared.Now().UnixMilli()).Scan(&id, &game, &user)
	if err != nil {
		return m, err
	}
	m, err = readMatch(ctx, tx, id, game, user)
	if err != nil {
		return m, err
	}
	return m, tx.Commit()
}

type reply struct {
	Protocol string  `json:"protocol"`
	ID       string  `json:"decision_id"`
	Action   Raw     `json:"action"`
	Memory   *string `json:"memory,omitempty"`
}

func (s *Service) decide(ctx context.Context, m *match) (reply, string) {
	var out reply
	r, err := rulesFor(m.Game, m.Mode)
	if err != nil {
		return out, "engine_error"
	}
	seat := 1 - m.Seat
	t, err := r.observe(m.Frame, &m.Record, seat, false)
	if err != nil || !t.Need[seat] {
		return out, "engine_error"
	}
	id := m.decision(seat)
	recent := m.Frame.Recent[seat]
	if recent == nil {
		recent = []Raw{}
	}
	q := Obj{"protocol": protocol, "game": m.Game, "mode": m.Mode, "rules_hash": r.hash, "seat": seat, "round": t.Round, "phase": t.Phase, "decision_id": id, "view": t.View, "space": t.Space, "recent": recent, "memory": m.Frame.Memory, "budget_ms": executionBudget.Milliseconds()}
	if t.Phase == "setup" {
		q["constraints"] = m.Record.Initial.Constraints
	}
	sources, err := s.historySources(ctx, m)
	if err != nil {
		return out, "engine_error"
	}
	if len(sources) > 0 {
		q["history"] = sources
	}
	ctx = historyio.WithReader(ctx, s.historyReader(m, sources))
	raw, err := s.programs.Decide(ctx, m.binding(), encode(q), r.catalog)
	if ctx.Err() != nil {
		return out, "timeout"
	}
	if err != nil {
		return out, "program_error"
	}
	if len(raw) > 64<<10 || strict(raw, &out) != nil || out.Protocol != protocol || out.ID != id || len(out.Action) == 0 || out.Memory != nil && len(*out.Memory) > 8192 {
		return out, "invalid_output"
	}
	if r.validate(m.Frame, &m.Record, seat, t, out.Action) != nil {
		return out, "invalid_action"
	}
	return out, ""
}
func (s *Service) runOne(ctx context.Context) error {
	m, err := s.claim(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	start := time.Now()
	work, cancel := context.WithTimeout(ctx, executionBudget)
	s.mu.Lock()
	s.running[m.ID] = cancel
	s.mu.Unlock()
	out, reason := s.decide(work, &m)
	if work.Err() == context.DeadlineExceeded || time.Since(start) >= executionBudget {
		reason = "timeout"
	}
	elapsed := time.Since(start)
	cancel()
	s.mu.Lock()
	delete(s.running, m.ID)
	s.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// The player may have locked or abandoned while this program ran. Re-read the exact window before applying its answer.
	for ctx.Err() == nil {
		err = s.finish(ctx, m, out, reason, elapsed)
		if err == nil || errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return ctx.Err()
}
func (s *Service) finish(ctx context.Context, claimed match, out reply, reason string, elapsed time.Duration) error {
	tx, err := s.shared.Database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	m, err := readMatch(ctx, tx, claimed.ID, claimed.Game, claimed.User)
	if err != nil {
		return err
	}
	seat := 1 - m.Seat
	if m.Status != "active" || m.AIState != "thinking" || m.decision(seat) != claimed.decision(seat) {
		return nil
	}
	if reason == "" {
		r, err := rulesFor(m.Game, m.Mode)
		if err != nil {
			return err
		}
		if err = m.apply(r, seat, out.Action); err != nil {
			reason = "engine_error"
		} else if out.Memory != nil {
			m.Frame.Memory = *out.Memory
		}
	}
	if reason != "" {
		m.result = result{Status: "technical", Reason: reason}
	}
	m.AIState = "idle"
	if err = m.update(ctx, tx, s.shared.Now()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE companion_matches SET execution_ms=? WHERE id=?", elapsed.Milliseconds(), m.ID); err != nil {
		return err
	}
	return tx.Commit()
}
