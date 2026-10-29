package v1

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func migrateOperatorDiagnostics(ctx context.Context, tx *sql.Tx) error {
	if err := rebuildTables(ctx, tx, []tableRebuild{{Name: "agent_request_drivers", SQL: db.AgentRequestDriversSQL}}); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, db.OperatorDiagnosticsSQL)
	return err
}
