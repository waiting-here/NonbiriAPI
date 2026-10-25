package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type agentPresetReadInput struct {
	DonationID string `json:"donation_id"`
	ChannelID  string `json:"channel_id,omitempty"`
}

// The same safe snapshot is used by the read tool and the human plan preview.
type agentPresetSnapshot struct {
	DonationID       string               `json:"donation_id"`
	DonationRevision string               `json:"donation_revision"`
	DonationKeyIDs   []string             `json:"donation_key_ids"`
	Channel          BindingPresetChannel `json:"channel"`
	Preset           BindingPreset        `json:"preset"`
}

type agentPresetReceipt struct {
	agentPresetSnapshot
	BindingMatrixReceipt
	MissingModelCount string `json:"missing_model_count"`
	AlreadyBoundCount string `json:"already_bound_count"`
	SkippedCount      string `json:"skipped_count"`
	Summary           string `json:"summary"`
}

func presetSnapshot(donationID, revision int64, channel BindingPresetChannel, preset BindingPreset, keys []bindingKey) agentPresetSnapshot {
	out := agentPresetSnapshot{DonationID: strconv.FormatInt(donationID, 10), DonationRevision: strconv.FormatInt(revision, 10), DonationKeyIDs: []string{}, Channel: channel, Preset: preset}
	for _, key := range keys {
		out.DonationKeyIDs = append(out.DonationKeyIDs, strconv.FormatInt(key.id, 10))
	}
	return out
}

func (s *Service) agentAuthorizePresetTx(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, scope charityscope.Scope, donationID int64, preset BindingPreset, keys []bindingKey, now int64, authorize agentplan.Authorize) error {
	return authorizePresetSourcesTx(ctx, tx, scope, donationID, preset, keys, now, func(modelID int64) (charityscope.Scope, error) {
		_, targetScope, err := s.agentScope(ctx, tx, actor, agentplan.Operation{Kind: "charity.binding.apply_preset"}, agentInput{modelID: modelID}, authorize)
		return targetScope, err
	})
}

func (s *Service) agentReadPresets(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, args json.RawMessage, authorize agentplan.Authorize) (json.RawMessage, error) {
	var input agentPresetReadInput
	if err := agentDecode(args, &input); err != nil {
		return nil, err
	}
	donationID, err := parsePositiveID(input.DonationID)
	if err != nil || input.ChannelID != "" && !db.ValidateOpaqueID(input.ChannelID, "mch_") {
		return nil, ErrInvalidRequest
	}
	_, scope, err := s.agentScope(ctx, tx, actor, agentplan.Operation{Kind: "charity_binding_presets"}, agentInput{}, authorize)
	if err != nil {
		return nil, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return nil, err
	}
	// Authorize the donation before consulting its source snapshots or presets.
	if err := scope.RequireDonation(ctx, tx, donationID, now); err != nil {
		return nil, managementScopeError(err)
	}
	revision, err := donationBindingRevision(ctx, tx, donationID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, presetChannelSelect+` WHERE c.enabled=1 AND c.state='active'
 AND (?='' OR c.id=?)
 AND EXISTS(SELECT 1 FROM channel_charity_binding_mappings pm WHERE pm.channel_id=c.id)
 AND EXISTS(SELECT 1 FROM donation_keys dk WHERE dk.donation_id=? AND dk.ended_at IS NULL
  AND (dk.expires_at IS NULL OR dk.expires_at>?) AND dk.mainstream_channel_id=c.id
  AND dk.connector_type=e.connector_type AND dk.canonical_base_url=e.canonical_base_url)
 ORDER BY c.name,c.id`, input.ChannelID, input.ChannelID, donationID, now)
	if err != nil {
		return nil, err
	}
	channels := []BindingPresetChannel{}
	for rows.Next() {
		channel, err := scanPresetChannel(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if err := agentplan.CheckReadCount(len(channels) + 1); err != nil {
			rows.Close()
			return nil, err
		}
		channels = append(channels, channel)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	data := []json.RawMessage{}
	var budget agentplan.ReadBudget
	for _, channel := range channels {
		keys, err := readBindingKeys(ctx, tx, donationID, 0, now, &channel)
		if err != nil {
			return nil, err
		}
		preset, err := readBindingPreset(ctx, tx, channel)
		if err != nil {
			return nil, err
		}
		if err := s.agentAuthorizePresetTx(ctx, tx, actor, scope, donationID, preset, keys, now, authorize); err != nil {
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) || errors.Is(err, authz.ErrPermissionDenied) || errors.Is(err, authz.ErrForbidden) {
				continue
			}
			return nil, err
		}
		raw, err := json.Marshal(presetSnapshot(donationID, revision, channel, preset, keys))
		if err != nil {
			return nil, err
		}
		if err := budget.AddEncoded(raw); err != nil {
			return nil, err
		}
		data = append(data, raw)
	}
	return agentplan.MarshalRead(struct {
		Data  []json.RawMessage `json:"data"`
		Total string            `json:"total"`
	}{data, strconv.Itoa(len(data))})
}

func (s *Service) agentPresetApplicationTx(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, scope charityscope.Scope, in agentInput, now int64, authorize agentplan.Authorize) (agentPresetSnapshot, []bindingKey, error) {
	var empty agentPresetSnapshot
	if err := scope.RequireDonation(ctx, tx, in.donationID, now); err != nil {
		return empty, nil, managementScopeError(err)
	}
	revision, err := donationBindingRevision(ctx, tx, in.donationID)
	if err != nil {
		return empty, nil, err
	}
	channel, err := readPresetChannel(ctx, tx, in.preset.ChannelID)
	if err != nil {
		return empty, nil, err
	}
	if !channel.Enabled || channel.State != "active" || revision != in.expected || channel.ChannelRevision != in.preset.ExpectedChannelRevision || channel.PresetRevision != in.preset.ExpectedPresetRevision {
		return empty, nil, ErrConflict
	}
	keys, err := readBindingKeys(ctx, tx, in.donationID, 0, now, &channel)
	if err != nil {
		return empty, nil, err
	}
	preset, err := readBindingPreset(ctx, tx, channel)
	if err != nil {
		return empty, nil, err
	}
	if err := s.agentAuthorizePresetTx(ctx, tx, actor, scope, in.donationID, preset, keys, now, authorize); err != nil {
		return empty, nil, err
	}
	if len(preset.Mappings) == 0 {
		return empty, nil, ErrConflict
	}
	if _, err := presetTargets(preset, in.revisions); err != nil {
		return empty, nil, err
	}
	for _, mapping := range preset.Mappings {
		if mapping.BindingRevision != nil && strconv.FormatInt(in.revisions[mapping.CharityModelID], 10) != *mapping.BindingRevision {
			return empty, nil, ErrConflict
		}
	}
	return presetSnapshot(in.donationID, revision, channel, preset, keys), keys, nil
}

func (s *Service) agentApplyPresetTx(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, scope charityscope.Scope, in agentInput, now int64, authorize agentplan.Authorize) (agentPresetReceipt, error) {
	var out agentPresetReceipt
	snapshot, keys, err := s.agentPresetApplicationTx(ctx, tx, actor, scope, in, now, authorize)
	if err != nil {
		return out, err
	}
	targets, err := presetTargets(snapshot.Preset, in.revisions)
	if err != nil {
		return out, err
	}
	receipt, err := applyBindingMatrix(ctx, tx, targets, keys, now)
	if err != nil {
		return out, err
	}
	missing, already := 0, 0
	for _, row := range receipt.Items {
		for _, status := range row.Results {
			switch status {
			case "added":
			case "missing_model":
				missing++
			case "already_bound":
				already++
			case "capacity_exceeded":
				return out, ErrResourceLimit
			default:
				return out, ErrConflict
			}
		}
	}
	out = agentPresetReceipt{agentPresetSnapshot: snapshot, BindingMatrixReceipt: receipt, MissingModelCount: strconv.Itoa(missing), AlreadyBoundCount: strconv.Itoa(already), SkippedCount: strconv.Itoa(missing + already), Summary: fmt.Sprintf("Added %s bindings; skipped %d missing upstream models and %d existing bindings.", receipt.AddedCount, missing, already)}
	return out, nil
}
