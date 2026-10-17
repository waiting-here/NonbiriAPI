package logapi

import (
	"context"
	"database/sql"
)

type traineeLogRead struct{ allowLogStewardRead }

func (traineeLogRead) AuthorizeStewardLogRead(context.Context, *sql.Tx, int64) (bool, error) {
	return true, nil
}

func (a *logStewardTestAuthorizer) AuthorizeStewardLogRead(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	return false, a.AuthorizeStewardRead(ctx, tx, id)
}
func (a allowLogStewardRead) AuthorizeStewardLogRead(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	return false, a.AuthorizeStewardRead(ctx, tx, id)
}
func (a mismatchStewardAuthorizer) AuthorizeStewardLogRead(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	if id == 5 {
		return true, nil
	}
	return false, a.AuthorizeStewardRead(ctx, tx, id)
}
func (a canceledLogAuthorizer) AuthorizeStewardLogRead(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	return false, a.AuthorizeStewardRead(ctx, tx, id)
}
func (a refuseDiagnosticRead) AuthorizeStewardLogRead(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	return false, a.AuthorizeStewardRead(ctx, tx, id)
}
func (a diagnosticFailureAuthorizer) AuthorizeStewardLogRead(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	return false, a.AuthorizeStewardRead(ctx, tx, id)
}
