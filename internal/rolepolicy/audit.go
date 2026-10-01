package rolepolicy

import (
	"context"
	"database/sql"
)

// RecordAudit stores authorized model revisions, never configuration or message bodies.
func RecordAudit(ctx context.Context, tx *sql.Tx, actorID int64, actorRole, resource string, modelID, fromRevision, toRevision, at int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO policy_audits(actor_user_id,actor_role,resource_type,resource_id,policy,old_value,new_value,from_revision,to_revision,created_at)
VALUES(?,?,?,?,'role_policy',NULL,NULL,?,?,?)`, actorID, actorRole, resource, modelID, fromRevision, toRevision, at)
	return err
}
