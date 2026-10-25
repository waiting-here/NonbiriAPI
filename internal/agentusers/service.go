// Package agentusers composes existing account domains inside one approved
// caller transaction, without managing a model client or session credentials.
package agentusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/adminusers"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"sort"
	"strconv"
	"sync"
)

type Config struct {
	Users    *adminusers.Service
	Accounts *lifecycle.Coordinator
	Auth     *auth.Runtime
}
type Service struct {
	users    *adminusers.Service
	accounts *lifecycle.Coordinator
	auth     *auth.Runtime
}

func New(config Config) (*Service, error) {
	if config.Users == nil || config.Accounts == nil || config.Auth == nil {
		return nil, errors.New("agentusers: users, accounts and authentication are required")
	}
	return &Service{users: config.Users, accounts: config.Accounts, auth: config.Auth}, nil
}
func (s *Service) Read(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, args json.RawMessage, authorize agentplan.Authorize) (json.RawMessage, error) {
	if s == nil || tx == nil || actor.Automatic {
		return nil, adminusers.ErrForbidden
	}
	in, err := adminusers.ParseAgentRead(args)
	if err != nil {
		return nil, err
	}
	if in.Kind == "mfa" {
		id, _ := strconv.ParseInt(in.UserID, 10, 64)
		value, err := s.auth.AgentReadMFA(ctx, tx, actor, id, authorize)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	return s.users.AgentRead(ctx, tx, actor, in, authorize)
}
func (s *Service) Preview(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, op agentplan.Operation, authorize agentplan.Authorize) (agentplan.Preview, error) {
	if s == nil || tx == nil || actor.Automatic {
		return agentplan.Preview{}, adminusers.ErrForbidden
	}
	p, err := adminusers.ParseAgentOperation(op)
	if err != nil {
		return agentplan.Preview{}, err
	}
	preview, err := s.users.AgentPreview(ctx, tx, actor, p, authorize)
	if err != nil {
		return preview, err
	}
	var after any
	if err = json.Unmarshal(op.Input, &after); err != nil {
		return preview, err
	}
	pretty, err := json.MarshalIndent(after, "", "  ")
	if err != nil {
		return preview, err
	}
	preview.After = string(pretty)
	return preview, nil
}

type preparedBatch struct {
	service    *Service
	actor      agentplan.Actor
	authorize  agentplan.Authorize
	ops        []adminusers.AgentOperation
	identities map[int64]string
	deletions  map[int64]*lifecycle.AgentDeletion
	releases   []func()
	finalizers []func(bool)
	next       int
	failed     bool
	finalized  bool
	once       sync.Once
}

func (s *Service) Prepare(ctx context.Context, actor agentplan.Actor, ops []agentplan.Operation, authorize agentplan.Authorize) (agentplan.PreparedBatch, error) {
	if s == nil || ctx == nil || actor.Automatic || authorize == nil || len(ops) == 0 || len(ops) > 100 {
		return nil, adminusers.ErrForbidden
	}
	p := &preparedBatch{service: s, actor: actor, authorize: authorize, identities: make(map[int64]string), deletions: make(map[int64]*lifecycle.AgentDeletion)}
	for _, op := range ops {
		parsed, err := adminusers.ParseAgentOperation(op)
		if err != nil {
			return nil, err
		}
		p.ops = append(p.ops, parsed)
	}
	targets, err := s.users.AgentPreflight(ctx, actor, p.ops, authorize)
	if err != nil {
		return nil, err
	}
	needs := make(map[int64]adminusers.AgentTarget)
	deleting := make(map[int64]bool)
	for i, op := range p.ops {
		if op.Kind == "users.delete" {
			if deleting[op.UserID] {
				return nil, adminusers.ErrInvalidRequest
			}
			deleting[op.UserID] = true
		}
		if op.NeedsIdentity() || op.Kind == "users.delete" {
			needs[op.UserID] = targets[i]
		}
	}
	ids := make([]int64, 0, len(needs))
	for id := range needs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		target := needs[id]
		p.identities[id] = target.DiscordID
		// Account retirement already owns the identity gate. Acquiring a second
		// identity change for an earlier operation on this account would deadlock.
		if deleting[id] {
			deletion, err := s.accounts.PrepareAgentDeletion(ctx, id)
			if err != nil {
				p.Finalize(false)
				return nil, err
			}
			p.deletions[id] = deletion
		} else {
			release, err := s.users.AgentLockIdentity(ctx, target)
			if err != nil {
				p.Finalize(false)
				return nil, err
			}
			p.releases = append(p.releases, release)
		}
	}
	return p, nil
}
func (p *preparedBatch) ApplyOperation(ctx context.Context, tx *sql.Tx, index int) (json.RawMessage, error) {
	if p == nil || p.finalized || p.failed || tx == nil || index != p.next || index < 0 || index >= len(p.ops) {
		return nil, adminusers.ErrConflict
	}
	op := p.ops[index]
	var raw json.RawMessage
	var err error
	switch op.Kind {
	case "users.delete":
		err = p.deletions[op.UserID].Apply(ctx, tx, p.actor, op.Revision.Decimal(), p.authorize)
		if err == nil {
			raw, _ = json.Marshal(map[string]any{"user_id": strconv.FormatInt(op.UserID, 10), "deleted": true})
		}
	case "users.mfa_reset":
		var finish func(bool)
		finish, err = p.service.auth.AgentResetMFA(ctx, tx, p.actor, op.UserID, op.Revision, op.MFAVersion, p.authorize)
		if finish != nil {
			p.finalizers = append(p.finalizers, finish)
		}
		if err == nil {
			raw, _ = json.Marshal(map[string]any{"user_id": strconv.FormatInt(op.UserID, 10), "mfa_reset": true})
		}
	default:
		var finish func(bool)
		raw, finish, err = p.service.users.AgentApply(ctx, tx, p.actor, op, p.identities[op.UserID], p.authorize)
		if finish != nil {
			p.finalizers = append(p.finalizers, finish)
		}
	}
	if err != nil {
		p.failed = true
		return nil, err
	}
	p.next++
	return raw, nil
}
func (p *preparedBatch) Finalize(committed bool) {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.finalized = true
		committed = committed && !p.failed && p.next == len(p.ops)
		for i := len(p.finalizers) - 1; i >= 0; i-- {
			p.finalizers[i](committed)
		}
		ids := make([]int64, 0, len(p.deletions))
		for id := range p.deletions {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
		for _, id := range ids {
			p.deletions[id].Finalize(committed)
		}
		for i := len(p.releases) - 1; i >= 0; i-- {
			p.releases[i]()
		}
	})
}
