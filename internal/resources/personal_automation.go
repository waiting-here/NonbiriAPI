package resources

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type AutomationMutation struct {
	ID      string
	Outcome string
}

// AutomationTargetInTransaction checks only the fixed owner target; it does
// not create or enable resources. The automation owner authorizes the transaction.
func (r *Repository) AutomationTargetInTransaction(ctx context.Context, tx *sql.Tx, userID, targetID int64, model bool) error {
	if model {
		_, err := getModelTx(ctx, tx, userID, targetID)
		return err
	}
	_, err := getEndpointTx(ctx, tx, userID, targetID)
	return err
}

// ImportEndpointKeyInTransaction preserves the first current body match before
// considering new-key capacity. Ordinary browser creation retains its semantics.
func (r *Repository) ImportEndpointKeyInTransaction(ctx context.Context, tx *sql.Tx, userID, endpointID int64, input CreateEndpointKeyInput) (AutomationMutation, error) {
	if r == nil || tx == nil || !validCreateEndpointKey(input) {
		return AutomationMutation{}, ErrInvalidRequest
	}
	if err := r.finalAuth.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return AutomationMutation{}, err
	}
	if _, err := getEndpointTx(ctx, tx, userID, endpointID); err != nil {
		return AutomationMutation{}, err
	}
	matcher, ok := r.secrets.(SecretMatcher)
	if !ok {
		return AutomationMutation{}, ErrUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,secret_ref_id FROM endpoint_keys WHERE endpoint_id=? ORDER BY id`, endpointID)
	if err != nil {
		return AutomationMutation{}, err
	}
	type reference struct{ id, ref int64 }
	refs := []reference{}
	for rows.Next() {
		var ref reference
		if err = rows.Scan(&ref.id, &ref.ref); err != nil {
			rows.Close()
			return AutomationMutation{}, err
		}
		refs = append(refs, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return AutomationMutation{}, err
	}
	for _, ref := range refs {
		same, err := matcher.MatchEndpointSecret(ctx, tx, ref.ref, input.Secret)
		if err != nil {
			return AutomationMutation{}, err
		}
		if same {
			return AutomationMutation{ID: strconv.FormatInt(ref.id, 10), Outcome: "existing"}, nil
		}
	}
	key, err := r.CreateEndpointKeyInTransaction(ctx, tx, userID, endpointID, input)
	if err != nil {
		return AutomationMutation{}, err
	}
	return AutomationMutation{ID: key.ID, Outcome: "created"}, nil
}

// ExistingAutomationBindingInTransaction checks live ownership before confirming
// an existing tuple, including disabled or no-longer-callable connections.
func (r *Repository) ExistingAutomationBindingInTransaction(ctx context.Context, tx *sql.Tx, userID, modelID, keyID int64, upstream string) (AutomationMutation, int64, error) {
	if _, err := getModelTx(ctx, tx, userID, modelID); err != nil {
		return AutomationMutation{}, 0, err
	}
	var endpointID int64
	err := tx.QueryRowContext(ctx, `SELECT e.id FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=? AND e.user_id=?`, keyID, userID).Scan(&endpointID)
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationMutation{}, 0, ErrNotFound
	}
	if err != nil {
		return AutomationMutation{}, 0, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM model_bindings WHERE model_id=? AND endpoint_key_id=? AND upstream_model_id=?`, modelID, keyID, upstream).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationMutation{}, endpointID, nil
	}
	if err != nil {
		return AutomationMutation{}, 0, err
	}
	return AutomationMutation{ID: strconv.FormatInt(id, 10), Outcome: "existing"}, endpointID, nil
}

// AppendAutomationBindingInTransaction adds an absent tuple at the current
// tail and reconciles the existing route projection in the same transaction.
func (r *Repository) AppendAutomationBindingInTransaction(ctx context.Context, tx *sql.Tx, userID, modelID, keyID int64, upstream string, manual bool, discoveryRevision int64) (AutomationMutation, error) {
	if r == nil || tx == nil || !validateCatalogText(upstream, 1, 512) {
		return AutomationMutation{}, ErrInvalidRequest
	}
	if err := r.finalAuth.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return AutomationMutation{}, err
	}
	existing, endpointID, err := r.ExistingAutomationBindingInTransaction(ctx, tx, userID, modelID, keyID, upstream)
	if err != nil || existing.ID != "" {
		return existing, err
	}
	model, err := automationBindingSourceTx(ctx, tx, userID, modelID, endpointID, keyID)
	if err != nil {
		return AutomationMutation{}, err
	}
	_, eligible, err := bindingSelectionConnectorTx(ctx, tx, userID, keyID, upstream)
	if err != nil {
		return AutomationMutation{}, err
	}
	if !manual {
		var current int64
		var state string
		if err = tx.QueryRowContext(ctx, `SELECT revision,state FROM model_discovery_evidence WHERE endpoint_key_id=?`, keyID).Scan(&current, &state); err != nil {
			return AutomationMutation{}, err
		}
		if current != discoveryRevision || state != "succeeded" {
			return AutomationMutation{}, ErrConflict
		}
		var fresh bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_pair_catalog WHERE endpoint_key_id=? AND normalized_model_id=? AND automatic_revision=? AND automatic_supports>0)`, keyID, upstream, discoveryRevision).Scan(&fresh); err != nil {
			return AutomationMutation{}, err
		}
		if !fresh {
			return AutomationMutation{}, ErrNotFound
		}
	} else if !eligible {
		if err = r.EnsureManualEntryInTransaction(ctx, tx, userID, endpointID, keyID, upstream); err != nil {
			return AutomationMutation{}, err
		}
	}
	count, _ := strconv.ParseInt(model.BindingCount, 10, 64)
	limit, err := readSiteLimitTx(ctx, tx, "default_binding_limit", 1, 10000)
	if err != nil {
		return AutomationMutation{}, err
	}
	if limit > maxBindingBatch {
		limit = maxBindingBatch
	}
	if count >= limit {
		return AutomationMutation{}, ErrResourceLimit
	}
	var tail int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ord),-1)+1 FROM model_bindings WHERE model_id=?`, modelID).Scan(&tail); err != nil {
		return AutomationMutation{}, err
	}
	now, err := r.nowUnix()
	if err != nil {
		return AutomationMutation{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO model_bindings(model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,?,?,?)`, modelID, keyID, upstream, tail, now, now)
	if err != nil {
		return AutomationMutation{}, conflictOrError("append automation binding", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AutomationMutation{}, err
	}
	revision, _ := strconv.ParseInt(model.BindingRevision, 10, 64)
	if err = advanceBindingRevisionTx(ctx, tx, userID, modelID, revision, now); err != nil {
		return AutomationMutation{}, err
	}
	if err = r.projection.ReconcileRoutingProjection(ctx, tx, userID, modelID); err != nil {
		return AutomationMutation{}, err
	}
	return AutomationMutation{ID: strconv.FormatInt(id, 10), Outcome: "created"}, nil
}

// ValidateAutomationBindingSourceInTransaction rejects unusable new sources
// before a refresh spends an upstream request. The final append repeats these
// same rules in its authoritative write transaction.
func (r *Repository) ValidateAutomationBindingSourceInTransaction(ctx context.Context, tx *sql.Tx, userID, modelID, endpointID, keyID int64) error {
	_, err := automationBindingSourceTx(ctx, tx, userID, modelID, endpointID, keyID)
	return err
}
func automationBindingSourceTx(ctx context.Context, tx *sql.Tx, userID, modelID, endpointID, keyID int64) (Model, error) {
	model, err := getModelTx(ctx, tx, userID, modelID)
	if err != nil {
		return Model{}, err
	}
	endpoint, err := getEndpointTx(ctx, tx, userID, endpointID)
	if err != nil {
		return Model{}, err
	}
	key, err := getEndpointKeyTx(ctx, tx, userID, endpointID, keyID)
	if err != nil {
		return Model{}, err
	}
	if !endpoint.Enabled || !key.Enabled {
		return Model{}, ErrNotFound
	}
	if key.SuspensionState != "none" {
		return Model{}, ErrResourceLocked
	}
	if model.FlattenToolCalls && endpoint.ConnectorType != string(connectorcontract.TypeOpenAICompatible) {
		return Model{}, ErrInvalidRequest
	}
	return model, nil
}
