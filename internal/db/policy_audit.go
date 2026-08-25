package db

import (
	"context"
	"database/sql"
	"fmt"
)

// appendPolicyAuditTx appends a bounded strategy transition in the caller's
// transaction. It deliberately accepts only the closed sets represented by
// the schema; invalid values fail before the transaction can commit.
func appendPolicyAuditTx(ctx context.Context, tx *sql.Tx, actorUserID int64, actorRole, resourceType string, resourceID int64, policy string, oldValue, newValue, now int64) error {
	if tx == nil || resourceID <= 0 || now <= 0 || (oldValue != 0 && oldValue != 1) || (newValue != 0 && newValue != 1) {
		return ErrInvalidValue
	}
	if actorRole != "owner" && actorRole != "admin" && actorRole != "level5" {
		return ErrInvalidValue
	}
	if resourceType != "endpoint_key" && resourceType != "model" && resourceType != "charity_model" {
		return ErrInvalidValue
	}
	if (resourceType == "endpoint_key" || resourceType == "model") && actorRole != "owner" {
		return ErrInvalidValue
	}
	if resourceType == "charity_model" && actorRole != "admin" && actorRole != "level5" {
		return ErrInvalidValue
	}
	if policy != "force_store_false" && policy != "flatten_tool_calls" {
		return ErrInvalidValue
	}
	var actor any
	if actorUserID > 0 {
		actor = actorUserID
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO policy_audits
	(actor_user_id, actor_role, resource_type, resource_id, policy, old_value, new_value, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, actor, actorRole, resourceType, resourceID, policy, oldValue, newValue, now); err != nil {
		return fmt.Errorf("append policy audit: %w", err)
	}
	return nil
}

// ListPolicyAudits is intentionally a narrow internal inspection helper. It
// returns only the append-only policy metadata and never exposes request data.
func (s *Store) ListPolicyAudits(ctx context.Context, resourceType string, resourceID, limit int64) ([]PolicyAudit, error) {
	if s == nil || resourceID <= 0 || limit <= 0 {
		return nil, ErrInvalidValue
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, actor_user_id, actor_role, resource_type, resource_id, policy, old_value, new_value, created_at
FROM policy_audits WHERE resource_type=? AND resource_id=? ORDER BY id LIMIT ?`, resourceType, resourceID, limit)
	if err != nil {
		return nil, fmt.Errorf("list policy audits: %w", err)
	}
	defer rows.Close()
	var out []PolicyAudit
	for rows.Next() {
		var a PolicyAudit
		var actor sql.NullInt64
		var oldValue, newValue int
		if err := rows.Scan(&a.ID, &actor, &a.ActorRole, &a.ResourceType, &a.ResourceID, &a.Policy, &oldValue, &newValue, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan policy audit: %w", err)
		}
		if actor.Valid {
			a.ActorUserID = &actor.Int64
		}
		a.OldValue = oldValue != 0
		a.NewValue = newValue != 0
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate policy audits: %w", err)
	}
	return out, nil
}

type PolicyAudit struct {
	ID           int64
	ActorUserID  *int64
	ActorRole    string
	ResourceType string
	ResourceID   int64
	Policy       string
	OldValue     bool
	NewValue     bool
	CreatedAt    int64
}
