package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

type BindingTargetInput struct {
	DonationKeyID   string                   `json:"donation_key_id"`
	UpstreamModelID string                   `json:"upstream_model_id"`
	NativeServices  []contract.NativeService `json:"native_services"`
}

type BindingTargetUse struct {
	UseID             string                 `json:"use_id"`
	NativeService     contract.NativeService `json:"native_service"`
	CredentialID      string                 `json:"credential_id"`
	CredentialVersion string                 `json:"credential_version"`
	GrantRevision     string                 `json:"grant_revision"`
	Revision          string                 `json:"revision"`
}

type ServiceRouteProjection struct {
	Operation     contract.Operation     `json:"operation"`
	NativeService contract.NativeService `json:"native_service"`
	Mode          string                 `json:"mode"`
}
type BindingCapabilities struct {
	CanBind           bool    `json:"can_bind"`
	UnavailableReason *string `json:"unavailable_reason"`
}
type ServiceProjection struct {
	NativeServices []contract.NativeService `json:"native_services"`
	ModelTypes     modeltype.Set            `json:"model_types"`
	ServiceRoutes  []ServiceRouteProjection `json:"service_routes"`
	Capabilities   BindingCapabilities      `json:"capabilities"`
}

type BindingTarget struct {
	ServiceProjection
	ID                string             `json:"id"`
	Revision          string             `json:"revision"`
	DonationID        string             `json:"donation_id"`
	DonationKeyID     string             `json:"donation_key_id"`
	UpstreamModelID   string             `json:"upstream_model_id"`
	EndpointID        string             `json:"endpoint_id"`
	ConnectorType     contract.Type      `json:"connector_type"`
	ConfigurationType contract.Type      `json:"configuration_type"`
	BaseURL           string             `json:"base_url"`
	Uses              []BindingTargetUse `json:"uses"`
}

type BindingTargetsUpdate struct {
	ExpectedBindingRevision string               `json:"expected_binding_revision"`
	Targets                 []BindingTargetInput `json:"targets"`
}

type validatedTarget struct {
	keyID    int64
	upstream string
	services contract.NativeServiceSet
	legacy   bool
}

func targetInputs(inputs []BindingTargetInput, legacy bool) ([]validatedTarget, error) {
	if len(inputs) == 0 || len(inputs) > maxBindingBatch {
		return nil, ErrInvalidRequest
	}
	out := make([]validatedTarget, 0, len(inputs))
	seen := map[string]bool{}
	for _, input := range inputs {
		id, err := parsePositiveID(input.DonationKeyID)
		if err != nil || !validUpstreamModel(input.UpstreamModelID) {
			return nil, ErrInvalidRequest
		}
		identity := input.DonationKeyID + "\x00" + input.UpstreamModelID
		if seen[identity] {
			return nil, ErrInvalidRequest
		}
		seen[identity] = true
		var services contract.NativeServiceSet
		if !legacy {
			services, err = contract.ParseNativeServices(input.NativeServices)
			if err != nil || services.Empty() {
				return nil, ErrInvalidRequest
			}
		}
		out = append(out, validatedTarget{id, input.UpstreamModelID, services, legacy})
	}
	return out, nil
}

func targetConnectionError(err error) error {
	switch {
	case errors.Is(err, connectionstore.ErrNotFound), errors.Is(err, connectionstore.ErrUnavailable):
		return ErrNotFound
	case errors.Is(err, connectionstore.ErrInvariant):
		return ErrInvariant
	default:
		return err
	}
}

func validateTargetsTx(ctx context.Context, tx *sql.Tx, scope charityscope.Scope, modelID int64, targets []validatedTarget, now int64) error {
	if len(targets) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.keyID)
	}
	uses, err := connectionstore.LoadApprovedUsesTx(ctx, tx, ids)
	if err != nil {
		return targetConnectionError(err)
	}
	var modelTypes modeltype.Set
	if err := tx.QueryRowContext(ctx, `SELECT model_types FROM charity_models WHERE id=?`, modelID).Scan(&modelTypes); err != nil {
		return err
	}
	for index := range targets {
		target := &targets[index]
		var donationID int64
		err := tx.QueryRowContext(ctx, `SELECT dk.donation_id FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id WHERE dk.id=? AND d.status IN ('pending','approved') AND d.terminal_at IS NULL AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?) AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=dk.endpoint_key_id)`, target.keyID, now).Scan(&donationID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := scope.RequireKey(ctx, tx, donationID, target.keyID, now, true); err != nil {
			return managementScopeError(err)
		}
		selected := []contract.NativeService{}
		for _, use := range uses[target.keyID] {
			if target.legacy {
				legacy, err := contract.LegacyNativeServices(use.Grant.ConfiguredType)
				if err != nil {
					return ErrInvariant
				}
				if !legacy.Has(use.NativeService) {
					continue
				}
			} else if !target.services.Has(use.NativeService) {
				continue
			}
			selected = append(selected, use.NativeService)
		}
		services, err := contract.ParseNativeServices(selected)
		if err != nil || services.Empty() {
			return ErrNotFound
		}
		if !target.legacy && services.Mask() != target.services.Mask() {
			return ErrNotFound
		}
		target.services = services
		compatible := false
		for _, use := range uses[target.keyID] {
			if !services.Has(use.NativeService) {
				continue
			}
			set, _ := contract.ParseNativeServices([]contract.NativeService{use.NativeService})
			for _, op := range modelTypes {
				routes, err := connector.ResolveServiceRoutes(use.Grant.ConfiguredType, set, use.Grant.RerankFormat, op)
				if err == nil && len(routes) > 0 {
					compatible = true
				} else if err != nil && !errors.Is(err, connector.ErrNoServiceRoute) {
					return ErrInvariant
				}
			}
		}
		if !compatible {
			return ErrInvalidRequest
		}
		if target.legacy {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_model_catalog WHERE donation_key_id=? AND normalized_model_id=? AND (automatic_supports>0 OR manual_supports>0))`, target.keyID, target.upstream).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
	}
	return nil
}

func insertBindingGroupTx(ctx context.Context, tx *sql.Tx, modelID int64, ord int, targets []validatedTarget, now int64) (int64, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO charity_model_bindings(charity_model_id,ord,created_at,updated_at) VALUES(?,?,?,?)`, modelID, ord, now, now)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil || id <= 0 {
		return 0, ErrInvariant
	}
	for index, target := range targets {
		origin := "reviewer"
		if target.legacy {
			origin = "legacy"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO charity_binding_targets(binding_id,donation_key_id,upstream_model_id,native_service_mask,selection_origin,ord,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,1,?,?)`, id, target.keyID, target.upstream, target.services.Mask(), origin, index, now, now); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func readTargetProjection(uses []connectionstore.UseAuthorization, selected contract.NativeServiceSet) (ServiceProjection, []BindingTargetUse, error) {
	projection := ServiceProjection{NativeServices: selected.Services(), ModelTypes: modeltype.Set{}, ServiceRoutes: []ServiceRouteProjection{}}
	refs := []BindingTargetUse{}
	operationSeen := map[contract.Operation]bool{}
	for _, use := range uses {
		if !selected.Has(use.NativeService) {
			continue
		}
		g := use.Grant
		refs = append(refs, BindingTargetUse{strconv.FormatInt(use.UseID, 10), use.NativeService, strconv.FormatInt(g.CredentialID, 10), strconv.FormatInt(g.CredentialVersion, 10), strconv.FormatInt(g.GrantRevision, 10), strconv.FormatInt(use.UseRevision, 10)})
		set, _ := contract.ParseNativeServices([]contract.NativeService{use.NativeService})
		for _, op := range contract.AllOperations() {
			routes, err := connector.ResolveServiceRoutes(g.ConfiguredType, set, g.RerankFormat, op)
			if errors.Is(err, connector.ErrNoServiceRoute) {
				continue
			}
			if err != nil {
				return projection, nil, ErrInvariant
			}
			for _, route := range routes {
				mode := "converted"
				if route.NativeMatch {
					mode = "native"
				}
				projection.ServiceRoutes = append(projection.ServiceRoutes, ServiceRouteProjection{op, route.NativeService, mode})
				operationSeen[op] = true
			}
		}
	}
	for _, op := range contract.AllOperations() {
		if operationSeen[op] {
			projection.ModelTypes = append(projection.ModelTypes, op)
		}
	}
	projection.Capabilities.CanBind = len(projection.ModelTypes) > 0
	if !projection.Capabilities.CanBind {
		reason := "unavailable"
		projection.Capabilities.UnavailableReason = &reason
	}
	return projection, refs, nil
}

func annotateCandidateServicesTx(ctx context.Context, tx *sql.Tx, items []AdminBindingCandidate, modelID int64) error {
	keys := make([]int64, 0, len(items))
	for _, item := range items {
		id, err := parsePositiveID(item.DonationKeyID)
		if err != nil {
			return ErrInvariant
		}
		keys = append(keys, id)
	}
	uses, err := connectionstore.LoadApprovedUsesTx(ctx, tx, keys)
	if err != nil {
		return targetConnectionError(err)
	}
	var types modeltype.Set
	if err := tx.QueryRowContext(ctx, `SELECT model_types FROM charity_models WHERE id=?`, modelID).Scan(&types); err != nil {
		return err
	}
	for index, keyID := range keys {
		services := []contract.NativeService{}
		for _, use := range uses[keyID] {
			services = append(services, use.NativeService)
		}
		selected, err := contract.ParseNativeServices(services)
		if err != nil {
			return ErrInvariant
		}
		projection, _, err := readTargetProjection(uses[keyID], selected)
		if err != nil {
			return err
		}
		compatible := false
		for _, op := range projection.ModelTypes {
			compatible = compatible || types.Supports(op)
		}
		projection.Capabilities.CanBind = compatible
		if !compatible {
			reason := "no_service_route"
			projection.Capabilities.UnavailableReason = &reason
		}
		items[index].ServiceProjection = projection
	}
	return nil
}
