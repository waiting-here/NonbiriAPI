package reports

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

type reportIssueTarget struct {
	ownerUserID   int64
	endpointKeyID int64
}

type reportIssueTransitions map[reportIssueTarget]bool

func readCaseIssueTargetsTx(ctx context.Context, tx *sql.Tx, caseID string) ([]reportIssueTarget, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT e.user_id,k.id
FROM endpoint_key_suspensions s
JOIN endpoint_keys k ON k.id=s.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id
WHERE s.reason_type='report_case' AND s.report_case_id=?
ORDER BY e.user_id,k.id`, caseID)
	if err != nil {
		return nil, fmt.Errorf("reports: list report-reason issue targets: %w", err)
	}
	defer rows.Close()
	targets := make([]reportIssueTarget, 0)
	for rows.Next() {
		var target reportIssueTarget
		if err := rows.Scan(&target.ownerUserID, &target.endpointKeyID); err != nil {
			return nil, fmt.Errorf("reports: scan report-reason issue target: %w", err)
		}
		if target.ownerUserID <= 0 || target.endpointKeyID <= 0 {
			return nil, ErrInvariant
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reports: iterate report-reason issue targets: %w", err)
	}
	return targets, nil
}

func readFingerprintIssueTargetsTx(
	ctx context.Context,
	tx *sql.Tx,
	fingerprint []byte,
	connectorType, baseURL string,
) ([]reportIssueTarget, error) {
	if len(fingerprint) != 32 || connectorType == "" || baseURL == "" {
		return nil, ErrInvariant
	}
	rows, err := tx.QueryContext(ctx, `
SELECT e.user_id,k.id
FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
WHERE k.secret_fingerprint=? AND e.connector_type=? AND e.base_url=?
ORDER BY e.user_id,k.id`, fingerprint, connectorType, baseURL)
	if err != nil {
		return nil, fmt.Errorf("reports: list fingerprint issue targets: %w", err)
	}
	defer rows.Close()
	targets := make([]reportIssueTarget, 0)
	for rows.Next() {
		var target reportIssueTarget
		if err := rows.Scan(&target.ownerUserID, &target.endpointKeyID); err != nil {
			return nil, fmt.Errorf("reports: scan fingerprint issue target: %w", err)
		}
		if target.ownerUserID <= 0 || target.endpointKeyID <= 0 {
			return nil, ErrInvariant
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reports: iterate fingerprint issue targets: %w", err)
	}
	return targets, nil
}

func reportReasonActiveTx(ctx context.Context, tx *sql.Tx, target reportIssueTarget) (bool, error) {
	if target.ownerUserID <= 0 || target.endpointKeyID <= 0 {
		return false, ErrInvariant
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM endpoint_key_suspensions s
 JOIN endpoint_keys k ON k.id=s.endpoint_key_id
 JOIN endpoints e ON e.id=k.endpoint_id
 WHERE s.endpoint_key_id=? AND s.reason_type='report_case' AND e.user_id=?
)`, target.endpointKeyID, target.ownerUserID).Scan(&active); err != nil {
		return false, fmt.Errorf("reports: read aggregate report reason: %w", err)
	}
	return active == 1, nil
}

func snapshotReportIssueTargetsTx(
	ctx context.Context,
	tx *sql.Tx,
	transitions reportIssueTransitions,
	targets []reportIssueTarget,
) error {
	if transitions == nil {
		return ErrInvariant
	}
	for _, target := range targets {
		if _, exists := transitions[target]; exists {
			continue
		}
		active, err := reportReasonActiveTx(ctx, tx, target)
		if err != nil {
			return err
		}
		transitions[target] = active
	}
	return nil
}

func (repository *Repository) reconcileReportIssueTransitionsTx(
	ctx context.Context,
	tx *sql.Tx,
	transitions reportIssueTransitions,
) error {
	targets := make([]reportIssueTarget, 0, len(transitions))
	for target := range transitions {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].ownerUserID != targets[j].ownerUserID {
			return targets[i].ownerUserID < targets[j].ownerUserID
		}
		return targets[i].endpointKeyID < targets[j].endpointKeyID
	})
	for _, target := range targets {
		active, err := reportReasonActiveTx(ctx, tx, target)
		if err != nil {
			return err
		}
		if active == transitions[target] {
			continue
		}
		if err := repository.issues.ReconcileReportReason(
			ctx, tx, target.ownerUserID, target.endpointKeyID,
		); err != nil {
			return fmt.Errorf("reports: reconcile report-reason issue projection: %w", err)
		}
	}
	return nil
}
