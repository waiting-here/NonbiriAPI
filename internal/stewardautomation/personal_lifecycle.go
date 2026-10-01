package stewardautomation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// Cleanup follows the batch's exact receipt mapping, including discovery
// receipts. Other control-plane operations by the same owner remain intact.
func (s *Service) deleteBatchTx(ctx context.Context, tx *sql.Tx, id string, userID int64) error {
	var rootActor, rootKey []byte
	var count int
	err := tx.QueryRowContext(ctx, `SELECT actor_scope_hash,root_key_hash,item_count FROM personal_automation_batches WHERE id=? AND user_id=?`, id, userID).Scan(&rootActor, &rootKey, &count)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	stepActor, _ := personalActor(userID, true)
	ownerActor, _ := idempotency.ActorScopeHash("user", strconv.FormatInt(userID, 10))
	stepKeys := make([]any, 0, count*2)
	discoveryKeys := make([]any, 0, count)
	for index := 0; index < count; index++ {
		for _, purpose := range []string{"result", "discovery_snapshot"} {
			hash, _ := idempotency.KeyHash(childKey(id, index, purpose))
			stepKeys = append(stepKeys, hash[:])
		}
		hash, _ := idempotency.KeyHash(childKey(id, index, "discovery"))
		discoveryKeys = append(discoveryKeys, hash[:])
	}
	marks := func(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
	if _, err = tx.ExecContext(ctx, `DELETE FROM idempotency_records WHERE scope='personal_automation' AND actor_scope_hash=? AND key_hash=?`, rootActor, rootKey); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM idempotency_records WHERE scope='personal_automation' AND actor_scope_hash=? AND key_hash IN (`+marks(len(stepKeys))+`)`, append([]any{stepActor[:]}, stepKeys...)...); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM idempotency_records WHERE scope='model_discovery' AND actor_scope_hash=? AND key_hash IN (`+marks(len(discoveryKeys))+`)`, append([]any{ownerActor[:]}, discoveryKeys...)...); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM resource_operation_status WHERE user_id=? AND actor_scope_hash=? AND key_hash IN (`+marks(len(discoveryKeys))+`)`, append([]any{userID, ownerActor[:]}, discoveryKeys...)...); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM personal_automation_batches WHERE id=? AND user_id=?`, id, userID)
	return err
}
func (s *Service) PrepareDelete(ctx context.Context, tx *sql.Tx, request lifecycle.DeleteRequest) (lifecycle.DeleteFinalizer, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM personal_automation_batches WHERE user_id=? ORDER BY id`, request.UserID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err = s.deleteBatchTx(ctx, tx, id, request.UserID); err != nil {
			return nil, err
		}
	}
	return nil, nil
}
func (s *Service) RecoverBeforeListener(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	return s.Retain(ctx, now, limit, deadline)
}
func (s *Service) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	result := lifecycle.WorkResult{}
	if ctx == nil || limit < 1 || limit > lifecycle.WorkerBatchLimit {
		return result, lifecycle.ErrInvalid
	}
	work, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	tx, err := s.db.BeginTx(work, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(work, `SELECT id,user_id FROM personal_automation_batches WHERE expires_at<=? ORDER BY expires_at,id LIMIT ?`, now, limit+1)
	if err != nil {
		return result, err
	}
	type batch struct {
		id   string
		user int64
	}
	list := []batch{}
	for rows.Next() {
		var value batch
		if err = rows.Scan(&value.id, &value.user); err != nil {
			rows.Close()
			return result, err
		}
		list = append(list, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if len(list) > limit {
		result.More = true
		list = list[:limit]
	}
	for _, value := range list {
		if err = s.deleteBatchTx(work, tx, value.id, value.user); err != nil {
			return lifecycle.WorkResult{}, err
		}
		result.Processed++
	}
	if err = tx.Commit(); err != nil {
		return lifecycle.WorkResult{}, err
	}
	return result, nil
}
func (s *Service) ExportPersonalAutomation(ctx context.Context, tx *sql.Tx, request lifecycle.ExportRequest) ([]lifecycle.PersonalAutomationBatchExport, error) {
	if request.UserID < 1 || request.Limit < 1 || request.Limit > lifecycle.CollectionLimit {
		return nil, lifecycle.ErrInvalid
	}
	out := []lifecycle.PersonalAutomationBatchExport{}
	rows, err := tx.QueryContext(ctx, `SELECT id,kind,target_id,item_count,created_at,expires_at FROM personal_automation_batches WHERE user_id=? AND expires_at>? ORDER BY created_at,id LIMIT ?`, request.UserID, request.DecisionNow, request.Limit+1)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item lifecycle.PersonalAutomationBatchExport
		var target int64
		if err = rows.Scan(&item.ID, &item.Kind, &target, &item.ItemCount, &item.CreatedAt, &item.ExpiresAt); err != nil {
			rows.Close()
			return nil, err
		}
		item.TargetID = strconv.FormatInt(target, 10)
		item.Results = make([]lifecycle.PersonalAutomationStepExport, item.ItemCount)
		for i := range item.Results {
			item.Results[i] = lifecycle.PersonalAutomationStepExport{Index: i, Status: "incomplete"}
		}
		out = append(out, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(out) > request.Limit {
		return nil, lifecycle.ErrTooLarge
	}
	for i := range out {
		rows, err = tx.QueryContext(ctx, `SELECT item_index,result_json FROM personal_automation_steps WHERE batch_id=? ORDER BY item_index`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var index int
			var body string
			if err = rows.Scan(&index, &body); err != nil {
				rows.Close()
				return nil, err
			}
			var item itemResult
			if err = json.Unmarshal([]byte(body), &item); err != nil {
				rows.Close()
				return nil, err
			}
			out[i].Results[index] = lifecycle.PersonalAutomationStepExport{Index: index, Status: item.Status, Outcome: item.Outcome, EndpointKeyID: item.EndpointKeyID, BindingID: item.BindingID, Code: item.Code, Message: item.Message}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
