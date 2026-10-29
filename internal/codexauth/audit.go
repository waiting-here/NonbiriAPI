package codexauth

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func (m *Manager) acceptLogin(ctx context.Context, p *pendingLogin) error {
	if _, present := managementlog.MetadataFromContext(ctx); !present {
		return nil
	}
	tx, err := m.config.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = m.config.AdminAuthorize(ctx, tx); err != nil {
		return err
	}
	managementlog.SetCorrelation(ctx, "codex_login", p.status.ID)
	managementlog.SetTarget(ctx, "codex_connection", "1", "Codex")
	managementlog.SetResult(ctx, managementlog.Result{Outcome: managementlog.Accepted})
	if err = managementlog.Commit(ctx, tx); err != nil {
		return err
	}
	p.audited = true
	return nil
}
func (m *Manager) captureLogin(ctx context.Context, tx *sql.Tx, p *pendingLogin) error {
	if !p.audited {
		return nil
	}
	return managementlog.StartRelatedCaptureTx(ctx, tx, "codex_login", p.status.ID)
}
func (m *Manager) finishLoginTx(ctx context.Context, tx *sql.Tx, p *pendingLogin, outcome managementlog.Outcome, code string) error {
	if !p.audited {
		return nil
	}
	return managementlog.AppendRelatedTx(ctx, tx, "codex_login", p.status.ID, managementlog.Item{
		Action: "connect", TargetKind: "codex_connection", TargetID: "1", TargetName: "Codex", Outcome: outcome, Code: code,
	}, true)
}
func (m *Manager) auditLoginEnd(p *pendingLogin, outcome managementlog.Outcome, code string) error {
	if !p.audited {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := m.config.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = m.finishLoginTx(ctx, tx, p, outcome, code); err != nil {
		return err
	}
	return tx.Commit()
}
