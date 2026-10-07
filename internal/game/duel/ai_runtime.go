package duel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
)

func (s *Service) wakeAI() {
	if s.aiWake != nil {
		select {
		case s.aiWake <- struct{}{}:
		default:
		}
	}
}

func (s *Service) compiledAI(snapshot AISnapshot) (any, error) {
	terms := snapshot.Terms.AI
	if terms == nil {
		return nil, ErrInvariant
	}
	key := terms.PolicyID + "/" + strconv.Itoa(terms.PolicyVersion) + "/" + digest(snapshot.Policy)
	if compiled, ok := s.aiCompiled.Load(key); ok {
		return compiled, nil
	}
	compiled, err := s.aiAdapter.Compile(snapshot.Policy)
	if err == nil {
		compiled, _ = s.aiCompiled.LoadOrStore(key, compiled)
	}
	return compiled, err
}

func (s *Service) driveAI(ctx context.Context) error {
	if s.aiAdapter == nil || !s.recovered.Load() || s.closed.Load() {
		return nil
	}
	tx, now, err := s.beginRead(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM game_duel_sessions WHERE game_key=? AND state='active' AND economy='ai_challenge' ORDER BY phase_deadline,id LIMIT ?`, s.rules.ID(), AIActiveCapacity)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	type work struct {
		request ai.Request
		seat    int
		phase   db.U128
		failure string
	}
	jobs := []work{}
	for _, id := range ids {
		v, err := s.session(ctx, tx, id)
		if err != nil {
			return err
		}
		if v.AI == nil {
			return ErrInvariant
		}
		seat := v.AI.BotSeat
		if v.Seats[seat].Locked || v.Deadline == nil || *v.Deadline <= now {
			continue
		}
		request, err := s.aiAdapter.Request(v.Payload.Rules, seat)
		if err != nil {
			return err
		}
		token := v.PhaseSeq.Decimal()
		if rules, ok := v.rules.(SequentialRules); ok {
			ids, err := rules.Decisions(v.Mode, v.Payload.Rules)
			if err != nil {
				return err
			}
			token = ids[seat]
			if token == "" {
				continue
			}
		}
		request.Window = ai.Window{Match: v.ID, Actor: strconv.Itoa(seat), Token: token, Phase: v.Phase}
		request.DecisionID = v.ID + "/" + request.Window.Actor + "/" + request.Window.Token
		request.RulesVersion = v.Terms.ContentHash
		// Wall-clock game deadlines become a monotonic local budget. The final
		// transaction still checks the authoritative phase clock before accepting.
		request.Deadline = time.Now().Add(time.Duration(*v.Deadline-now)*time.Second - 500*time.Millisecond)
		item := work{request: request, seat: seat, phase: v.PhaseSeq}
		compiled, err := s.compiledAI(v.AI.Snapshot)
		if err != nil {
			item.failure = "configuration"
		} else {
			item.request.Policy = compiled
		}
		if v.AI.Snapshot.MemoryEnabled {
			item.request.Personalization, err = s.aiAdapter.Memory(v.AI.Snapshot.Memory)
			if err != nil {
				item.failure = "memory"
			}
		}
		seed, err := hex.DecodeString(v.AI.Snapshot.RandomSeed)
		if err != nil || len(seed) != 32 {
			return ErrInvariant
		}
		mac := hmac.New(sha256.New, seed)
		_, _ = mac.Write([]byte(item.request.DecisionID))
		var decisionSeed [32]byte
		copy(decisionSeed[:], mac.Sum(nil))
		item.request.Random = rand.New(rand.NewChaCha8(decisionSeed))
		jobs = append(jobs, item)
	}
	// No source computation or callback runs inside the observation transaction.
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, item := range jobs {
		if item.failure != "" {
			if err := s.submitAI(ctx, item.request, item.seat, item.phase, ai.Result{}, item.failure); err != nil && !errors.Is(err, ErrConflict) {
				return err
			}
			continue
		}
		_, err := s.aiPool.Submit(ctx, ai.Job{Request: item.request, Source: s.aiAdapter.Source(), Budget: 250 * time.Millisecond, Complete: func(result ai.Result, cause error) {
			failure := ""
			switch {
			case errors.Is(cause, ai.ErrQueueTimeout):
				failure = "queue_timeout"
			case errors.Is(cause, context.DeadlineExceeded):
				failure = "compute_timeout"
			case errors.Is(cause, context.Canceled):
				failure = "cancelled"
			case errors.Is(cause, ai.ErrInvalidResult):
				failure = "invalid_result"
			case cause != nil:
				failure = "source_failure"
			}
			work, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := s.submitAI(work, item.request, item.seat, item.phase, result, failure)
			if err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrUnavailable) && s.reportError != nil {
				s.reportError(err)
			}
			s.wakeAI()
		}})
		if errors.Is(err, ai.ErrCapacity) {
			if err := s.submitAI(ctx, item.request, item.seat, item.phase, ai.Result{}, "queue_capacity"); err != nil && !errors.Is(err, ErrConflict) {
				return err
			}
		} else if err != nil && !errors.Is(err, ai.ErrClosed) {
			return err
		}
	}
	return nil
}

func (s *Service) submitAI(ctx context.Context, request ai.Request, seat int, phase db.U128, result ai.Result, failure string) error {
	tx, now, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	v, err := s.session(ctx, tx, request.Window.Match)
	if err != nil {
		return notFound(err)
	}
	if v.Economy != AIEconomy || v.AI == nil || v.AI.BotSeat != seat || v.Seats[seat].Kind != "bot" {
		return ErrInvariant
	}
	validWindow := v.PhaseSeq == phase
	if rules, ok := v.rules.(SequentialRules); ok {
		ids, err := rules.Decisions(v.Mode, v.Payload.Rules)
		if err != nil {
			return err
		}
		validWindow = ids[seat] != "" && ids[seat] == request.Window.Token
	}
	if v.State != "active" || !validWindow || v.Seats[seat].Locked {
		return ErrConflict
	}
	if facts, changed, err := s.advance(ctx, tx, &v, now); err != nil {
		return err
	} else if changed {
		if err := tx.Commit(); err != nil {
			return err
		}
		s.publish(ctx, facts)
		return nil
	}
	var action any
	if failure == "" {
		action, err = ai.ResolveChoice(request, result)
		if err != nil {
			failure = "invalid_candidate"
		}
	}
	var body []byte
	if failure == "" {
		body, err = Encode(action)
		if err != nil {
			failure = "invalid_result"
		}
	}
	if failure != "" {
		body, err = s.aiFallback(&v, seat, request)
		if err != nil {
			return err
		}
	}
	accepted, err := v.rules.Accept(v.Mode, v.Payload.Rules, seat, body)
	if err != nil {
		if failure != "" {
			return err
		}
		failure = "illegal_action"
		body, err = s.aiFallback(&v, seat, request)
		if err != nil {
			return err
		}
		accepted, err = v.rules.Accept(v.Mode, v.Payload.Rules, seat, body)
		if err != nil {
			return err
		}
	}
	expected := v.Revision
	v.Revision, err = increment(v.Revision)
	if err != nil {
		return err
	}
	origin := "ai"
	if failure != "" {
		origin = "fallback"
	}
	facts, err := s.commitAcceptedAction(ctx, tx, &v, expected, seat, accepted, origin, failure, now)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return classify(err)
	}
	s.publish(ctx, facts)
	return nil
}

// aiFallback keeps provider failures separate from human timeout accounting.
func (s *Service) aiFallback(v *sessionRecord, seat int, request ai.Request) (json.RawMessage, error) {
	if rules, ok := v.rules.(interface {
		AIFallback(string, json.RawMessage, int, io.Reader) (json.RawMessage, error)
	}); ok {
		return rules.AIFallback(v.Mode, v.Payload.Rules, seat, aiRandomReader{request.Random})
	}
	return v.rules.Automatic(v.Mode, v.Payload.Rules, seat)
}

type aiRandomReader struct{ random ai.Random }

func (r aiRandomReader) Read(body []byte) (int, error) {
	for i := range body {
		body[i] = byte(r.random.IntN(256))
	}
	return len(body), nil
}
