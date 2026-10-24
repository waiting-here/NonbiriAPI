package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationstate"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) BindingCandidatesAdmin(ctx context.Context, modelID int64, query CandidateQuery) ([]AdminBindingCandidate, int64, string, error) {
	return s.bindingCandidates(ctx, roleAdmin, 0, modelID, query)
}

func (s *Service) BindingCandidatesSteward(ctx context.Context, actorUserID, modelID int64, query CandidateQuery) ([]StewardBindingCandidate, int64, string, error) {
	items, nextKey, nextModel, err := s.bindingCandidates(ctx, roleSteward, actorUserID, modelID, query)
	if err != nil {
		return nil, 0, "", err
	}
	out := make([]StewardBindingCandidate, len(items))
	for index := range items {
		out[index] = stewardCandidate(items[index])
	}
	return out, nextKey, nextModel, nil
}

func (s *Service) bindingCandidates(ctx context.Context, role roleKind, actorUserID, modelID int64, query CandidateQuery) ([]AdminBindingCandidate, int64, string, error) {
	if s == nil || s.db == nil || ctx == nil || modelID <= 0 || query.DonationID < 0 || query.DonationKeyID < 0 ||
		query.AfterKeyID < 0 || query.Limit < 1 || query.Limit > maxPageLimit ||
		(role != roleAdmin && role != roleSteward || role == roleSteward && actorUserID <= 0) ||
		(query.Source != "" && query.Source != "automatic" && query.Source != "manual") || !donationstate.Valid(query.KeyState) ||
		!utf8.ValidString(query.Query) || utf8.RuneCountInString(query.Query) > 512 {
		return nil, 0, "", ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return nil, 0, "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, "", fmt.Errorf("charity routing: begin candidate read: %w", err)
	}
	defer tx.Rollback()
	scope, err := s.managementScope(ctx, tx, role, actorUserID, modelID, false)
	if err != nil {
		return nil, 0, "", err
	}
	if err := s.donationState.MaterializeDueExpiriesTx(ctx, tx, now, 100); err != nil {
		return nil, 0, "", fmt.Errorf("charity routing: materialize candidate expiry: %w", err)
	}
	query.maintenanceScope = scope
	statement, args := candidatePageSelection(modelID, now, query)
	statement += ` ORDER BY dk.id,pc.normalized_model_id LIMIT ?`
	args = append(args, query.Limit+1)
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, 0, "", fmt.Errorf("charity routing: list binding candidates: %w", err)
	}
	defer rows.Close()
	items := make([]AdminBindingCandidate, 0, query.Limit+1)
	keyIDs := make([]int64, 0, query.Limit+1)
	for rows.Next() {
		var item AdminBindingCandidate
		var keyID, donationID int64
		var automatic, manual int64
		if err := rows.Scan(&keyID, &donationID, &item.Source.ConnectorType, &item.Source.CanonicalBaseURL,
			&item.Source.DisplayHead, &item.Source.DisplayTail, &item.UpstreamModelID, &automatic, &manual, &item.Source.MaxConcurrency, &item.Source.MaxRPM); err != nil {
			return nil, 0, "", fmt.Errorf("charity routing: scan binding candidate: %w", err)
		}
		item.DonationKeyID = strconv.FormatInt(keyID, 10)
		item.DonationID = strconv.FormatInt(donationID, 10)
		item.SourceTypes = sourceTypes(automatic, manual)
		items = append(items, item)
		keyIDs = append(keyIDs, keyID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, "", fmt.Errorf("charity routing: iterate binding candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, "", err
	}
	if err := annotateBindingCandidates(ctx, tx, items, modelID, scope.Trainee); err != nil {
		return nil, 0, "", err
	}
	if err := annotateCandidateServicesTx(ctx, tx, items, modelID); err != nil {
		return nil, 0, "", err
	}
	nextKey, nextModel := int64(0), ""
	if len(items) > query.Limit {
		nextKey = keyIDs[query.Limit-1]
		nextModel = items[query.Limit-1].UpstreamModelID
		items = items[:query.Limit]
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, "", fmt.Errorf("charity routing: commit candidate read: %w", err)
	}
	return items, nextKey, nextModel, nil
}

func (s *Service) GetBindingsAdmin(ctx context.Context, modelID int64) (AdminBindings, error) {
	if s == nil || s.db == nil || ctx == nil || modelID <= 0 {
		return AdminBindings{}, ErrInvalidRequest
	}
	tx, _, err := s.beginManagementTx(ctx, roleAdmin, 0, modelID, false, true)
	if err != nil {
		return AdminBindings{}, err
	}
	defer tx.Rollback()
	return readAdminBindingsTx(ctx, tx, modelID)
}

func (s *Service) GetBindingsSteward(ctx context.Context, actorUserID, modelID int64) (StewardBindings, error) {
	if s == nil || s.db == nil || ctx == nil || actorUserID <= 0 || modelID <= 0 || nilDependency(s.roleAuth) {
		return StewardBindings{}, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return StewardBindings{}, fmt.Errorf("charity routing: begin steward bindings read: %w", err)
	}
	defer tx.Rollback()
	scope, err := s.managementScope(ctx, tx, roleSteward, actorUserID, modelID, false)
	if err != nil {
		return StewardBindings{}, err
	}
	value, err := readAdminBindingsTx(ctx, tx, modelID)
	if err != nil {
		return StewardBindings{}, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return StewardBindings{}, err
	}
	value, err = scopedBindingsTx(ctx, tx, scope, value, now)
	if err != nil {
		return StewardBindings{}, err
	}
	if err := tx.Commit(); err != nil {
		return StewardBindings{}, fmt.Errorf("charity routing: commit steward bindings read: %w", err)
	}
	return stewardBindings(value), nil
}

func (s *Service) AddBindingsAdmin(ctx context.Context, modelID int64, mutation resources.ControlMutation, input BindingBatch) (resources.MutationResult[AdminBindings], error) {
	if !validMutation(mutation, http.MethodPost, routeAdminBindingBatch, modelID) {
		return resources.MutationResult[AdminBindings]{}, ErrInvalidRequest
	}
	result, err := s.addBindings(ctx, roleAdmin, 0, modelID, mutation, input, false)
	return result.full, err
}

func (s *Service) AddBindingsSteward(ctx context.Context, actorUserID, modelID int64, mutation resources.ControlMutation, input BindingBatch) (resources.MutationResult[StewardBindings], error) {
	if actorUserID <= 0 || !validMutation(mutation, http.MethodPost, routeStewardBindingBatch, modelID) {
		return resources.MutationResult[StewardBindings]{}, ErrInvalidRequest
	}
	result, err := s.addBindings(ctx, roleSteward, actorUserID, modelID, mutation, input, false)
	if err != nil {
		return resources.MutationResult[StewardBindings]{}, err
	}
	admin := result.full
	value := stewardBindings(admin.Value)
	body, err := json.Marshal(value)
	if err != nil {
		return resources.MutationResult[StewardBindings]{}, ErrUnavailable
	}
	return resources.MutationResult[StewardBindings]{Value: value, Status: admin.Status, Body: body, Replayed: admin.Replayed}, nil
}

func (s *Service) AddBindingsReceiptAdmin(ctx context.Context, modelID int64, mutation resources.ControlMutation, input BindingBatch) (resources.MutationResult[BindingBatchReceipt], error) {
	if !validMutation(mutation, http.MethodPost, routeAdminBindingReceipt, modelID) {
		return resources.MutationResult[BindingBatchReceipt]{}, ErrInvalidRequest
	}
	result, err := s.addBindings(ctx, roleAdmin, 0, modelID, mutation, input, true)
	return result.receipt, err
}

func (s *Service) AddBindingsReceiptSteward(ctx context.Context, actorUserID, modelID int64, mutation resources.ControlMutation, input BindingBatch) (resources.MutationResult[BindingBatchReceipt], error) {
	if actorUserID <= 0 || !validMutation(mutation, http.MethodPost, routeStewardBindingReceipt, modelID) {
		return resources.MutationResult[BindingBatchReceipt]{}, ErrInvalidRequest
	}
	result, err := s.addBindings(ctx, roleSteward, actorUserID, modelID, mutation, input, true)
	return result.receipt, err
}

type bindingBatchMutation struct {
	full    resources.MutationResult[AdminBindings]
	receipt resources.MutationResult[BindingBatchReceipt]
}

func (s *Service) addBindings(ctx context.Context, role roleKind, actorUserID, modelID int64, mutation resources.ControlMutation, input BindingBatch, compact bool) (bindingBatchMutation, error) {
	var result bindingBatchMutation
	expected, err := parseNonnegativeRevision(input.ExpectedBindingRevision)
	selections, validateErr := validateSelections(input.Selections)
	if s == nil || ctx == nil || modelID <= 0 || err != nil || validateErr != nil {
		return result, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return result, err
	}
	tx, scope, err := s.beginManagementTx(ctx, role, actorUserID, modelID, false, false)
	if err != nil {
		return result, err
	}
	committed := false
	defer finishTx(tx, &committed)
	allTargets := []validatedTarget{}
	for _, selection := range selections {
		allTargets = append(allTargets, selection.targets...)
	}
	if len(allTargets) > maxBindingBatch {
		return result, ErrResourceLimit
	}
	for _, selection := range allTargets {
		var donationID int64
		if err := tx.QueryRowContext(ctx, `SELECT donation_id FROM donation_keys WHERE id=?`, selection.keyID).Scan(&donationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return result, ErrNotFound
			}
			return result, err
		}
		if err := scope.RequireKey(ctx, tx, donationID, selection.keyID, now, true); err != nil {
			return result, managementScopeError(err)
		}
	}
	decision, err := beginMutation(ctx, tx, roleKind(scope.AuditRole(role == roleAdmin)), scope.ActorID, mutation, now)
	if err != nil {
		return result, err
	}
	if decision.Kind == idempotency.Replay {
		if compact {
			result.receipt, err = replay[BindingBatchReceipt](decision)
		} else {
			result.full, err = replayScopedBindings(ctx, tx, decision, scope, now)
		}
		return result, err
	}
	current, count, err := readBindingHeadTx(ctx, tx, modelID)
	if err != nil {
		return result, err
	}
	if current != expected {
		return result, ErrConflict
	}
	if count+len(selections) > maxBindingBatch {
		return result, ErrResourceLimit
	}
	var targetCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM charity_binding_targets t JOIN charity_model_bindings b ON b.id=t.binding_id WHERE b.charity_model_id=?`, modelID).Scan(&targetCount); err != nil {
		return result, err
	}
	if targetCount+len(allTargets) > maxBindingBatch {
		return result, ErrResourceLimit
	}
	if err := validateTargetsTx(ctx, tx, scope, modelID, allTargets, now); err != nil {
		return result, err
	}
	if err := rejectDuplicateTargetsTx(ctx, tx, modelID, 0, allTargets); err != nil {
		return result, err
	}
	start := 0
	for index, selection := range selections {
		end := start + len(selection.targets)
		if _, err := insertBindingGroupTx(ctx, tx, modelID, count+index, allTargets[start:end], now); err != nil {
			return result, classifyWrite("add charity binding", err)
		}
		start = end
	}
	if err := advanceBindingRevisionTx(ctx, tx, modelID, expected, now); err != nil {
		return result, err
	}
	if compact {
		value := BindingBatchReceipt{ModelID: strconv.FormatInt(modelID, 10), BindingRevision: strconv.FormatInt(expected+1, 10), AddedCount: strconv.Itoa(len(selections)), BindingCount: strconv.Itoa(count + len(selections))}
		result.receipt, err = finishJSON(ctx, tx, decision, http.StatusCreated, value)
	} else {
		var value AdminBindings
		value, err = readAdminBindingsTx(ctx, tx, modelID)
		if err == nil {
			value, err = scopedBindingsTx(ctx, tx, scope, value, now)
		}
		if err == nil {
			result.full, err = finishJSON(ctx, tx, decision, http.StatusCreated, value)
		}
	}
	if err != nil {
		return result, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return bindingBatchMutation{}, err
	}
	return result, nil
}

func (s *Service) OrderBindingsAdmin(ctx context.Context, modelID int64, mutation resources.ControlMutation, input BindingOrder) (resources.MutationResult[AdminBindings], error) {
	if !validMutation(mutation, http.MethodPut, routeAdminBindingOrder, modelID) {
		return resources.MutationResult[AdminBindings]{}, ErrInvalidRequest
	}
	return s.orderBindings(ctx, roleAdmin, 0, modelID, mutation, input)
}

func (s *Service) OrderBindingsSteward(ctx context.Context, actorUserID, modelID int64, mutation resources.ControlMutation, input BindingOrder) (resources.MutationResult[StewardBindings], error) {
	if actorUserID <= 0 || !validMutation(mutation, http.MethodPut, routeStewardBindingOrder, modelID) {
		return resources.MutationResult[StewardBindings]{}, ErrInvalidRequest
	}
	admin, err := s.orderBindings(ctx, roleSteward, actorUserID, modelID, mutation, input)
	if err != nil {
		return resources.MutationResult[StewardBindings]{}, err
	}
	value := stewardBindings(admin.Value)
	if input.Response == "receipt" {
		return resources.MutationResult[StewardBindings]{Value: value, Status: admin.Status, Body: admin.Body, Replayed: admin.Replayed}, nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return resources.MutationResult[StewardBindings]{}, ErrUnavailable
	}
	return resources.MutationResult[StewardBindings]{Value: value, Status: admin.Status, Body: body, Replayed: admin.Replayed}, nil
}

func (s *Service) orderBindings(ctx context.Context, role roleKind, actorUserID, modelID int64, mutation resources.ControlMutation, input BindingOrder) (resources.MutationResult[AdminBindings], error) {
	expected, err := parseNonnegativeRevision(input.ExpectedBindingRevision)
	order, orderErr := parseUniqueStringIDs(input.Order, true)
	if s == nil || ctx == nil || modelID <= 0 || err != nil || orderErr != nil || input.Response != "" && input.Response != "receipt" {
		return resources.MutationResult[AdminBindings]{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	tx, scope, err := s.beginManagementTx(ctx, role, actorUserID, modelID, false, false)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	if err := requireBindingSourceScope(ctx, tx, scope, modelID, 0, now); err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	decision, err := beginMutation(ctx, tx, roleKind(scope.AuditRole(role == roleAdmin)), scope.ActorID, mutation, now)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayBindingEdit(ctx, tx, decision, scope, now, input.Response)
	}
	current, count, err := readBindingHeadTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if current != expected || count != len(order) {
		return resources.MutationResult[AdminBindings]{}, ErrConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM charity_model_bindings WHERE charity_model_id=? ORDER BY id`, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, fmt.Errorf("charity routing: read binding order set: %w", err)
	}
	currentIDs, err := scanIDs(rows)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	want := append([]int64(nil), order...)
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if !equalIDs(currentIDs, want) {
		return resources.MutationResult[AdminBindings]{}, ErrConflict
	}
	if err := rewriteBindingOrderTx(ctx, tx, modelID, order, now); err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if err := advanceBindingRevisionTx(ctx, tx, modelID, expected, now); err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	out, err := finishBindingEdit(ctx, tx, decision, modelID, expected+1, input.Response, scope, now)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	return out, nil
}

func (s *Service) DeleteBindingAdmin(ctx context.Context, modelID, bindingID int64, mutation resources.ControlMutation, input BindingDelete) (resources.MutationResult[AdminBindings], error) {
	if !validMutation(mutation, http.MethodDelete, routeAdminBinding, modelID, bindingID) {
		return resources.MutationResult[AdminBindings]{}, ErrInvalidRequest
	}
	return s.deleteBinding(ctx, roleAdmin, 0, modelID, bindingID, mutation, input)
}

func (s *Service) DeleteBindingSteward(ctx context.Context, actorUserID, modelID, bindingID int64, mutation resources.ControlMutation, input BindingDelete) (resources.MutationResult[StewardBindings], error) {
	if actorUserID <= 0 || !validMutation(mutation, http.MethodDelete, routeStewardBinding, modelID, bindingID) {
		return resources.MutationResult[StewardBindings]{}, ErrInvalidRequest
	}
	admin, err := s.deleteBinding(ctx, roleSteward, actorUserID, modelID, bindingID, mutation, input)
	if err != nil {
		return resources.MutationResult[StewardBindings]{}, err
	}
	value := stewardBindings(admin.Value)
	if input.Response == "receipt" {
		return resources.MutationResult[StewardBindings]{Value: value, Status: admin.Status, Body: admin.Body, Replayed: admin.Replayed}, nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return resources.MutationResult[StewardBindings]{}, ErrUnavailable
	}
	return resources.MutationResult[StewardBindings]{Value: value, Status: admin.Status, Body: body, Replayed: admin.Replayed}, nil
}

func (s *Service) deleteBinding(ctx context.Context, role roleKind, actorUserID, modelID, bindingID int64, mutation resources.ControlMutation, input BindingDelete) (resources.MutationResult[AdminBindings], error) {
	expected, err := parseNonnegativeRevision(input.ExpectedBindingRevision)
	if s == nil || ctx == nil || modelID <= 0 || bindingID <= 0 || err != nil || input.Response != "" && input.Response != "receipt" {
		return resources.MutationResult[AdminBindings]{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	tx, scope, err := s.beginManagementTx(ctx, role, actorUserID, modelID, false, false)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	if err := requireBindingSourceScope(ctx, tx, scope, modelID, bindingID, now); err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	decision, err := beginMutation(ctx, tx, roleKind(scope.AuditRole(role == roleAdmin)), scope.ActorID, mutation, now)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayDeletedBinding(ctx, tx, decision, scope, now, input.Response)
	}
	current, _, err := readBindingHeadTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if current != expected {
		return resources.MutationResult[AdminBindings]{}, ErrConflict
	}
	sourceKeys, err := bindingReplaySourcesTx(ctx, tx, modelID, bindingID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM charity_model_bindings WHERE id=? AND charity_model_id=?`, bindingID, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, fmt.Errorf("charity routing: delete binding: %w", err)
	}
	if err := requireOne(result); err != nil {
		return resources.MutationResult[AdminBindings]{}, ErrNotFound
	}
	// Compact surviving bindings in place so their adaptation rows survive.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM charity_model_bindings WHERE charity_model_id=? ORDER BY ord,id`, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, fmt.Errorf("charity routing: read compacted bindings: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if err := rewriteBindingOrderTx(ctx, tx, modelID, ids, now); err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if err := advanceBindingRevisionTx(ctx, tx, modelID, expected, now); err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	out, err := finishBindingEdit(ctx, tx, decision, modelID, expected+1, input.Response, scope, now, sourceKeys)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	return out, nil
}

func readBindingHeadTx(ctx context.Context, tx *sql.Tx, modelID int64) (int64, int, error) {
	var revision int64
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT binding_revision,
(SELECT COUNT(*) FROM charity_model_bindings b WHERE b.charity_model_id=charity_models.id)
FROM charity_models WHERE id=?`, modelID).Scan(&revision, &count); errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrNotFound
	} else if err != nil {
		return 0, 0, fmt.Errorf("charity routing: read binding head: %w", err)
	}
	return revision, count, nil
}

func advanceBindingRevisionTx(ctx context.Context, tx *sql.Tx, modelID, expected, now int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE charity_models SET binding_revision=binding_revision+1,updated_at=?
WHERE id=? AND binding_revision=? AND binding_revision<9223372036854775807`, now, modelID, expected)
	if err != nil {
		return fmt.Errorf("charity routing: advance binding revision: %w", err)
	}
	return requireOne(result)
}

type bindingRecord struct {
	id int64
}

func rewriteBindingOrderTx(ctx context.Context, tx *sql.Tx, modelID int64, order []int64, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id
FROM charity_model_bindings WHERE charity_model_id=? ORDER BY id`, modelID)
	if err != nil {
		return fmt.Errorf("charity routing: read bindings for reorder: %w", err)
	}
	records := make(map[int64]bindingRecord, len(order))
	for rows.Next() {
		var record bindingRecord
		if err := rows.Scan(&record.id); err != nil {
			rows.Close()
			return fmt.Errorf("charity routing: scan binding for reorder: %w", err)
		}
		records[record.id] = record
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("charity routing: close bindings for reorder: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("charity routing: iterate bindings for reorder: %w", err)
	}
	if len(records) != len(order) {
		return ErrConflict
	}
	for _, id := range order {
		if _, exists := records[id]; !exists {
			return ErrConflict
		}
	}
	return db.ReorderBindingsTx(ctx, tx, true, modelID, order, now)
}

func readAdminBindingsDB(ctx context.Context, database *sql.DB, modelID int64) (AdminBindings, error) {
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AdminBindings{}, fmt.Errorf("charity routing: begin bindings read: %w", err)
	}
	defer tx.Rollback()
	value, err := readAdminBindingsTx(ctx, tx, modelID)
	if err != nil {
		return AdminBindings{}, err
	}
	return value, nil
}

func readAdminBindingsTx(ctx context.Context, tx *sql.Tx, modelID int64) (AdminBindings, error) {
	var out AdminBindings
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT binding_revision FROM charity_models WHERE id=?`, modelID).Scan(&revision); errors.Is(err, sql.ErrNoRows) {
		return AdminBindings{}, ErrNotFound
	} else if err != nil {
		return AdminBindings{}, fmt.Errorf("charity routing: read bindings revision: %w", err)
	}
	out.BindingRevision = strconv.FormatInt(revision, 10)
	rows, err := tx.QueryContext(ctx, adminBindingsQuery(), modelID, maxBindingBatch+1)
	if err != nil {
		return AdminBindings{}, fmt.Errorf("charity routing: read bindings: %w", err)
	}
	defer rows.Close()
	out.Bindings = make([]AdminBinding, 0)
	type targetRow struct {
		group       int
		keyID, mask int64
		value       BindingTarget
	}
	var targets []targetRow
	keyIDs := []int64{}
	seen := map[int64]int{}
	for rows.Next() {
		var item AdminBinding
		var id, donationKeyID, donationID int64
		var automatic, manual int64
		var targetID, targetRevision, mask, endpointID int64
		var policy sql.NullString
		if err := rows.Scan(&id, &item.Ord, &donationKeyID, &donationID, &item.Source.ConnectorType,
			&item.Source.CanonicalBaseURL, &item.Source.DisplayHead, &item.Source.DisplayTail,
			&item.UpstreamModelID, &automatic, &manual, &item.Source.MaxConcurrency, &item.Source.MaxRPM, &policy, &targetID, &targetRevision, &mask, &endpointID); err != nil {
			return AdminBindings{}, fmt.Errorf("charity routing: scan binding: %w", err)
		}
		if policy.Valid {
			value, err := gatewaypolicy.DecodePolicy([]byte(policy.String))
			if err != nil {
				return AdminBindings{}, ErrInvariant
			}
			item.GatewayCapabilities = &value
		}
		item.ID = strconv.FormatInt(id, 10)
		item.DonationKeyID = strconv.FormatInt(donationKeyID, 10)
		item.DonationID = strconv.FormatInt(donationID, 10)
		item.SourceTypes = sourceTypes(automatic, manual)
		group, exists := seen[id]
		if !exists {
			group = len(out.Bindings)
			seen[id] = group
			item.Targets = []BindingTarget{}
			out.Bindings = append(out.Bindings, item)
		}
		targets = append(targets, targetRow{group: group, keyID: donationKeyID, mask: mask, value: BindingTarget{ID: strconv.FormatInt(targetID, 10), Revision: strconv.FormatInt(targetRevision, 10), DonationID: item.DonationID, DonationKeyID: item.DonationKeyID, UpstreamModelID: item.UpstreamModelID, EndpointID: strconv.FormatInt(endpointID, 10), ConnectorType: contract.Type(item.Source.ConnectorType), ConfigurationType: contract.Type(item.Source.ConnectorType).ConfigurationType(), BaseURL: item.Source.CanonicalBaseURL}})
		keyIDs = append(keyIDs, donationKeyID)
	}
	if err := rows.Err(); err != nil {
		return AdminBindings{}, fmt.Errorf("charity routing: iterate bindings: %w", err)
	}
	if err := rows.Close(); err != nil {
		return AdminBindings{}, err
	}
	if len(targets) > maxBindingBatch {
		return AdminBindings{}, ErrInvariant
	}
	uses, err := connectionstore.LoadApprovedUsesTx(ctx, tx, keyIDs)
	if err != nil {
		return AdminBindings{}, targetConnectionError(err)
	}
	for _, row := range targets {
		selected, err := contract.NativeServicesFromMask(row.mask)
		if err != nil || selected.Empty() {
			return AdminBindings{}, ErrInvariant
		}
		row.value.ServiceProjection, row.value.Uses, err = readTargetProjection(uses[row.keyID], selected)
		if err != nil {
			return AdminBindings{}, err
		}
		out.Bindings[row.group].Targets = append(out.Bindings[row.group].Targets, row.value)
	}
	return out, nil
}

type validatedSelection struct {
	donationKeyID   int64
	upstreamModelID string
	targets         []validatedTarget
}

func validateSelections(input []BindingSelection) ([]validatedSelection, error) {
	if len(input) < 1 || len(input) > maxBindingBatch {
		return nil, ErrInvalidRequest
	}
	seen := make(map[string]struct{}, len(input))
	out := make([]validatedSelection, 0, len(input))
	for _, value := range input {
		if value.Targets != nil {
			if value.DonationKeyID != "" || value.UpstreamModelID != "" {
				return nil, ErrInvalidRequest
			}
			targets, err := targetInputs(value.Targets, false)
			if err != nil {
				return nil, err
			}
			for _, target := range targets {
				key := strconv.FormatInt(target.keyID, 10) + "\x00" + target.upstream
				if _, exists := seen[key]; exists {
					return nil, ErrInvalidRequest
				}
				seen[key] = struct{}{}
			}
			out = append(out, validatedSelection{targets: targets})
			continue
		}
		id, err := parsePositiveID(value.DonationKeyID)
		if err != nil || !validUpstreamModel(value.UpstreamModelID) {
			return nil, ErrInvalidRequest
		}
		key := value.DonationKeyID + "\x00" + value.UpstreamModelID
		if _, exists := seen[key]; exists {
			return nil, ErrInvalidRequest
		}
		seen[key] = struct{}{}
		out = append(out, validatedSelection{donationKeyID: id, upstreamModelID: value.UpstreamModelID, targets: []validatedTarget{{keyID: id, upstream: value.UpstreamModelID, legacy: true}}})
	}
	return out, nil
}

func validUpstreamModel(value string) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 512 {
		return false
	}
	return strings.TrimSpace(value) == value
}

func parseNonnegativeRevision(value string) (int64, error) {
	if value == "" || len(value) > 19 || len(value) > 1 && value[0] == '0' || !digits(value) {
		return 0, ErrInvalidRequest
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, ErrInvalidRequest
	}
	return parsed, nil
}

func parseUniqueStringIDs(values []string, allowEmpty bool) ([]int64, error) {
	if len(values) == 0 && !allowEmpty || len(values) > maxBindingBatch {
		return nil, ErrInvalidRequest
	}
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, len(values))
	for index, value := range values {
		id, err := parsePositiveID(value)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[id]; exists {
			return nil, ErrInvalidRequest
		}
		seen[id] = struct{}{}
		out[index] = id
	}
	return out, nil
}

func sourceTypes(automatic, manual int64) []string {
	out := make([]string, 0, 2)
	if automatic > 0 {
		out = append(out, "automatic")
	}
	if manual > 0 {
		out = append(out, "manual")
	}
	return out
}

func stewardCandidate(value AdminBindingCandidate) StewardBindingCandidate {
	return StewardBindingCandidate{
		ServiceProjection: value.ServiceProjection,
		BindingMarkers:    value.BindingMarkers,
		DonationKeyID:     value.DonationKeyID, DonationID: value.DonationID,
		Source: StewardCandidateSource{
			ConnectorType: value.Source.ConnectorType, CanonicalBaseURL: value.Source.CanonicalBaseURL,
			DisplayHead: value.Source.DisplayHead, DisplayTail: value.Source.DisplayTail,
			MaxConcurrency: value.Source.MaxConcurrency, MaxRPM: value.Source.MaxRPM,
		},
		UpstreamModelID: value.UpstreamModelID, SourceTypes: append([]string(nil), value.SourceTypes...),
	}
}

func stewardBindings(value AdminBindings) StewardBindings {
	out := StewardBindings{BindingRevision: value.BindingRevision, Bindings: make([]StewardBinding, len(value.Bindings))}
	for index, binding := range value.Bindings {
		out.Bindings[index] = StewardBinding{
			Targets:             binding.Targets,
			GatewayCapabilities: binding.GatewayCapabilities,
			ID:                  binding.ID, Ord: binding.Ord, DonationKeyID: binding.DonationKeyID, DonationID: binding.DonationID,
			Source: StewardCandidateSource{
				ConnectorType: binding.Source.ConnectorType, CanonicalBaseURL: binding.Source.CanonicalBaseURL,
				DisplayHead: binding.Source.DisplayHead, DisplayTail: binding.Source.DisplayTail,
				MaxConcurrency: binding.Source.MaxConcurrency, MaxRPM: binding.Source.MaxRPM,
			},
			UpstreamModelID: binding.UpstreamModelID, SourceTypes: append([]string{}, binding.SourceTypes...),
		}
	}
	return out
}

func equalIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
