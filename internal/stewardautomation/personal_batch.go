package stewardautomation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

var errBatchCapacity = errors.New("automation batch capacity exhausted")

type batchIdentity struct {
	ID                   string
	UserID, TargetID     int64
	Kind                 string
	Count                int
	CreatedAt, ExpiresAt int64
	Actor, Digest        [32]byte
}

func (s *Service) beginPersonal(ctx context.Context, userID int64) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = s.auth.AuthorizePersonalCaller(ctx, tx, userID); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}
func personalActor(userID int64, step bool) ([32]byte, error) {
	kind := "personal_automation_root"
	if step {
		kind = "personal_automation_step"
	}
	return idempotency.ActorScopeHash(kind, strconv.FormatInt(userID, 10))
}
func childKey(batchID string, index int, purpose string) string {
	value := sha256.Sum256([]byte("NonbiriAPI/personal-automation-child/v1:" + batchID + ":" + strconv.Itoa(index) + ":" + purpose))
	return base64.RawURLEncoding.EncodeToString(value[:])
}
func childBody(batch batchIdentity, index int, purpose string) []byte {
	body, _ := json.Marshal(struct {
		Batch   string `json:"batch_digest"`
		Index   int    `json:"index"`
		Purpose string `json:"purpose"`
	}{hex.EncodeToString(batch.Digest[:]), index, purpose})
	return body
}
func childDigest(batch batchIdentity, index int, purpose string, actor [32]byte) ([32]byte, error) {
	body := childBody(batch, index, purpose)
	return idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: http.MethodPost, Route: "/api/automation/internal", PathResourceIDs: []string{batch.ID}, Body: body})
}
func (s *Service) registerBatch(ctx context.Context, userID, target int64, kind, key string, canonical []byte, count int) (batchIdentity, error) {
	actor, err := personalActor(userID, false)
	if err != nil {
		return batchIdentity{}, err
	}
	route := PersonalPrefix + "endpoints/{id}/keys/batch-import"
	if kind == "binding_append" {
		route = PersonalPrefix + "models/{id}/bindings/batch"
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: http.MethodPost, Route: route, PathResourceIDs: []string{strconv.FormatInt(target, 10)}, Body: canonical})
	if err != nil {
		return batchIdentity{}, errInvalid
	}
	keyHash, err := idempotency.KeyHash(key)
	if err != nil {
		return batchIdentity{}, errInvalid
	}
	tx, err := s.beginPersonal(ctx, userID)
	if err != nil {
		return batchIdentity{}, err
	}
	defer tx.Rollback()
	if err = s.resources.AutomationTargetInTransaction(ctx, tx, userID, target, kind == "binding_append"); err != nil {
		return batchIdentity{}, err
	}
	now := s.now().Unix()
	batch := batchIdentity{UserID: userID, TargetID: target, Kind: kind, Count: count, CreatedAt: now, ExpiresAt: now + idempotency.ReplayWindowSeconds, Actor: actor, Digest: digest}
	var stored []byte
	err = tx.QueryRowContext(ctx, `SELECT id,request_hash,kind,target_id,item_count,created_at,expires_at FROM personal_automation_batches WHERE user_id=? AND root_key_hash=?`, userID, keyHash[:]).Scan(&batch.ID, &stored, &batch.Kind, &batch.TargetID, &batch.Count, &batch.CreatedAt, &batch.ExpiresAt)
	if err == nil && batch.ExpiresAt > now {
		if string(stored) != string(digest[:]) {
			return batchIdentity{}, idempotency.ErrConflict
		}
		return batch, tx.Commit()
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return batchIdentity{}, err
	}
	if err == nil {
		if err = s.deleteBatchTx(ctx, tx, batch.ID, userID); err != nil {
			return batchIdentity{}, err
		}
	}
	batch = batchIdentity{UserID: userID, TargetID: target, Kind: kind, Count: count, CreatedAt: now, ExpiresAt: now + idempotency.ReplayWindowSeconds, Actor: actor, Digest: digest}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopePersonalAutomation, ActorHash: actor, Key: key, RequestHash: digest, DecisionNow: now})
	if err != nil {
		return batchIdentity{}, err
	}
	if decision.Kind == idempotency.Replay {
		return batchIdentity{}, idempotency.ErrState
	}
	batch.ID, err = db.GenerateOpaqueID("pab_")
	if err != nil {
		return batchIdentity{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO personal_automation_batches(id,user_id,actor_scope_hash,root_key_hash,request_hash,kind,target_id,item_count,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, batch.ID, userID, actor[:], keyHash[:], digest[:], kind, target, count, now, batch.ExpiresAt)
	if err != nil {
		if strings.Contains(err.Error(), "automation batch capacity exhausted") {
			return batchIdentity{}, errBatchCapacity
		}
		return batchIdentity{}, err
	}
	registration, _ := json.Marshal(struct {
		BatchID string `json:"batch_id"`
	}{batch.ID})
	if err = idempotency.Complete(ctx, tx, decision, http.StatusOK, registration); err != nil {
		return batchIdentity{}, err
	}
	return batch, tx.Commit()
}
func (s *Service) batchTx(ctx context.Context, batch batchIdentity) (*sql.Tx, error) {
	tx, err := s.beginPersonal(ctx, batch.UserID)
	if err != nil {
		return nil, err
	}
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM personal_automation_batches WHERE id=? AND user_id=? AND request_hash=? AND expires_at>?)`, batch.ID, batch.UserID, batch.Digest[:], s.now().Unix()).Scan(&active)
	if err == nil && !active {
		err = idempotency.ErrConflict
	}
	if err == nil {
		err = s.resources.AutomationTargetInTransaction(ctx, tx, batch.UserID, batch.TargetID, batch.Kind == "binding_append")
	}
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *Service) runItem(ctx context.Context, batch batchIdentity, index int, keyID string, discovery *resources.DiscoveryAccepted, mutate func(context.Context, *sql.Tx) (resources.AutomationMutation, error)) (itemResult, error) {
	tx, err := s.batchTx(ctx, batch)
	if err != nil {
		return itemResult{}, err
	}
	defer tx.Rollback()
	var recorded string
	err = tx.QueryRowContext(ctx, `SELECT result_json FROM personal_automation_steps WHERE batch_id=? AND item_index=?`, batch.ID, index).Scan(&recorded)
	if err == nil {
		var out itemResult
		err = json.Unmarshal([]byte(recorded), &out)
		return out, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return itemResult{}, err
	}
	actor, _ := personalActor(batch.UserID, true)
	digest, _ := childDigest(batch, index, "result", actor)
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopePersonalAutomation, ActorHash: actor, Key: childKey(batch.ID, index, "result"), RequestHash: digest, DecisionNow: batch.CreatedAt})
	if err != nil {
		return itemResult{}, err
	}
	if decision.Kind != idempotency.Proceed {
		return itemResult{}, idempotency.ErrState
	}
	if _, err = tx.ExecContext(ctx, "SAVEPOINT automation_item"); err != nil {
		return itemResult{}, err
	}
	value, mutationErr := mutate(ctx, tx)
	if ctx.Err() != nil {
		return itemResult{}, ctx.Err()
	}
	if errors.Is(mutationErr, authz.ErrUnauthorized) || errors.Is(mutationErr, authz.ErrForbidden) || errors.Is(mutationErr, resources.ErrUnauthorized) || errors.Is(mutationErr, resources.ErrForbidden) {
		return itemResult{}, mutationErr
	}
	out := itemResult{Index: index, EndpointKeyID: keyID, Status: "success", Outcome: value.Outcome}
	if batch.Kind == "key_import" {
		out.EndpointKeyID = value.ID
	} else {
		out.BindingID = value.ID
	}
	if mutationErr != nil {
		if _, err = tx.ExecContext(ctx, "ROLLBACK TO automation_item"); err != nil {
			return itemResult{}, err
		}
		out = itemResult{Index: index, EndpointKeyID: keyID, Status: "failed"}
		out.Code, out.Message = safePersonalError(mutationErr)
	}
	if _, err = tx.ExecContext(ctx, "RELEASE automation_item"); err != nil {
		return itemResult{}, err
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return itemResult{}, err
	}
	var op any
	var revision any
	if discovery != nil {
		op = discovery.OperationID
		revision, _ = strconv.ParseInt(discovery.Evidence.Revision, 10, 64)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO personal_automation_steps(batch_id,user_id,item_index,status,result_json,discovery_operation_id,discovery_revision,created_at) VALUES(?,?,?,?,?,?,?,?)`, batch.ID, batch.UserID, index, out.Status, string(encoded), op, revision, s.now().Unix())
	if err != nil {
		return itemResult{}, err
	}
	if err = idempotency.Complete(ctx, tx, decision, http.StatusOK, encoded); err != nil {
		return itemResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return itemResult{}, err
	}
	return out, nil
}
func (s *Service) discoverySnapshot(ctx context.Context, batch batchIdentity, index int, endpointID, keyID int64) (resources.AutomationDiscoverySnapshot, error) {
	tx, err := s.batchTx(ctx, batch)
	if err != nil {
		return resources.AutomationDiscoverySnapshot{}, err
	}
	defer tx.Rollback()
	actor, _ := personalActor(batch.UserID, true)
	digest, _ := childDigest(batch, index, "discovery_snapshot", actor)
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopePersonalAutomation, ActorHash: actor, Key: childKey(batch.ID, index, "discovery_snapshot"), RequestHash: digest, DecisionNow: batch.CreatedAt})
	if err != nil {
		return resources.AutomationDiscoverySnapshot{}, err
	}
	if decision.Kind == idempotency.Replay {
		var out resources.AutomationDiscoverySnapshot
		err = json.Unmarshal(decision.ResponseBody, &out)
		if err == nil {
			err = s.resources.VerifyAutomationDiscoverySnapshotInTransaction(ctx, tx, batch.UserID, out)
		}
		return out, err
	}
	out, err := s.resources.AutomationDiscoverySnapshotInTransaction(ctx, tx, batch.UserID, endpointID, keyID)
	if err != nil {
		return out, err
	}
	body, _ := json.Marshal(out)
	if err = idempotency.Complete(ctx, tx, decision, http.StatusOK, body); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) batchResults(ctx context.Context, batch batchIdentity, keys []string) (batchResult, int, error) {
	tx, err := s.batchTx(ctx, batch)
	if err != nil {
		return batchResult{}, 0, err
	}
	defer tx.Rollback()
	out := batchResult{Results: make([]itemResult, batch.Count)}
	if batch.Kind == "key_import" {
		out.EndpointID = strconv.FormatInt(batch.TargetID, 10)
	} else {
		out.ModelID = strconv.FormatInt(batch.TargetID, 10)
	}
	for i := range out.Results {
		out.Results[i] = itemResult{Index: i, Status: "incomplete", Code: "incomplete", Message: "No result is confirmed; use the same idempotency key to check and continue."}
		if keys != nil {
			out.Results[i].EndpointKeyID = keys[i]
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT item_index,result_json FROM personal_automation_steps WHERE batch_id=? ORDER BY item_index`, batch.ID)
	if err != nil {
		return out, 0, err
	}
	for rows.Next() {
		var i int
		var body string
		if err = rows.Scan(&i, &body); err != nil {
			rows.Close()
			return out, 0, err
		}
		var item itemResult
		if err = json.Unmarshal([]byte(body), &item); err != nil {
			rows.Close()
			return out, 0, err
		}
		out.Results[i] = item
		if keys != nil {
			out.Results[i].EndpointKeyID = keys[i]
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, 0, err
	}
	successes, incomplete := 0, false
	for _, item := range out.Results {
		if item.Status == "success" {
			successes++
		}
		if item.Status == "incomplete" {
			incomplete = true
		}
	}
	status := http.StatusUnprocessableEntity
	if successes > 0 {
		status = http.StatusOK
	} else if incomplete {
		status = http.StatusGatewayTimeout
	}
	if err = tx.Commit(); err != nil {
		return out, 0, err
	}
	return out, status, nil
}

func (s *Service) importKeys(ctx context.Context, userID, endpointID int64, key string, input importInput) (batchResult, int, error) {
	if !input.OwnershipConfirmed || len(input.Keys) < 1 || len(input.Keys) > maxKeys {
		return batchResult{}, 0, errInvalid
	}
	canonical, err := canonicalImport(input)
	if err != nil {
		return batchResult{}, 0, err
	}
	defer clear(canonical)
	batch, err := s.registerBatch(ctx, userID, endpointID, "key_import", key, canonical, len(input.Keys))
	if err != nil {
		return batchResult{}, 0, err
	}
	for index, item := range input.Keys {
		if ctx.Err() != nil {
			break
		}
		_, err = s.runItem(ctx, batch, index, "", nil, func(ctx context.Context, tx *sql.Tx) (resources.AutomationMutation, error) {
			secret := []byte(*item.Secret)
			defer clear(secret)
			return s.resources.ImportEndpointKeyInTransaction(ctx, tx, userID, endpointID, resources.CreateEndpointKeyInput{Secret: secret, Note: item.Note, Enabled: enabled(item.Enabled), ForceStoreFalse: item.ForceStoreFalse, OwnershipConfirmed: true, MaxConcurrency: item.MaxConcurrency, MaxRPM: item.MaxRPM})
		})
		if err != nil {
			if isPersonalAuthorityError(err) {
				return batchResult{}, 0, err
			}
			break
		}
	}
	return s.finalBatchResults(ctx, batch, nil)
}
func (s *Service) appendBindings(ctx context.Context, userID, modelID int64, key string, input appendInput) (batchResult, int, error) {
	if len(input.EndpointKeyIDs) < 1 || len(input.EndpointKeyIDs) > maxKeys || !validModelID(input.UpstreamModelID) {
		return batchResult{}, 0, errInvalid
	}
	if input.CatalogMode == "" {
		input.CatalogMode = "manual"
	}
	if input.CatalogMode != "manual" && input.CatalogMode != "refresh" {
		return batchResult{}, 0, errInvalid
	}
	ids := make([]int64, len(input.EndpointKeyIDs))
	seen := map[int64]bool{}
	for i, value := range input.EndpointKeyIDs {
		id, err := numericID(value)
		if err != nil || seen[id] {
			return batchResult{}, 0, errInvalid
		}
		ids[i] = id
		seen[id] = true
	}
	canonical, _ := json.Marshal(input)
	defer clear(canonical)
	batch, err := s.registerBatch(ctx, userID, modelID, "binding_append", key, canonical, len(ids))
	if err != nil {
		return batchResult{}, 0, err
	}
	for index, keyID := range ids {
		if ctx.Err() != nil {
			break
		}
		tx, err := s.batchTx(ctx, batch)
		if err != nil {
			return batchResult{}, 0, err
		}
		var confirmed bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM personal_automation_steps WHERE batch_id=? AND item_index=?)`, batch.ID, index).Scan(&confirmed)
		var existing resources.AutomationMutation
		var endpointID int64
		if err == nil && !confirmed {
			existing, endpointID, err = s.resources.ExistingAutomationBindingInTransaction(ctx, tx, userID, modelID, keyID, input.UpstreamModelID)
		}
		if err == nil && !confirmed && existing.ID == "" && input.CatalogMode == "refresh" {
			err = s.resources.ValidateAutomationBindingSourceInTransaction(ctx, tx, userID, modelID, endpointID, keyID)
		}
		tx.Rollback()
		if confirmed {
			continue
		}
		if isPersonalAuthorityError(err) {
			return batchResult{}, 0, err
		}
		var snapshot resources.AutomationDiscoverySnapshot
		var discovery *resources.DiscoveryAccepted
		if err == nil && existing.ID == "" && input.CatalogMode == "refresh" {
			snapshot, err = s.discoverySnapshot(ctx, batch, index, endpointID, keyID)
			if err == nil {
				work, cancel := context.WithTimeout(ctx, s.discoveryTimeout)
				accepted, discoveryErr := s.resources.RefreshAutomationDiscovery(work, userID, snapshot, childKey(batch.ID, index, "discovery"), childBody(batch, index, "discovery"))
				cancel()
				discovery = &accepted
				err = discoveryErr
				if discoveryErr == nil {
					read, beginErr := s.batchTx(ctx, batch)
					if beginErr == nil {
						revision, _ := strconv.ParseInt(accepted.Evidence.Revision, 10, 64)
						var state string
						err = read.QueryRowContext(ctx, `SELECT state FROM model_discovery_evidence WHERE endpoint_key_id=? AND revision=?`, keyID, revision).Scan(&state)
						if err == nil && state == "checking" {
							err = context.DeadlineExceeded
						}
						if err == nil {
							err = freshModelTx(ctx, read, keyID, revision, input.UpstreamModelID)
						}
						read.Rollback()
					} else {
						err = beginErr
					}
				}
			}
		}
		if isPersonalAuthorityError(err) {
			return batchResult{}, 0, err
		}
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			break
		}
		itemErr := err
		if discovery != nil && discovery.OperationID == "" {
			discovery = nil
		}
		_, err = s.runItem(ctx, batch, index, input.EndpointKeyIDs[index], discovery, func(ctx context.Context, tx *sql.Tx) (resources.AutomationMutation, error) {
			if itemErr != nil {
				return resources.AutomationMutation{}, itemErr
			}
			if discovery != nil {
				if err := s.resources.VerifyAutomationDiscoverySnapshotInTransaction(ctx, tx, userID, snapshot); err != nil {
					return resources.AutomationMutation{}, err
				}
			}
			revision := int64(0)
			if discovery != nil {
				revision, _ = strconv.ParseInt(discovery.Evidence.Revision, 10, 64)
			}
			return s.resources.AppendAutomationBindingInTransaction(ctx, tx, userID, modelID, keyID, input.UpstreamModelID, input.CatalogMode == "manual", revision)
		})
		if err != nil {
			if isPersonalAuthorityError(err) {
				return batchResult{}, 0, err
			}
			break
		}
	}
	return s.finalBatchResults(ctx, batch, input.EndpointKeyIDs)
}
func (s *Service) finalBatchResults(ctx context.Context, batch batchIdentity, keys []string) (batchResult, int, error) {
	// Budget exhaustion stops writes, but the response may still report only
	// confirmed rows after a fresh live-authority check.
	read, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.batchResults(read, batch, keys)
}
func isPersonalAuthorityError(err error) bool {
	return errors.Is(err, authz.ErrUnauthorized) || errors.Is(err, authz.ErrForbidden) || errors.Is(err, resources.ErrUnauthorized) || errors.Is(err, resources.ErrForbidden)
}
func safePersonalError(err error) (string, string) {
	code, message := safeError(err)
	if code == "forbidden" {
		message = "An active account is required."
	}
	if code == "not_found" {
		message = "The resource is missing or unavailable to this account."
	}
	return code, message
}
