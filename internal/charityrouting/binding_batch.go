package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	routeAdminKeyBindingBatch      = "/admin/api/donations/{id}/keys/{keyId}/bindings/batch"
	routeStewardKeyBindingBatch    = "/api/steward/donations/{id}/keys/{keyId}/bindings/batch"
	routeAdminApplyBindingPreset   = "/admin/api/donations/{id}/bindings/apply-preset"
	routeStewardApplyBindingPreset = "/api/steward/donations/{id}/bindings/apply-preset"
	maxKeyBindingTargets           = maxBindingBatch
	maxChannelBindingMappings      = 16
)

type KeyBindingTarget struct {
	CharityModelID          string `json:"charity_model_id"`
	ExpectedBindingRevision string `json:"expected_binding_revision"`
	UpstreamModelID         string `json:"upstream_model_id"`
}
type BindingModelRevision struct {
	CharityModelID  string `json:"charity_model_id"`
	BindingRevision string `json:"binding_revision"`
}
type BindingMatrixRow struct {
	DonationKeyID string   `json:"donation_key_id"`
	Results       []string `json:"results"`
}

// Results use the input target order, and preset results use saved mapping order.
type BindingMatrixReceipt struct {
	Items      []BindingMatrixRow     `json:"items"`
	Models     []BindingModelRevision `json:"models"`
	AddedCount string                 `json:"added_count"`
}
type KeyBindingBatch struct {
	ExpectedDonationRevision string             `json:"expected_donation_revision"`
	Targets                  []KeyBindingTarget `json:"targets"`
}
type PresetBindingRevision struct {
	CharityModelID          string `json:"charity_model_id"`
	ExpectedBindingRevision string `json:"expected_binding_revision"`
}
type ApplyBindingPreset struct {
	ChannelID                string                  `json:"channel_id"`
	ExpectedChannelRevision  string                  `json:"expected_channel_revision"`
	ExpectedPresetRevision   string                  `json:"expected_preset_revision"`
	ExpectedDonationRevision string                  `json:"expected_donation_revision"`
	Models                   []PresetBindingRevision `json:"models"`
}
type bindingTarget struct {
	modelID  int64
	upstream string
	expected int64
}
type bindingKey struct {
	id, physicalID int64
	valid          bool
}
type bindingHead struct {
	revision    int64
	count       int
	targetCount int
	added       bool
}

func parseBindingTargets(input []KeyBindingTarget) ([]bindingTarget, error) {
	if len(input) < 1 || len(input) > maxKeyBindingTargets {
		return nil, ErrInvalidRequest
	}
	out := make([]bindingTarget, 0, len(input))
	seen := map[string]bool{}
	revisions := map[int64]int64{}
	for _, item := range input {
		id, err := parsePositiveID(item.CharityModelID)
		if err != nil || !validManualName(item.UpstreamModelID) {
			return nil, ErrInvalidRequest
		}
		expected, err := parseNonnegativeRevision(item.ExpectedBindingRevision)
		if err != nil {
			return nil, err
		}
		pair := item.CharityModelID + "\x00" + item.UpstreamModelID
		if seen[pair] {
			return nil, ErrInvalidRequest
		}
		seen[pair] = true
		if old, ok := revisions[id]; ok && old != expected {
			return nil, ErrInvalidRequest
		}
		revisions[id] = expected
		out = append(out, bindingTarget{id, item.UpstreamModelID, expected})
	}
	return out, nil
}

func donationBindingRevision(ctx context.Context, tx *sql.Tx, id int64) (int64, error) {
	var revision int64
	var status string
	var owner sql.NullInt64
	var terminal sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT revision,status,user_id,terminal_at FROM donations WHERE id=?`, id).Scan(&revision, &status, &owner, &terminal)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if status != "approved" && status != "pending" || terminal.Valid || !owner.Valid {
		return 0, ErrConflict
	}
	return revision, nil
}
func readBindingKeys(ctx context.Context, tx *sql.Tx, donationID, keyID, now int64, channel *BindingPresetChannel) ([]bindingKey, error) {
	rows, err := tx.QueryContext(ctx, `SELECT dk.id,COALESCE(dk.endpoint_key_id,0),
 dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?) AND EXISTS(
 SELECT 1 FROM donation_key_memberships m JOIN endpoint_keys k ON k.id=m.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id JOIN donations d ON d.id=m.donation_id
 WHERE m.donation_key_id=dk.id AND m.donation_id=dk.donation_id AND m.endpoint_key_id=dk.endpoint_key_id AND e.user_id=d.user_id)
 AND EXISTS(SELECT 1 FROM charity_use_availability av WHERE av.donation_key_id=dk.id),
 COALESCE(dk.mainstream_channel_id,''),dk.connector_type,dk.canonical_base_url
 FROM donation_keys dk WHERE dk.donation_id=? AND (?=0 OR dk.id=?) ORDER BY dk.id`, now, donationID, keyID, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []bindingKey{}
	for rows.Next() {
		var key bindingKey
		var source, connector, base string
		if err := rows.Scan(&key.id, &key.physicalID, &key.valid, &source, &connector, &base); err != nil {
			return nil, err
		}
		if channel != nil && (source != channel.ChannelID || connector != channel.ConnectorType || base != channel.BaseURL) {
			key.valid = false
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	if len(out) > 100 {
		return nil, ErrInvariant
	}
	return out, nil
}

func (s *Service) bindKeyTargets(ctx context.Context, role roleKind, actorID, donationID, keyID int64, mutation resources.ControlMutation, input KeyBindingBatch) (resources.MutationResult[BindingMatrixReceipt], error) {
	empty := resources.MutationResult[BindingMatrixReceipt]{}
	route := routeAdminKeyBindingBatch
	if role == roleSteward {
		route = routeStewardKeyBindingBatch
	}
	expected, err := parsePositiveID(input.ExpectedDonationRevision)
	targets, parseErr := parseBindingTargets(input.Targets)
	if ctx == nil || err != nil || parseErr != nil || !validMutation(mutation, http.MethodPost, route, donationID, keyID) {
		return empty, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	tx, scope, err := s.beginManagementTx(ctx, role, actorID, 0, true, false)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	// Check every requested permission before reading receipts or skipping missing targets.
	if role != roleAdmin {
		if err := authz.RequireStewardPermission(ctx, tx, actorID, "charity.manage"); err != nil {
			return empty, err
		}
	}
	for _, target := range targets {
		if scope.Trainee {
			targetScope, err := s.managementScope(ctx, tx, role, actorID, target.modelID, false)
			if err != nil {
				return empty, err
			}
			if err := targetScope.RequireKey(ctx, tx, donationID, keyID, now, true); err != nil {
				return empty, managementScopeError(err)
			}
		}
	}
	revision, err := donationBindingRevision(ctx, tx, donationID)
	if err != nil {
		return empty, err
	}
	keys, err := readBindingKeys(ctx, tx, donationID, keyID, now, nil)
	if err != nil {
		return empty, err
	}
	if !keys[0].valid {
		return empty, ErrConflict
	}
	decision, err := beginMutation(ctx, tx, roleKind(scope.AuditRole(role == roleAdmin)), scope.ActorID, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[BindingMatrixReceipt](decision)
	}
	if revision != expected {
		return empty, ErrConflict
	}
	value, err := applyBindingMatrix(ctx, tx, targets, keys, now)
	if err != nil {
		return empty, err
	}
	result, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func applyBindingMatrix(ctx context.Context, tx *sql.Tx, targets []bindingTarget, keys []bindingKey, now int64) (BindingMatrixReceipt, error) {
	out := BindingMatrixReceipt{Items: []BindingMatrixRow{}, Models: []BindingModelRevision{}, AddedCount: "0"}
	heads := map[int64]*bindingHead{}
	order := []int64{}
	for _, target := range targets {
		if _, ok := heads[target.modelID]; ok {
			continue
		}
		var head bindingHead
		err := tx.QueryRowContext(ctx, `SELECT binding_revision,(SELECT COUNT(*) FROM charity_model_bindings WHERE charity_model_id=cm.id),(SELECT COUNT(*) FROM charity_binding_targets t JOIN charity_model_bindings b ON b.id=t.binding_id WHERE b.charity_model_id=cm.id) FROM charity_models cm WHERE id=?`, target.modelID).Scan(&head.revision, &head.count, &head.targetCount)
		if errors.Is(err, sql.ErrNoRows) {
			heads[target.modelID] = nil
			continue
		}
		if err != nil {
			return out, err
		}
		if head.revision != target.expected {
			return out, ErrConflict
		}
		heads[target.modelID] = &head
		order = append(order, target.modelID)
	}
	added := 0
	for _, key := range keys {
		row := BindingMatrixRow{DonationKeyID: strconv.FormatInt(key.id, 10), Results: make([]string, len(targets))}
		for index, target := range targets {
			status := "invalid_resource"
			head := heads[target.modelID]
			if key.valid && head != nil {
				var exists bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_model_bindings b JOIN charity_binding_targets t ON t.binding_id=b.id WHERE b.charity_model_id=? AND t.donation_key_id=? AND t.upstream_model_id=?)`, target.modelID, key.id, target.upstream).Scan(&exists); err != nil {
					return out, err
				}
				switch {
				case exists:
					status = "already_bound"
				default:
					var supported bool
					if err := tx.QueryRowContext(ctx, `SELECT `+charityscope.SupportSQL("?", "?", "?"), key.id, target.upstream).Scan(&supported); err != nil {
						return out, err
					}
					if !supported {
						status = "missing_model"
					} else if head.targetCount >= maxBindingBatch {
						status = "capacity_exceeded"
					} else {
						values := []validatedTarget{{keyID: key.id, upstream: target.upstream, legacy: true}}
						if err := validateTargetsTx(ctx, tx, charityscope.Scope{}, target.modelID, values, now); err != nil {
							if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidRequest) {
								row.Results[index] = "invalid_resource"
								continue
							}
							return out, err
						}
						if _, err := insertBindingGroupTx(ctx, tx, target.modelID, head.count, values, now); err != nil {
							return out, err
						}
						head.count++
						head.targetCount++
						head.added = true
						added++
						status = "added"
					}
				}
			}
			row.Results[index] = status
		}
		out.Items = append(out.Items, row)
	}
	for _, id := range order {
		head := heads[id]
		if head.added {
			if err := advanceBindingRevisionTx(ctx, tx, id, head.revision, now); err != nil {
				return out, err
			}
			head.revision++
		}
		out.Models = append(out.Models, BindingModelRevision{strconv.FormatInt(id, 10), strconv.FormatInt(head.revision, 10)})
	}
	out.AddedCount = strconv.Itoa(added)
	return out, nil
}
