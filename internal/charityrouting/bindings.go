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
		(query.Source != "" && query.Source != "automatic" && query.Source != "manual") ||
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
	if role == roleSteward {
		if nilDependency(s.roleAuth) {
			return nil, 0, "", ErrUnavailable
		}
		if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, actorUserID); err != nil {
			return nil, 0, "", mapAuthorization(err)
		}
	}
	if err := s.donationState.MaterializeDueExpiriesTx(ctx, tx, now, 100); err != nil {
		return nil, 0, "", fmt.Errorf("charity routing: materialize candidate expiry: %w", err)
	}
	statement := `SELECT dk.id,d.id,dk.connector_type,dk.canonical_base_url,dk.display_head,dk.display_tail,
pc.normalized_model_id,pc.automatic_supports,pc.manual_supports
FROM charity_models cm
JOIN donation_keys dk
JOIN donations d ON d.id=dk.donation_id
JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
JOIN endpoint_keys k ON k.id=m.endpoint_key_id
JOIN model_pair_catalog pc ON pc.endpoint_key_id=k.id
WHERE cm.id=? AND d.status='approved' AND (dk.expires_at IS NULL OR dk.expires_at>?)
AND dk.ended_at IS NULL AND (dk.id>? OR (dk.id=? AND pc.normalized_model_id>?))
AND NOT EXISTS(SELECT 1 FROM charity_model_bindings b WHERE b.charity_model_id=cm.id
 AND b.donation_key_id=dk.id AND b.upstream_model_id=pc.normalized_model_id)`
	args := []any{modelID, now, query.AfterKeyID, query.AfterKeyID, query.AfterModelID}
	if query.DonationID > 0 {
		statement += ` AND d.id=?`
		args = append(args, query.DonationID)
	}
	if query.DonationKeyID > 0 {
		statement += ` AND dk.id=?`
		args = append(args, query.DonationKeyID)
	}
	if query.Source == "automatic" {
		statement += ` AND pc.automatic_supports>0`
	} else if query.Source == "manual" {
		statement += ` AND pc.manual_supports>0`
	} else {
		statement += ` AND (pc.automatic_supports>0 OR pc.manual_supports>0)`
	}
	if query.Query != "" {
		statement += ` AND pc.normalized_model_id LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(query.Query)+"%")
	}
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
			&item.Source.DisplayHead, &item.Source.DisplayTail, &item.UpstreamModelID, &automatic, &manual); err != nil {
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
	return readAdminBindingsDB(ctx, s.db, modelID)
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
	if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, actorUserID); err != nil {
		return StewardBindings{}, mapAuthorization(err)
	}
	value, err := readAdminBindingsTx(ctx, tx, modelID)
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
	return s.addBindings(ctx, roleAdmin, 0, modelID, mutation, input)
}

func (s *Service) AddBindingsSteward(ctx context.Context, actorUserID, modelID int64, mutation resources.ControlMutation, input BindingBatch) (resources.MutationResult[StewardBindings], error) {
	if actorUserID <= 0 || !validMutation(mutation, http.MethodPost, routeStewardBindingBatch, modelID) {
		return resources.MutationResult[StewardBindings]{}, ErrInvalidRequest
	}
	admin, err := s.addBindings(ctx, roleSteward, actorUserID, modelID, mutation, input)
	if err != nil {
		return resources.MutationResult[StewardBindings]{}, err
	}
	value := stewardBindings(admin.Value)
	body, err := json.Marshal(value)
	if err != nil {
		return resources.MutationResult[StewardBindings]{}, ErrUnavailable
	}
	return resources.MutationResult[StewardBindings]{Value: value, Status: admin.Status, Body: body, Replayed: admin.Replayed}, nil
}

func (s *Service) addBindings(ctx context.Context, role roleKind, actorUserID, modelID int64, mutation resources.ControlMutation, input BindingBatch) (resources.MutationResult[AdminBindings], error) {
	expected, err := parseNonnegativeRevision(input.ExpectedBindingRevision)
	selections, validateErr := validateSelections(input.Selections)
	if s == nil || ctx == nil || modelID <= 0 || err != nil || validateErr != nil {
		return resources.MutationResult[AdminBindings]{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	tx, actorID, err := s.beginRoleTx(ctx, role, actorUserID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginMutation(ctx, tx, role, actorID, mutation, now)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[AdminBindings](decision)
	}
	current, count, err := readBindingHeadTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if current != expected {
		return resources.MutationResult[AdminBindings]{}, ErrConflict
	}
	if count+len(selections) > maxBindingBatch {
		return resources.MutationResult[AdminBindings]{}, ErrResourceLimit
	}
	for index, selection := range selections {
		var endpointKeyID int64
		err := tx.QueryRowContext(ctx, `SELECT dk.endpoint_key_id
FROM donation_keys dk
JOIN donations d ON d.id=dk.donation_id
JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
JOIN model_pair_catalog pc ON pc.endpoint_key_id=m.endpoint_key_id AND pc.normalized_model_id=?
WHERE dk.id=? AND d.status='approved' AND (dk.expires_at IS NULL OR dk.expires_at>?)
AND dk.ended_at IS NULL AND (pc.automatic_supports>0 OR pc.manual_supports>0)`,
			selection.upstreamModelID, selection.donationKeyID, now).Scan(&endpointKeyID)
		if errors.Is(err, sql.ErrNoRows) {
			return resources.MutationResult[AdminBindings]{}, ErrNotFound
		}
		if err != nil {
			return resources.MutationResult[AdminBindings]{}, fmt.Errorf("charity routing: validate binding candidate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO charity_model_bindings(
charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,?,?,?,?)`, modelID, selection.donationKeyID, endpointKeyID, selection.upstreamModelID,
			count+index, now, now); err != nil {
			return resources.MutationResult[AdminBindings]{}, classifyWrite("add charity binding", err)
		}
	}
	if err := advanceBindingRevisionTx(ctx, tx, modelID, expected, now); err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	value, err := readAdminBindingsTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusCreated, value)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	return out, nil
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
	body, err := json.Marshal(value)
	if err != nil {
		return resources.MutationResult[StewardBindings]{}, ErrUnavailable
	}
	return resources.MutationResult[StewardBindings]{Value: value, Status: admin.Status, Body: body, Replayed: admin.Replayed}, nil
}

func (s *Service) orderBindings(ctx context.Context, role roleKind, actorUserID, modelID int64, mutation resources.ControlMutation, input BindingOrder) (resources.MutationResult[AdminBindings], error) {
	expected, err := parseNonnegativeRevision(input.ExpectedBindingRevision)
	order, orderErr := parseUniqueStringIDs(input.Order, true)
	if s == nil || ctx == nil || modelID <= 0 || err != nil || orderErr != nil {
		return resources.MutationResult[AdminBindings]{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	tx, actorID, err := s.beginRoleTx(ctx, role, actorUserID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginMutation(ctx, tx, role, actorID, mutation, now)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[AdminBindings](decision)
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
	value, err := readAdminBindingsTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
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
	body, err := json.Marshal(value)
	if err != nil {
		return resources.MutationResult[StewardBindings]{}, ErrUnavailable
	}
	return resources.MutationResult[StewardBindings]{Value: value, Status: admin.Status, Body: body, Replayed: admin.Replayed}, nil
}

func (s *Service) deleteBinding(ctx context.Context, role roleKind, actorUserID, modelID, bindingID int64, mutation resources.ControlMutation, input BindingDelete) (resources.MutationResult[AdminBindings], error) {
	expected, err := parseNonnegativeRevision(input.ExpectedBindingRevision)
	if s == nil || ctx == nil || modelID <= 0 || bindingID <= 0 || err != nil {
		return resources.MutationResult[AdminBindings]{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	tx, actorID, err := s.beginRoleTx(ctx, role, actorUserID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginMutation(ctx, tx, role, actorID, mutation, now)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[AdminBindings](decision)
	}
	current, _, err := readBindingHeadTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	if current != expected {
		return resources.MutationResult[AdminBindings]{}, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM charity_model_bindings WHERE id=? AND charity_model_id=?`, bindingID, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, fmt.Errorf("charity routing: delete binding: %w", err)
	}
	if err := requireOne(result); err != nil {
		return resources.MutationResult[AdminBindings]{}, ErrNotFound
	}
	// Compact ord by rebuilding the bounded binding rows with their stable IDs;
	// the frozen ord domain has no out-of-band staging value.
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
	value, err := readAdminBindingsTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
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
	id, donationKeyID, endpointKeyID int64
	upstreamModelID                  string
	createdAt                        int64
}

func rewriteBindingOrderTx(ctx context.Context, tx *sql.Tx, modelID int64, order []int64, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,donation_key_id,endpoint_key_id,upstream_model_id,created_at
FROM charity_model_bindings WHERE charity_model_id=? ORDER BY id`, modelID)
	if err != nil {
		return fmt.Errorf("charity routing: read bindings for reorder: %w", err)
	}
	records := make(map[int64]bindingRecord, len(order))
	for rows.Next() {
		var record bindingRecord
		if err := rows.Scan(&record.id, &record.donationKeyID, &record.endpointKeyID, &record.upstreamModelID, &record.createdAt); err != nil {
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM charity_model_bindings WHERE charity_model_id=?`, modelID); err != nil {
		return fmt.Errorf("charity routing: clear bindings for reorder: %w", err)
	}
	for ord, id := range order {
		record := records[id]
		if _, err := tx.ExecContext(ctx, `INSERT INTO charity_model_bindings(
id,charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?)`, record.id, modelID, record.donationKeyID, record.endpointKeyID,
			record.upstreamModelID, ord, record.createdAt, now); err != nil {
			return fmt.Errorf("charity routing: rebuild binding order: %w", err)
		}
	}
	return nil
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
	rows, err := tx.QueryContext(ctx, `SELECT b.id,b.ord,dk.id,d.id,dk.connector_type,dk.canonical_base_url,
dk.display_head,dk.display_tail,b.upstream_model_id,pc.automatic_supports,pc.manual_supports
FROM charity_model_bindings b
JOIN donation_keys dk ON dk.id=b.donation_key_id
JOIN donations d ON d.id=dk.donation_id
JOIN model_pair_catalog pc ON pc.endpoint_key_id=b.endpoint_key_id AND pc.normalized_model_id=b.upstream_model_id
WHERE b.charity_model_id=? ORDER BY b.ord,b.id`, modelID)
	if err != nil {
		return AdminBindings{}, fmt.Errorf("charity routing: read bindings: %w", err)
	}
	defer rows.Close()
	out.Bindings = make([]AdminBinding, 0)
	for rows.Next() {
		var item AdminBinding
		var id, donationKeyID, donationID int64
		var automatic, manual int64
		if err := rows.Scan(&id, &item.Ord, &donationKeyID, &donationID, &item.Source.ConnectorType,
			&item.Source.CanonicalBaseURL, &item.Source.DisplayHead, &item.Source.DisplayTail,
			&item.UpstreamModelID, &automatic, &manual); err != nil {
			return AdminBindings{}, fmt.Errorf("charity routing: scan binding: %w", err)
		}
		item.ID = strconv.FormatInt(id, 10)
		item.DonationKeyID = strconv.FormatInt(donationKeyID, 10)
		item.DonationID = strconv.FormatInt(donationID, 10)
		item.SourceTypes = sourceTypes(automatic, manual)
		out.Bindings = append(out.Bindings, item)
	}
	if err := rows.Err(); err != nil {
		return AdminBindings{}, fmt.Errorf("charity routing: iterate bindings: %w", err)
	}
	return out, nil
}

type validatedSelection struct {
	donationKeyID   int64
	upstreamModelID string
}

func validateSelections(input []BindingSelection) ([]validatedSelection, error) {
	if len(input) < 1 || len(input) > maxBindingBatch {
		return nil, ErrInvalidRequest
	}
	seen := make(map[string]struct{}, len(input))
	out := make([]validatedSelection, 0, len(input))
	for _, value := range input {
		id, err := parsePositiveID(value.DonationKeyID)
		if err != nil || !validUpstreamModel(value.UpstreamModelID) {
			return nil, ErrInvalidRequest
		}
		key := value.DonationKeyID + "\x00" + value.UpstreamModelID
		if _, exists := seen[key]; exists {
			return nil, ErrInvalidRequest
		}
		seen[key] = struct{}{}
		out = append(out, validatedSelection{donationKeyID: id, upstreamModelID: value.UpstreamModelID})
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
		DonationKeyID: value.DonationKeyID, DonationID: value.DonationID,
		Source: StewardCandidateSource{
			ConnectorType: value.Source.ConnectorType, CanonicalBaseURL: value.Source.CanonicalBaseURL,
			DisplayHead: value.Source.DisplayHead, DisplayTail: value.Source.DisplayTail,
		},
		UpstreamModelID: value.UpstreamModelID, SourceTypes: append([]string(nil), value.SourceTypes...),
	}
}

func stewardBindings(value AdminBindings) StewardBindings {
	out := StewardBindings{BindingRevision: value.BindingRevision, Bindings: make([]StewardBinding, len(value.Bindings))}
	for index, binding := range value.Bindings {
		out.Bindings[index] = StewardBinding{
			ID: binding.ID, Ord: binding.Ord, DonationKeyID: binding.DonationKeyID, DonationID: binding.DonationID,
			Source: StewardCandidateSource{
				ConnectorType: binding.Source.ConnectorType, CanonicalBaseURL: binding.Source.CanonicalBaseURL,
				DisplayHead: binding.Source.DisplayHead, DisplayTail: binding.Source.DisplayTail,
			},
			UpstreamModelID: binding.UpstreamModelID, SourceTypes: append([]string(nil), binding.SourceTypes...),
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
