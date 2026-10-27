package aiagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
)

func (s *Service) memoryAccess(ctx context.Context, tx *sql.Tx, actor Actor, kind string, owner int64, write bool) error {
	if actor.Automatic || actor.Role != "admin" && actor.Role != "level5" && actor.Role != "level6" {
		return ErrForbidden
	}
	if kind != "personal" && kind != "chat" && kind != "auto_review" || owner < 0 || kind != "personal" && owner != 0 {
		return ErrInvalid
	}
	if kind == "personal" && owner != 0 && owner != actor.ID && actor.Role != "admin" || kind != "personal" && write && actor.Role != "admin" {
		return ErrForbidden
	}
	permission := "ai.chat"
	if kind != "personal" && write {
		permission = "ai.memory.edit"
	}
	return s.authorize(ctx, tx, actor, permission)
}

func personalOwner(ctx context.Context, tx *sql.Tx, actor Actor, owner int64) (MemoryOwner, error) {
	if owner == 0 || owner == actor.ID {
		return MemoryOwner{ID: actor.ID, Role: actor.Role, Label: actor.Label}, nil
	}
	var value MemoryOwner
	err := tx.QueryRowContext(ctx, `SELECT id,CASE WHEN is_admin=1 THEN 'admin' ELSE 'level'||COALESCE(level,auto_level) END,username FROM users WHERE id=? AND (is_admin=1 OR COALESCE(level,auto_level) IN (5,6))`, owner).Scan(&value.ID, &value.Role, &value.Label)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT actor_id,actor_role,actor_label FROM ai_agent_personal_memories WHERE actor_id=?`, owner).Scan(&value.ID, &value.Role, &value.Label)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return value, err
}

func readPersonalMemory(ctx context.Context, tx *sql.Tx, owner int64) (Memory, error) {
	value := Memory{Kind: "personal", OwnerID: owner}
	err := tx.QueryRowContext(ctx, `SELECT content,revision,updated_at,updated_by FROM ai_agent_personal_memories WHERE actor_id=?`, owner).Scan(&value.Content, &value.Revision, &value.UpdatedAt, &value.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	return value, err
}

func (s *Service) previewMemory(ctx context.Context, actor Actor, input memoryEdit) (Memory, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Memory{}, err
	}
	defer tx.Rollback()
	if err = s.memoryAccess(ctx, tx, actor, input.Kind, input.OwnerID, true); err != nil {
		return Memory{}, err
	}
	if input.Kind != "personal" {
		return readMemory(ctx, tx, input.Kind)
	}
	owner, err := personalOwner(ctx, tx, actor, input.OwnerID)
	if err != nil {
		return Memory{}, err
	}
	return readPersonalMemory(ctx, tx, owner.ID)
}

func (s *Service) PersonalMemory(ctx context.Context, actor Actor, owner int64) (Memory, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Memory{}, err
	}
	defer tx.Rollback()
	if err = s.memoryAccess(ctx, tx, actor, "personal", owner, false); err != nil {
		return Memory{}, err
	}
	identity, err := personalOwner(ctx, tx, actor, owner)
	if err != nil {
		return Memory{}, err
	}
	value, err := readPersonalMemory(ctx, tx, identity.ID)
	value.Writable = true
	return value, err
}

func (s *Service) writePersonalMemory(ctx context.Context, tx *sql.Tx, actor Actor, owner int64, content string, expected, now int64) (Memory, error) {
	content = strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	if expected < 0 || expected == math.MaxInt64 || len(content) > 4<<20 {
		return Memory{}, ErrInvalid
	}
	if err := s.memoryAccess(ctx, tx, actor, "personal", owner, true); err != nil {
		return Memory{}, err
	}
	identity, err := personalOwner(ctx, tx, actor, owner)
	if err != nil {
		return Memory{}, err
	}
	before, err := readPersonalMemory(ctx, tx, identity.ID)
	if err != nil {
		return Memory{}, err
	}
	if before.Revision != expected {
		return Memory{}, ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_agent_personal_memories(actor_id,actor_role,actor_label,content,revision,updated_at,updated_by) VALUES(?,?,?,?,?,?,?) ON CONFLICT(actor_id) DO UPDATE SET actor_role=excluded.actor_role,actor_label=excluded.actor_label,content=excluded.content,revision=excluded.revision,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, identity.ID, identity.Role, identity.Label, content, expected+1, now, actor.Label)
	if err != nil {
		return Memory{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_agent_personal_memory_history(owner_id,before_content,after_content,revision,actor_id,actor_label,created_at) VALUES(?,?,?,?,?,?,?)`, identity.ID, before.Content, content, expected+1, actor.ID, actor.Label, now)
	return Memory{Kind: "personal", OwnerID: identity.ID, Content: content, Revision: expected + 1, UpdatedAt: now, UpdatedBy: actor.Label, Writable: true}, err
}

func (s *Service) PutPersonalMemory(ctx context.Context, actor Actor, owner int64, content string, expected int64) (Memory, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Memory{}, err
	}
	defer tx.Rollback()
	value, err := s.writePersonalMemory(ctx, tx, actor, owner, content, expected, s.now().UnixMilli())
	if err != nil {
		return Memory{}, err
	}
	return value, tx.Commit()
}

func (s *Service) MemoryOwners(ctx context.Context, actor Actor, query string, page, size int) (MemoryOwnerPage, error) {
	if actor.Role != "admin" || actor.Automatic {
		return MemoryOwnerPage{}, ErrForbidden
	}
	if page < 1 || page > 1000000 || size < 1 || size > 100 || len(query) > 128 {
		return MemoryOwnerPage{}, ErrInvalid
	}
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return MemoryOwnerPage{}, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, actor, "ai.config"); err != nil {
		return MemoryOwnerPage{}, err
	}
	// Live names take precedence, while saved memories survive account removal.
	const owners = `WITH owners AS (
 SELECT u.id,CASE WHEN u.is_admin=1 THEN 'admin' ELSE 'level'||COALESCE(u.level,u.auto_level) END role,u.username label
 FROM users u WHERE (u.is_admin=1 OR COALESCE(u.level,u.auto_level) IN (5,6))
 UNION ALL SELECT m.actor_id,m.actor_role,m.actor_label FROM ai_agent_personal_memories m
 WHERE NOT EXISTS(SELECT 1 FROM users u WHERE u.id=m.actor_id AND (u.is_admin=1 OR COALESCE(u.level,u.auto_level) IN (5,6)))
 ) `
	query = strings.TrimSpace(query)
	where := ` WHERE (?='' OR instr(lower(label),lower(?))>0 OR CAST(id AS TEXT)=?)`
	out := MemoryOwnerPage{Page: Page[MemoryOwner]{Items: []MemoryOwner{}, Page: page, PageSize: size}, CurrentOwnerID: actor.ID}
	if err = tx.QueryRowContext(ctx, owners+`SELECT COUNT(*) FROM owners`+where, query, query, query).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, owners+`SELECT id,role,label FROM owners`+where+` ORDER BY label,id LIMIT ? OFFSET ?`, query, query, query, size, (page-1)*size)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var value MemoryOwner
		if err = rows.Scan(&value.ID, &value.Role, &value.Label); err != nil {
			return out, err
		}
		out.Items = append(out.Items, value)
	}
	return out, rows.Err()
}

func interlocutor(job Job) string {
	raw, _ := json.Marshal(map[string]string{"id": strconv.FormatInt(job.ActorID, 10), "role": job.ActorRole, "name": job.ActorLabel})
	return string(raw)
}
