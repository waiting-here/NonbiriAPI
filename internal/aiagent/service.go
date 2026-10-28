package aiagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
)

type runningJob struct {
	cancel  context.CancelFunc
	kind    string
	actorID int64
}
type Service struct {
	config   Config
	db, read *sql.DB
	now      func() time.Time
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	running  map[int64]runningJob
	wakeup   chan struct{}
	wg       sync.WaitGroup
	start    sync.Once
}

func New(config Config) (*Service, error) {
	if config.DB == nil || config.Driver == nil || config.Operations == nil || config.Authorize == nil {
		return nil, ErrInvalid
	}
	if config.ReadDB == nil {
		config.ReadDB = config.DB
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{config: config, db: config.DB, read: config.ReadDB, now: config.Now, ctx: ctx, cancel: cancel, running: make(map[int64]runningJob), wakeup: make(chan struct{}, 1)}, nil
}

func (s *Service) authorize(ctx context.Context, tx *sql.Tx, actor Actor, permission string) error {
	if actor.ID <= 0 {
		return ErrForbidden
	}
	return s.config.Authorize(ctx, tx, actor, permission)
}

func (s *Service) chatEnabled(ctx context.Context, tx *sql.Tx, actor Actor) (bool, error) {
	settings, _, _, err := readSettings(ctx, tx)
	if err != nil || !settings.ChatEnabled {
		return false, err
	}
	return s.config.Driver.ReadyFor(ctx, tx, jobAttribution(Job{ActorID: actor.ID, ActorRole: actor.Role, ActorLabel: actor.Label}, "model"))
}

func (s *Service) requireChat(ctx context.Context, tx *sql.Tx, actor Actor) error {
	if err := s.authorize(ctx, tx, actor, "ai.chat"); err != nil {
		return err
	}
	enabled, err := s.chatEnabled(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrChatDisabled
	}
	return nil
}

func (s *Service) Start() { s.start.Do(func() { s.wg.Add(1); go s.loop() }) }

// Recover is called before the model dispatcher starts. Interrupted network
// turns are never reissued automatically after process recovery.
func (s *Service) Recover(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='interrupted',error='服务重启，请继续对话或重新提交待审内容',updated_at=? WHERE state='running'`, s.now().UnixMilli())
	return err
}
func (s *Service) Close() error {
	s.cancel()
	s.mu.Lock()
	for _, job := range s.running {
		job.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}
func (s *Service) wake() {
	select {
	case s.wakeup <- struct{}{}:
	default:
	}
}
func (s *Service) cancelAutomatic() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.running {
		if job.kind == "auto_review" {
			job.cancel()
		}
	}
}
func (s *Service) cancelChat() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.running {
		if job.kind != "auto_review" {
			job.cancel()
		}
	}
}
func (s *Service) cancelJob(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, ok := s.running[id]; ok {
		job.cancel()
	}
}

// InvalidateActor is a post-commit account-authority notification. Live checks
// remain authoritative even if persistence is temporarily unavailable.
func (s *Service) InvalidateActor(actorID int64) {
	if actorID <= 0 {
		return
	}
	s.mu.Lock()
	for _, job := range s.running {
		if job.actorID == actorID {
			job.cancel()
		}
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_plans SET state='superseded',decided_at=? WHERE state='pending' AND job_id IN (SELECT id FROM ai_agent_jobs WHERE actor_id=?)`, s.now().UnixMilli(), actorID); err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='cancelled',error='账号权限已变化，请重新发起对话',updated_at=? WHERE actor_id=? AND state IN ('pending','running','waiting_confirmation')`, s.now().UnixMilli(), actorID); err != nil {
		return
	}
	_ = tx.Commit()
	s.wake()
}

func (s *Service) loop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		s.recoverResults(s.ctx)
		_ = s.collectReviewEvents(s.ctx)
		s.dispatch()
		select {
		case <-s.ctx.Done():
			return
		case <-s.wakeup:
		case <-ticker.C:
		}
	}
}

func (s *Service) dispatch() {
	s.mu.Lock()
	capacity := 32 - len(s.running)
	s.mu.Unlock()
	if capacity <= 0 {
		return
	}
	rows, err := s.read.QueryContext(s.ctx, `SELECT id,kind,actor_id FROM ai_agent_jobs WHERE state='pending' ORDER BY id LIMIT ?`, capacity)
	if err != nil {
		return
	}
	type candidate struct {
		id      int64
		kind    string
		actorID int64
	}
	items := []candidate{}
	for rows.Next() {
		var v candidate
		if err = rows.Scan(&v.id, &v.kind, &v.actorID); err != nil {
			break
		}
		items = append(items, v)
	}
	rows.Close()
	if err != nil {
		return
	}
	for _, item := range items {
		s.mu.Lock()
		_, exists := s.running[item.id]
		if exists {
			s.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(s.ctx)
		s.running[item.id] = runningJob{cancel: cancel, kind: item.kind, actorID: item.actorID}
		s.wg.Add(1)
		s.mu.Unlock()
		go func(id int64) {
			defer s.wg.Done()
			defer func() { s.mu.Lock(); delete(s.running, id); s.mu.Unlock(); s.wake() }()
			defer cancel()
			if err := s.run(ctx, id); err != nil {
				s.fail(id, err)
			}
		}(item.id)
	}
}

const jobColumns = `id,kind,actor_id,actor_role,actor_label,COALESCE(conversation_id,0),generation,COALESCE(donation_id,0),state,request_id,error,created_at,updated_at`

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Kind, &j.ActorID, &j.ActorRole, &j.ActorLabel, &j.ConversationID, &j.Generation, &j.DonationID, &j.State, &j.RequestID, &j.Error, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return j, err
}
func (s *Service) loadJob(ctx context.Context, id int64) (Job, Actor, error) {
	j, err := scanJob(s.read.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ai_agent_jobs WHERE id=?`, id))
	if err != nil {
		return Job{}, Actor{}, err
	}
	a := Actor{ID: j.ActorID, Role: j.ActorRole, Label: j.ActorLabel, Automatic: j.Kind == "auto_review"}
	err = s.read.QueryRowContext(ctx, `SELECT session_hash,session_generation FROM ai_agent_jobs WHERE id=?`, id).Scan(&a.SessionHash, &a.SessionGeneration)
	return j, a, err
}
func (s *Service) fail(id int64, err error) {
	state := "failed"
	reason := err.Error()
	if errors.Is(err, context.Canceled) || errors.Is(err, agentrequest.ErrInterrupted) {
		state = "interrupted"
		reason = "请求已中断，可继续对话。"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.db.ExecContext(ctx, `UPDATE ai_agent_jobs SET state=?,error=?,updated_at=? WHERE id=? AND state IN ('pending','running')`, state, reason, s.now().UnixMilli(), id)
}

// AuthorizeRequest is wired to the driver's final dispatch check. A queued
// request cannot outlive its conversation generation, role, or review mode.
func (s *Service) AuthorizeRequest(ctx context.Context, attr agentrequest.Attribution) error {
	parts := strings.Split(attr.ClientRequestID, ":")
	if len(parts) != 3 || parts[0] != "job" {
		return ErrForbidden
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		return ErrForbidden
	}
	sequence, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || sequence <= 0 {
		return ErrForbidden
	}
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ai_agent_jobs WHERE id=?`, id))
	if err != nil {
		return err
	}
	if j.State != "running" || j.ActorID != attr.ActorID {
		return ErrConflict
	}
	var currentSequence int64
	if err = tx.QueryRowContext(ctx, `SELECT request_sequence FROM ai_agent_jobs WHERE id=?`, id).Scan(&currentSequence); err != nil {
		return err
	}
	if sequence != currentSequence {
		return ErrConflict
	}
	actor := Actor{ID: j.ActorID, Role: j.ActorRole, Label: j.ActorLabel, Automatic: j.Kind == "auto_review"}
	if err = tx.QueryRowContext(ctx, `SELECT session_hash,session_generation FROM ai_agent_jobs WHERE id=?`, id).Scan(&actor.SessionHash, &actor.SessionGeneration); err != nil {
		return err
	}
	if j.Kind == "auto_review" {
		settings, _, _, err := readSettings(ctx, tx)
		if err != nil {
			return err
		}
		if settings.Mode == "off" {
			return ErrConflict
		}
		return s.authorize(ctx, tx, actor, "ai.auto")
	}
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT generation FROM ai_agent_conversations WHERE id=? AND actor_id=?`, j.ConversationID, actor.ID).Scan(&generation); err != nil {
		return err
	}
	if generation != j.Generation {
		return ErrConflict
	}
	return s.requireChat(ctx, tx, actor)
}

func jobAttribution(job Job, kind string) agentrequest.Attribution {
	source := "steward_chat"
	if job.Kind == "auto_review" {
		source = "auto_review"
	} else if job.ActorRole == "admin" {
		source = "admin_chat"
	}
	return agentrequest.Attribution{Source: source, Kind: kind, ActorID: job.ActorID, ActorRole: job.ActorRole, ActorLabel: job.ActorLabel}
}

func (s *Service) call(ctx context.Context, job Job, kind string, messages, tools, responseFormat []byte) (agentrequest.Result, error) {
	var sequence int64
	err := s.db.QueryRowContext(ctx, `UPDATE ai_agent_jobs SET request_sequence=request_sequence+1,updated_at=? WHERE id=? AND state='running' RETURNING request_sequence`, s.now().UnixMilli(), job.ID).Scan(&sequence)
	if err != nil {
		return agentrequest.Result{}, ErrConflict
	}
	attr := jobAttribution(job, kind)
	attr.ClientRequestID = fmt.Sprintf("job:%d:%d", job.ID, sequence)
	request, err := s.config.Driver.Submit(ctx, agentrequest.Input{Attribution: attr, Messages: messages, Tools: tools, ResponseFormat: responseFormat})
	if err != nil {
		return agentrequest.Result{}, err
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE ai_agent_jobs SET request_id=?,updated_at=? WHERE id=? AND state='running'`, request.ID, s.now().UnixMilli(), job.ID); err != nil {
		_ = s.config.Driver.Cancel(context.Background(), request.ID)
		return agentrequest.Result{}, err
	}
	result, err := s.config.Driver.Wait(ctx, request.ID)
	if ctx.Err() != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.config.Driver.Cancel(cleanup, request.ID)
		if terminal, waitErr := s.config.Driver.Wait(cleanup, request.ID); !errors.Is(waitErr, context.DeadlineExceeded) {
			result = terminal
		}
	}
	result.RequestID = request.ID
	// A durable workflow copy is acknowledged in the same transaction. Manual
	// request-log cleanup can then neither race this handoff nor erase a review.
	if saveErr := s.storeModelResult(job, kind, result, err); saveErr != nil {
		return result, saveErr
	}
	return result, err
}

func (s *Service) recoverResults(ctx context.Context) {
	requests, err := s.config.Driver.Unconsumed(ctx, 16)
	if err != nil {
		return
	}
	for _, request := range requests {
		parts := strings.Split(request.ClientRequestID, ":")
		if len(parts) != 3 || parts[0] != "job" {
			continue
		}
		id, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil || id <= 0 {
			continue
		}
		bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		result, e := s.config.Driver.Wait(bounded, request.ID)
		expired := bounded.Err() != nil
		cancel()
		if expired {
			continue
		}
		job, _, err := s.loadJob(ctx, id)
		if errors.Is(err, ErrNotFound) {
			// The corresponding approval aggregate may have completed its normal
			// retention lifecycle. Its independent request log remains available.
			tx, e := s.db.BeginTx(ctx, nil)
			if e != nil {
				continue
			}
			var exists bool
			e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_agent_jobs WHERE id=?)`, id).Scan(&exists)
			if e == nil && !exists {
				e = s.config.Driver.AckInTransaction(ctx, tx, request.ID)
			}
			if e == nil && !exists {
				_ = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			continue
		}
		if err != nil {
			continue
		}
		s.mu.Lock()
		_, active := s.running[id]
		s.mu.Unlock()
		if active || job.State == "running" || job.State == "pending" || job.State == "waiting_confirmation" {
			continue
		}
		if job.ActorID != request.ActorID {
			continue
		}
		result.RequestID = request.ID
		_ = s.storeModelResult(job, request.Kind, result, e)
	}
}

func (s *Service) storeModelResult(job Job, kind string, result agentrequest.Result, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	message := result.Message
	if !json.Valid(message) {
		message = json.RawMessage(`{}`)
	}
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_agent_model_results(request_id,job_id,kind,message,error,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(request_id) DO NOTHING`, result.RequestID, job.ID, kind, string(message), reason, s.now().UnixMilli())
	if err != nil {
		return err
	}
	if err = s.config.Driver.AckInTransaction(ctx, tx, result.RequestID); err != nil {
		return err
	}
	return tx.Commit()
}

func modelResults(ctx context.Context, tx *sql.Tx, jobID int64) ([]ModelResult, error) {
	rows, err := tx.QueryContext(ctx, `SELECT request_id,kind,message,error,created_at FROM ai_agent_model_results WHERE job_id=? ORDER BY request_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ModelResult{}
	for rows.Next() {
		var item ModelResult
		var message string
		if err = rows.Scan(&item.RequestID, &item.Kind, &message, &item.Error, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Message = json.RawMessage(message)
		items = append(items, item)
	}
	return items, rows.Err()
}
