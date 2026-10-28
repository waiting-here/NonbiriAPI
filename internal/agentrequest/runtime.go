package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/chatgptauth"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type Runtime struct {
	chatgpt         *chatgptauth.Manager
	connectionMu    sync.Mutex
	activeProviders map[int64]string
	adminAuthorize  func(context.Context, *sql.Tx) error
	db, readDB      *sql.DB
	stack           *egress.Stack
	flow            *flowcontrol.Controller
	secrets         secret.AgentCodec
	now             func() time.Time
	authorize       func(context.Context, Attribution) error
	onEvent         func(Event)
	mu              sync.Mutex
	started, closed bool
	context         context.Context
	cancel          context.CancelFunc
	changed         chan struct{}
	failures        map[int64]error
	active          map[int64]context.CancelFunc
	waitingID       int64
	waitingCancel   context.CancelFunc
	workers         sync.WaitGroup
}

func New(c Config) (*Runtime, error) {
	if c.DB == nil || c.ReadDB == nil || c.Stack == nil || c.Flow == nil || c.Secrets == nil || c.Authorize == nil || c.AdminAuthorize == nil {
		return nil, ErrInvalid
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{chatgpt: c.ChatGPT, activeProviders: map[int64]string{}, adminAuthorize: c.AdminAuthorize, db: c.DB, readDB: c.ReadDB, stack: c.Stack, flow: c.Flow, secrets: c.Secrets, now: c.Now, authorize: c.Authorize, onEvent: c.OnEvent, context: ctx, cancel: cancel, changed: make(chan struct{}), active: map[int64]context.CancelFunc{}, failures: map[int64]error{}}, nil
}
func (r *Runtime) signal() {
	r.mu.Lock()
	close(r.changed)
	r.changed = make(chan struct{})
	r.mu.Unlock()
}
func (r *Runtime) Start() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	if r.started {
		r.mu.Unlock()
		return nil
	}
	r.started = true
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.context, 10*time.Second)
	defer cancel()
	err := r.recoverInterrupted(ctx)
	if err != nil {
		r.mu.Lock()
		r.started = false
		r.mu.Unlock()
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	r.workers.Add(1)
	r.mu.Unlock()
	go r.schedule()
	return nil
}
func (r *Runtime) Close() error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.cancel()
	}
	r.mu.Unlock()
	r.signal()
	r.workers.Wait()
	return nil
}
func (r *Runtime) emit(id int64, client, state string) {
	r.signal()
	if r.onEvent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		queue, _ := r.Queue(ctx, id)
		cancel()
		r.onEvent(Event{RequestID: id, ClientRequestID: client, State: state, Queue: queue})
	}
}
func (r *Runtime) schedule() {
	defer r.workers.Done()
	for r.context.Err() == nil {
		r.mu.Lock()
		active := len(r.active)
		changed := r.changed
		r.mu.Unlock()
		settings, err := r.Settings(r.context)
		var id int64
		var encoded string
		if err == nil && active < settings.Concurrency {
			err = r.readDB.QueryRowContext(r.context, "SELECT id,input_json FROM agent_requests WHERE state='pending' ORDER BY id LIMIT 1").Scan(&id, &encoded)
			if err == nil {
				r.dispatchNext(id, encoded)
				r.mu.Lock()
				failed := r.failures[id] != nil
				r.mu.Unlock()
				if !failed {
					continue
				}
			}
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-r.context.Done():
			timer.Stop()
			return
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}
func (r *Runtime) dispatchNext(id int64, encoded string) {
	ctx, cancel := context.WithCancel(r.context)
	r.mu.Lock()
	r.waitingID = id
	r.waitingCancel = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.waitingID == id {
			r.waitingID = 0
			r.waitingCancel = nil
		}
		r.mu.Unlock()
	}()
	var in Input
	if json.Unmarshal([]byte(encoded), &in) != nil {
		cancel()
		r.failPending(id, "invalid_request")
		return
	}
	if err := r.authorize(ctx, in.Attribution); err != nil {
		if ctx.Err() != nil {
			cancel()
			return
		}
		cancel()
		r.failPending(id, "authorization")
		return
	}
	admission, err := r.flow.WaitGlobal(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.failPending(id, "admission_unavailable")
		}
		cancel()
		return
	}
	if err = r.authorize(ctx, in.Attribution); err != nil {
		if ctx.Err() != nil {
			admission.Release()
			cancel()
			return
		}
		admission.Release()
		cancel()
		r.failPending(id, "authorization")
		return
	}
	settings, key, err := r.resolveSettingsKey(ctx, in.Attribution)
	if err != nil || !settings.HasKey || settings.BaseURL == "" || settings.Model == "" {
		admission.Release()
		cancel()
		r.failPending(id, "unconfigured")
		return
	}
	if settings.Provider == ProviderChatGPT {
		// Disconnect and dispatch share this boundary so a token obtained just
		// before logout cannot start an untracked request afterwards.
		r.connectionMu.Lock()
		defer r.connectionMu.Unlock()
	}
	key, err = r.modelKey(ctx, &settings, key)
	if err != nil {
		admission.Release()
		cancel()
		r.failPending(id, "credential_unavailable")
		return
	}
	body, err := requestBody(in, settings)
	if err != nil {
		admission.Release()
		cancel()
		r.failPending(id, "invalid_request")
		return
	}
	r.mu.Lock()
	active := len(r.active)
	r.mu.Unlock()
	if active >= settings.Concurrency {
		admission.Release()
		cancel()
		return
	}
	plaintext, err := r.secrets.OpenAgentKey(key, "model")
	if err != nil {
		admission.Release()
		cancel()
		r.failPending(id, "credential_unavailable")
		return
	}
	leaked := containsSecret(body, plaintext)
	clear(plaintext)
	if leaked {
		admission.Release()
		cancel()
		r.failPending(id, "invalid_request")
		return
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		r.recordDispatchFailure(ctx, id, err)
		admission.Release()
		cancel()
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE agent_requests SET state='running',base_url=?,model=?,request_body=?,started_at=MAX(created_at,?) WHERE id=? AND state='pending'", settings.BaseURL, settings.Model, body, r.now().UnixMilli(), id)
	if err != nil {
		if ctx.Err() == nil {
			r.mu.Lock()
			r.failures[id] = err
			r.mu.Unlock()
			r.signal()
		}
		admission.Release()
		cancel()
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		admission.Release()
		cancel()
		return
	}
	snapshot, _ := json.Marshal(settings)
	if _, err = tx.ExecContext(ctx, "INSERT INTO agent_request_drivers(request_id,driver_id,provider,settings_json) VALUES(?,?,?,?)", id, settings.DriverID, settings.Provider, string(snapshot)); err != nil {
		r.recordDispatchFailure(ctx, id, err)
		admission.Release()
		cancel()
		return
	}
	if err = tx.Commit(); err != nil {
		r.recordDispatchFailure(ctx, id, err)
		admission.Release()
		cancel()
		return
	}
	r.mu.Lock()
	delete(r.failures, id)
	r.active[id] = cancel
	r.activeProviders[id] = settings.Provider
	r.workers.Add(1)
	r.mu.Unlock()
	r.emit(id, in.ClientRequestID, "running")
	go func() {
		defer r.workers.Done()
		defer cancel()
		r.execute(ctx, id, in, settings, key, body, admission)
		r.mu.Lock()
		delete(r.active, id)
		delete(r.activeProviders, id)
		r.mu.Unlock()
		r.signal()
	}()
}
func (r *Runtime) recordDispatchFailure(ctx context.Context, id int64, err error) {
	if ctx.Err() != nil {
		return
	}
	r.mu.Lock()
	r.failures[id] = err
	r.mu.Unlock()
	r.signal()
}
func (r *Runtime) failPending(id int64, code string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := r.db.ExecContext(ctx, "UPDATE agent_requests SET state='failed',error_code=?,completed_at=MAX(created_at,?) WHERE id=? AND state='pending'", code, r.now().UnixMilli(), id)
	r.mu.Lock()
	if err != nil {
		r.failures[id] = err
	} else {
		delete(r.failures, id)
	}
	r.mu.Unlock()
	r.emit(id, "", "failed")
}
func terminal(state string) bool { return state != "pending" && state != "running" }
func (r *Runtime) Cancel(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalid
	}
	r.mu.Lock()
	if cancel := r.active[id]; cancel != nil {
		cancel()
	}
	if r.waitingID == id && r.waitingCancel != nil {
		r.waitingCancel()
	}
	r.mu.Unlock()
	result, err := r.db.ExecContext(ctx, "UPDATE agent_requests SET state='cancelled',error_code='cancelled',completed_at=MAX(created_at,?) WHERE id=? AND state='pending'", r.now().UnixMilli(), id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		r.emit(id, "", "cancelled")
	}
	return nil
}
func (r *Runtime) Run(ctx context.Context, in Input) (Result, error) {
	request, err := r.Submit(ctx, in)
	if err != nil {
		return Result{}, err
	}
	result, err := r.Wait(ctx, request.ID)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = r.Cancel(cleanup, request.ID)
		cancel()
	}
	return result, err
}

func (r *Runtime) releaseSlot(id int64) {
	r.mu.Lock()
	delete(r.active, id)
	delete(r.activeProviders, id)
	r.mu.Unlock()
	r.signal()
}
