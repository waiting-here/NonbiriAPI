package charityrouting

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func parsePresetApplication(input ApplyBindingPreset) (int64, map[string]int64, error) {
	expected, err := parsePositiveID(input.ExpectedDonationRevision)
	if err != nil || !db.ValidateOpaqueID(input.ChannelID, "mch_") || len(input.Models) > maxChannelBindingMappings {
		return 0, nil, ErrInvalidRequest
	}
	if _, err := parsePositiveID(input.ExpectedChannelRevision); err != nil {
		return 0, nil, err
	}
	if _, err := parsePositiveID(input.ExpectedPresetRevision); err != nil {
		return 0, nil, err
	}
	revisions := map[string]int64{}
	for _, model := range input.Models {
		if _, err := parsePositiveID(model.CharityModelID); err != nil {
			return 0, nil, err
		}
		revision, err := parseNonnegativeRevision(model.ExpectedBindingRevision)
		if err != nil {
			return 0, nil, err
		}
		if _, ok := revisions[model.CharityModelID]; ok {
			return 0, nil, ErrInvalidRequest
		}
		revisions[model.CharityModelID] = revision
	}
	return expected, revisions, nil
}

func authorizePresetSourcesTx(ctx context.Context, tx *sql.Tx, scope charityscope.Scope, donationID int64, preset BindingPreset, keys []bindingKey, now int64, resolve func(int64) (charityscope.Scope, error)) error {
	if scope.DonationScope == authz.DonationScopeNone {
		return authz.ErrPermissionDenied
	}
	checkedSources := false
	for _, mapping := range preset.Mappings {
		if (!scope.Trainee && !scope.RestrictedKeys()) || mapping.BindingRevision == nil {
			continue
		}
		modelID, err := parsePositiveID(mapping.CharityModelID)
		if err != nil {
			return ErrInvariant
		}
		targetScope, err := resolve(modelID)
		if err != nil {
			return err
		}
		checkedSources = true
		for _, key := range keys {
			if err := targetScope.RequireKey(ctx, tx, donationID, key.id, now, true); err != nil {
				return managementScopeError(err)
			}
		}
	}
	if scope.RestrictedKeys() && !checkedSources {
		for _, key := range keys {
			if err := scope.RequireKey(ctx, tx, donationID, key.id, now, true); err != nil {
				return managementScopeError(err)
			}
		}
	}
	return nil
}

func presetTargets(preset BindingPreset, revisions map[string]int64) ([]bindingTarget, error) {
	targets := make([]bindingTarget, 0, len(preset.Mappings))
	used := map[string]bool{}
	for _, mapping := range preset.Mappings {
		id, _ := parsePositiveID(mapping.CharityModelID)
		expected := int64(0)
		if mapping.BindingRevision != nil {
			var ok bool
			expected, ok = revisions[mapping.CharityModelID]
			if !ok {
				return nil, ErrConflict
			}
			used[mapping.CharityModelID] = true
		} else if _, ok := revisions[mapping.CharityModelID]; ok {
			used[mapping.CharityModelID] = true
		}
		targets = append(targets, bindingTarget{modelID: id, upstream: mapping.UpstreamModelID, expected: expected, legacy: true})
	}
	if len(used) != len(revisions) {
		return nil, ErrInvalidRequest
	}
	return targets, nil
}
