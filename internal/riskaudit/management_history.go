package riskaudit

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func commitScanResult(ctx context.Context, tx *sql.Tx, id string) error {
	var state, reason string
	if err := tx.QueryRowContext(ctx, "SELECT state,reason FROM risk_client_scans WHERE id=?", id).Scan(&state, &reason); err != nil {
		return err
	}
	if state != "queued" && state != "running" {
		outcome := managementlog.Succeeded
		switch state {
		case "failed":
			outcome = managementlog.Failed
		case "cancelled":
			outcome = managementlog.Rejected
		case "limited":
			outcome = managementlog.Partial
		}
		if err := managementlog.AppendRelatedTx(ctx, tx, "risk_scan", id, managementlog.Item{Action: "scan", TargetKind: "risk_scan", TargetID: id, Outcome: outcome, Code: reason}, true); err != nil {
			return err
		}
	} else {
		if err := managementlog.AppendRelatedTx(ctx, tx, "risk_scan", id, managementlog.Item{Action: "scan", TargetKind: "risk_scan", TargetID: id, Outcome: managementlog.Accepted, Code: "running"}, false); err != nil {
			return err
		}
	}
	return tx.Commit()
}
