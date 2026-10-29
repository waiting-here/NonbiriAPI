package managementlog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

func validOutcome(out Outcome) bool {
	switch out {
	case Succeeded, Failed, Rejected, NoChange, Partial, Accepted, Unknown:
		return true
	}
	return false
}
func validSource(source Source) bool {
	switch source {
	case SourceWeb, SourceCallerKey, SourceAIManual, SourceAIAuto, SourceMainstreamRule:
		return true
	}
	return false
}
func textOK(value string, budget int) bool {
	return len(value) <= budget && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r")
}
func tokenOK(value string, budget int) bool {
	if len(value) > budget {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_-./:{} ", c)) {
			return false
		}
	}
	return true
}
func validateRecord(value Record) error {
	if value.EventID == "" || !tokenOK(value.EventID, 128) || value.CreatedAt < 0 || value.CreatedAt > 253402300799999 || !validSource(value.Source) || !validOutcome(value.Outcome) || value.Domain == "" || value.Action == "" {
		return ErrInvalid
	}
	for _, v := range []string{value.Domain, value.Action, value.ExecutorKind, value.CorrelationKind} {
		if !tokenOK(v, 160) {
			return ErrInvalid
		}
	}
	for _, v := range []string{value.AuthorizationRef, value.CorrelationID, value.Actor.ID, value.Authorizer.ID} {
		if !textOK(v, 192) {
			return ErrInvalid
		}
	}
	for _, person := range []Subject{value.Actor, value.Authorizer} {
		if !textOK(person.Name, 256) || !tokenOK(person.Role, 64) {
			return ErrInvalid
		}
	}
	if !tokenOK(value.Code, 128) || !textOK(value.Summary, 1024) {
		return ErrInvalid
	}
	for _, item := range value.Items {
		if !validOutcome(item.Outcome) || !tokenOK(item.Action, 160) || !tokenOK(item.TargetKind, 96) || !textOK(item.TargetID, 192) || !textOK(item.TargetName, 256) || !tokenOK(item.Code, 128) || len(item.Changes) > 64 {
			return ErrInvalid
		}
		for _, change := range item.Changes {
			if change.Field == "" || !tokenOK(change.Field, 96) {
				return ErrInvalid
			}
			switch change.Operation {
			case "updated", "cleared", "created", "deleted", "enabled", "disabled":
			default:
				return ErrInvalid
			}
			for _, text := range []*string{change.Before, change.After} {
				if text != nil && (!textOK(*text, 2048) || utf8.RuneCountInString(*text) > 512) {
					return ErrInvalid
				}
			}
			if !tokenOK(change.Unit, 32) {
				return ErrInvalid
			}
		}
		encoded, err := json.Marshal(item.Details)
		if err != nil || len(encoded) > 65536 {
			return ErrInvalid
		}
	}
	return nil
}
func groupKey(value Record) string {
	encoded, _ := json.Marshal([]any{value.CreatedAt / 3600000, value.Source, value.Domain, value.Action, value.Actor, value.Authorizer, value.Outcome})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
func counts(items []Item) (success, skipped, failed int64) {
	for _, item := range items {
		switch item.Outcome {
		case Succeeded:
			success++
		case NoChange:
			skipped++
		case Failed, Rejected:
			failed++
		}
	}
	return
}

// RecordTx inserts one immutable operation. Reusing EventID in the same logical
// operation is harmless; it does not create another event or increment rollups.
func RecordTx(ctx context.Context, tx *sql.Tx, value Record) (int64, error) {
	if tx == nil {
		return 0, ErrInvalid
	}
	if value.EventID == "" {
		value.EventID = newEventID()
	}
	if value.CreatedAt == 0 {
		value.CreatedAt = nowUnix()
	}
	value.Actor.Name = snapshotName(value.Actor.Name)
	value.Authorizer.Name = snapshotName(value.Authorizer.Name)
	value.Items = cloneItems(value.Items)
	for i := range value.Items {
		value.Items[i].TargetName = snapshotName(value.Items[i].TargetName)
	}
	if len(value.Items) == 0 {
		value.Items = []Item{{Action: value.Action, TargetKind: value.Domain, Outcome: value.Outcome, Code: value.Code, Changes: []Change{}}}
	}
	var err error
	var captured bool
	value.Items, captured, err = enrichCaptureTx(ctx, tx, value.EventID, value.Items)
	if err != nil {
		return 0, err
	}
	allUnchanged := len(value.Items) > 0
	for i := range value.Items {
		var dropped bool
		value.Items[i].Changes, dropped = normalizeChanges(value.Items[i].Changes)
		value.Items[i].Details.Truncated = value.Items[i].Details.Truncated || dropped
		value.Items[i].Details.Version = 1
		allUnchanged = allUnchanged && value.Items[i].Outcome == NoChange
	}
	if allUnchanged && value.Outcome == Succeeded {
		value.Outcome = NoChange
	}
	if err := validateRecord(value); err != nil {
		return 0, err
	}
	key := groupKey(value)
	bucket := value.CreatedAt / 3600000 * 3600000
	success, skipped, failed := counts(value.Items)
	result, err := tx.ExecContext(ctx, `INSERT INTO management_operations(event_id,created_at,bucket,group_key,source,executor_kind,actor_id,actor_name,actor_role,authorizer_id,authorizer_name,authorizer_role,authorization_ref,domain,action,correlation_kind,correlation_id,outcome,code,summary,targets,succeeded,skipped,failed)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(event_id) DO NOTHING`,
		value.EventID, value.CreatedAt, bucket, key, value.Source, value.ExecutorKind, value.Actor.ID, value.Actor.Name, value.Actor.Role, value.Authorizer.ID, value.Authorizer.Name, value.Authorizer.Role, value.AuthorizationRef, value.Domain, value.Action, value.CorrelationKind, value.CorrelationID, value.Outcome, value.Code, value.Summary, len(value.Items), success, skipped, failed)
	if err != nil {
		return 0, fmt.Errorf("record management operation: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	var id int64
	if n == 0 {
		err = tx.QueryRowContext(ctx, "SELECT id FROM management_operations WHERE event_id=?", value.EventID).Scan(&id)
		if err == nil && captured {
			parent, readErr := scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationColumns+" FROM management_operations WHERE id=?", id))
			if readErr != nil {
				return 0, readErr
			}
			err = appendOperationTx(ctx, tx, parent, value.Items, value.Outcome != Accepted)
		}
		return id, err
	}
	id, err = result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE management_operations SET target_name=? WHERE id=?", value.Items[0].TargetName, id); err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO management_operation_items(operation_id,ordinal,action,target_kind,target_id,target_name,outcome,code,changes,details) VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for i, item := range value.Items {
		if item.Changes == nil {
			item.Changes = []Change{}
		}
		changes, err := json.Marshal(item.Changes)
		if err != nil {
			return 0, err
		}
		details, _ := json.Marshal(item.Details)
		if _, err = stmt.ExecContext(ctx, id, i+1, item.Action, item.TargetKind, item.TargetID, item.TargetName, item.Outcome, item.Code, string(changes), string(details)); err != nil {
			return 0, err
		}
		if err = insertTermsTx(ctx, tx, id, int64(i+1), item); err != nil {
			return 0, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO management_operation_rollups(group_key,bucket,source,domain,action,actor_id,actor_name,actor_role,authorizer_id,authorizer_name,authorizer_role,outcome,operations,targets,succeeded,skipped,failed,min_id,max_id)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,?,?)
 ON CONFLICT(group_key) DO UPDATE SET operations=operations+1,targets=targets+excluded.targets,succeeded=succeeded+excluded.succeeded,skipped=skipped+excluded.skipped,failed=failed+excluded.failed,max_id=MAX(max_id,excluded.max_id),min_id=MIN(min_id,excluded.min_id)`,
		key, bucket, value.Source, value.Domain, value.Action, value.Actor.ID, value.Actor.Name, value.Actor.Role, value.Authorizer.ID, value.Authorizer.Name, value.Authorizer.Role, value.Outcome, len(value.Items), success, skipped, failed, id, id)
	return id, err
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func SubjectTx(ctx context.Context, db queryer, id int64) (Subject, error) {
	var subject Subject
	var admin bool
	var level int
	err := db.QueryRowContext(ctx, "SELECT CAST(id AS TEXT),username,is_admin,COALESCE(level,auto_level) FROM users WHERE id=?", id).Scan(&subject.ID, &subject.Name, &admin, &level)
	if err != nil {
		return subject, err
	}
	if admin {
		subject.Role = "admin"
	} else {
		subject.Role = fmt.Sprintf("level%d", level)
	}
	return subject, nil
}
func requireAdmin(ctx context.Context, db queryer, id int64) error {
	if id <= 0 {
		return ErrForbidden
	}
	var allowed bool
	err := db.QueryRowContext(ctx, "SELECT is_admin=1 AND is_banned=0 FROM users WHERE id=?", id).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
		return ErrForbidden
	}
	return err
}

func snapshotName(value string) string {
	if len(value) <= 256 {
		return value
	}
	value = value[:253]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}
