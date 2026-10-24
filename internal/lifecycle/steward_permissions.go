package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

type StewardPermissionAuditExport struct {
	Previous  authz.StewardPermissions `json:"previous"`
	Next      authz.StewardPermissions `json:"next"`
	CreatedAt int64                    `json:"created_at"`
}

type StewardPermissionsExport struct {
	Effective authz.StewardPermissions       `json:"effective"`
	Stored    *authz.StewardPermissions      `json:"stored"`
	History   []StewardPermissionAuditExport `json:"history"`
}

func exportStewardPermissions(ctx context.Context, tx *sql.Tx, userID int64) (StewardPermissionsExport, error) {
	out := StewardPermissionsExport{History: []StewardPermissionAuditExport{}}
	var level int
	var revision int64
	var permissions, limits, scope string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(u.level,u.auto_level),COALESCE(p.revision,0),COALESCE(p.permissions,''),COALESCE(p.limits,''),COALESCE(p.donation_scope,'standard') FROM users u LEFT JOIN steward_permissions p ON p.user_id=u.id WHERE u.id=?`, userID).Scan(&level, &revision, &permissions, &limits, &scope)
	if err != nil {
		return out, err
	}
	out.Effective, err = authz.DecodeStewardPermissions(level, revision, permissions, limits, scope)
	if err != nil {
		return out, err
	}
	if revision > 0 {
		p, e := authz.DecodeStewardPermissions(5, revision, permissions, limits, scope)
		if e != nil {
			return out, e
		}
		out.Stored = &p
	}
	rows, err := tx.QueryContext(ctx, `SELECT previous_policy,next_policy,created_at FROM steward_permission_audits WHERE user_id=? ORDER BY id LIMIT ?`, userID, CollectionLimit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var audit StewardPermissionAuditExport
		var previous, next string
		if err := rows.Scan(&previous, &next, &audit.CreatedAt); err != nil {
			return out, err
		}
		if json.Unmarshal([]byte(previous), &audit.Previous) != nil || json.Unmarshal([]byte(next), &audit.Next) != nil {
			return out, ErrUnavailable
		}
		out.History = append(out.History, audit)
		if len(out.History) > CollectionLimit {
			return out, ErrTooLarge
		}
	}
	return out, rows.Err()
}
