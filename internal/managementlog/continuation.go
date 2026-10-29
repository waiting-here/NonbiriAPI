package managementlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// AppendRelatedTx records a persisted task's result in its business transaction.
// The accepted operation supplies its original authority snapshot after restart.
// Pre-upgrade jobs have no parent and are deliberately not backfilled.
func AppendRelatedTx(ctx context.Context, tx *sql.Tx, kind, id string, item Item, terminal bool) error {
	parent, err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationColumns+" FROM management_operations WHERE correlation_kind=? AND correlation_id=? AND cleaning=0 AND outcome='accepted' ORDER BY id DESC LIMIT 1", kind, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return appendOperationTx(ctx, tx, parent, []Item{item}, terminal)
}
func appendOperationTx(ctx context.Context, tx *sql.Tx, parent Operation, items []Item, terminal bool) error {
	items = cloneItems(items)
	if len(items) > 0 {
		var err error
		items, _, err = enrichCaptureTx(ctx, tx, parent.EventID, items)
		if err != nil {
			return err
		}
	}
	for i := range items {
		items[i].TargetName = snapshotName(items[i].TargetName)
		var dropped bool
		items[i].Changes, dropped = normalizeChanges(items[i].Changes)
		items[i].Details.Truncated = items[i].Details.Truncated || dropped
		items[i].Details.Version = 1
	}
	check := Record{Metadata: parent.Metadata, Result: Result{Outcome: parent.Outcome, Items: items}}
	if err := validateRecord(check); err != nil {
		return err
	}
	var oldKey string
	if err := tx.QueryRowContext(ctx, "SELECT group_key FROM management_operations WHERE id=?", parent.ID).Scan(&oldKey); err != nil {
		return err
	}
	for _, item := range items {
		parent.Targets++
		if item.Changes == nil {
			item.Changes = []Change{}
		}
		changes, _ := json.Marshal(item.Changes)
		details, _ := json.Marshal(item.Details)
		if _, err := tx.ExecContext(ctx, "INSERT INTO management_operation_items(operation_id,ordinal,action,target_kind,target_id,target_name,outcome,code,changes,details) VALUES(?,?,?,?,?,?,?,?,?,?)", parent.ID, parent.Targets, item.Action, item.TargetKind, item.TargetID, item.TargetName, item.Outcome, item.Code, string(changes), string(details)); err != nil {
			return err
		}
		if err := insertTermsTx(ctx, tx, parent.ID, parent.Targets, item); err != nil {
			return err
		}
	}
	unknown := parent.Code == "outcome_unconfirmed"
	for _, item := range items {
		unknown = unknown || item.Outcome == Unknown
	}
	if unknown {
		parent.Code = "outcome_unconfirmed"
	}
	success, skipped, failed := counts(items)
	// Remove the previous contribution before changing the parent outcome.
	if _, err := tx.ExecContext(ctx, `UPDATE management_operation_rollups SET operations=operations-1,targets=targets-(SELECT targets FROM management_operations WHERE id=?),succeeded=succeeded-(SELECT succeeded FROM management_operations WHERE id=?),skipped=skipped-(SELECT skipped FROM management_operations WHERE id=?),failed=failed-(SELECT failed FROM management_operations WHERE id=?) WHERE group_key=?`, parent.ID, parent.ID, parent.ID, parent.ID, oldKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM management_operation_rollups WHERE group_key=? AND operations=0", oldKey); err != nil {
		return err
	}
	parent.Succeeded += success
	parent.Skipped += skipped
	parent.Failed += failed
	parent.Outcome = Accepted
	if terminal {
		var partial, rejected bool
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(outcome='partial'),0),COALESCE(MIN(outcome='rejected'),0) FROM management_operation_items WHERE operation_id=?", parent.ID).Scan(&partial, &rejected); err != nil {
			return err
		}
		switch {
		case unknown && parent.Succeeded == 0 && parent.Failed == 0:
			parent.Outcome = Unknown
		case partial || unknown:
			parent.Outcome = Partial
		case rejected:
			parent.Outcome = Rejected
		case parent.Failed > 0 && parent.Succeeded > 0:
			parent.Outcome = Partial
		case parent.Failed > 0:
			parent.Outcome = Failed
		case parent.Succeeded > 0 && parent.Skipped > 0:
			parent.Outcome = Partial
		case parent.Succeeded > 0:
			parent.Outcome = Succeeded
		default:
			parent.Outcome = NoChange
		}
	}
	nextKey := groupKey(Record{Metadata: parent.Metadata, Result: Result{Outcome: parent.Outcome}})
	if _, err := tx.ExecContext(ctx, "UPDATE management_operations SET group_key=?,outcome=?,code=?,targets=?,succeeded=?,skipped=?,failed=? WHERE id=?", nextKey, parent.Outcome, parent.Code, parent.Targets, parent.Succeeded, parent.Skipped, parent.Failed, parent.ID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO management_operation_rollups(group_key,bucket,source,domain,action,actor_id,actor_name,actor_role,authorizer_id,authorizer_name,authorizer_role,outcome,operations,targets,succeeded,skipped,failed,min_id,max_id)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,?,?)
 ON CONFLICT(group_key) DO UPDATE SET operations=operations+1,targets=targets+excluded.targets,succeeded=succeeded+excluded.succeeded,skipped=skipped+excluded.skipped,failed=failed+excluded.failed,max_id=MAX(max_id,excluded.max_id),min_id=MIN(min_id,excluded.min_id)`,
		nextKey, parent.CreatedAt/3600000*3600000, parent.Source, parent.Domain, parent.Action, parent.Actor.ID, parent.Actor.Name, parent.Actor.Role, parent.Authorizer.ID, parent.Authorizer.Name, parent.Authorizer.Role, parent.Outcome, parent.Targets, parent.Succeeded, parent.Skipped, parent.Failed, parent.ID, parent.ID)
	return err
}

// AppendContextTx maintains a partial batch across its existing per-item
// transactions without changing the atomicity of the business operation.
func AppendContextTx(ctx context.Context, tx *sql.Tx, item Item, terminal bool) error {
	return AppendItemsContextTx(ctx, tx, []Item{item}, terminal)
}

// AppendItemsContextTx adds one committed chunk without rewriting its rollup
// once per target. Callers retain their existing transaction and chunk limits.
func AppendItemsContextTx(ctx context.Context, tx *sql.Tx, items []Item, terminal bool) error {
	meta, ok := MetadataFromContext(ctx)
	if !ok {
		return nil
	}
	parent, err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationColumns+" FROM management_operations WHERE event_id=?", meta.EventID))
	if errors.Is(err, sql.ErrNoRows) {
		if len(items) == 0 && terminal {
			_, err = RecordTx(ctx, tx, Record{Metadata: meta, Result: Result{Outcome: NoChange}})
			return err
		}
		code := ""
		for _, item := range items {
			if item.Outcome == Unknown {
				code = "outcome_unconfirmed"
			}
		}
		_, err = RecordTx(ctx, tx, Record{Metadata: meta, Result: Result{Outcome: Accepted, Code: code, Items: items}})
		if err == nil && terminal {
			err = FinalizeContextTx(ctx, tx)
		}
		return err
	}
	if err != nil {
		return err
	}
	return appendOperationTx(ctx, tx, parent, items, terminal)
}
func FinalizeContextTx(ctx context.Context, tx *sql.Tx) error {
	meta, ok := MetadataFromContext(ctx)
	if !ok {
		return nil
	}
	parent, err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationColumns+" FROM management_operations WHERE event_id=?", meta.EventID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return appendOperationTx(ctx, tx, parent, nil, true)
}

// RecoverInterrupted closes synchronous batches interrupted by process exit.
// Call once before serving requests; durable background tasks use their own
// recovery paths and retain their accepted operation.
func (s *Service) RecoverInterrupted(ctx context.Context) error {
	for {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+operationColumns+" FROM management_operations WHERE correlation_kind IN ('automation_batch','request_cleanup') AND outcome='accepted' ORDER BY id LIMIT 32")
		if err != nil {
			tx.Rollback()
			return err
		}
		parents := []Operation{}
		for rows.Next() {
			parent, e := scanOperation(rows)
			if e != nil {
				rows.Close()
				tx.Rollback()
				return e
			}
			parents = append(parents, parent)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			tx.Rollback()
			return err
		}
		for _, parent := range parents {
			err = appendOperationTx(ctx, tx, parent, []Item{{Action: "interrupted", TargetKind: parent.CorrelationKind, TargetID: parent.CorrelationID, Outcome: Unknown, Code: "process_interrupted"}}, true)
			if err != nil {
				tx.Rollback()
				return err
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if len(parents) < 32 {
			return nil
		}
	}
}
