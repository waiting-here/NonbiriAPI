package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	routeAdminBindingPresets   = "/admin/api/charity-binding-presets"
	routeAdminBindingPreset    = routeAdminBindingPresets + "/{channelId}"
	routeStewardBindingPresets = "/api/steward/charity-binding-presets"
	routeStewardBindingPreset  = routeStewardBindingPresets + "/{channelId}"
)

type BindingPresetChannel struct {
	ChannelID       string `json:"channel_id"`
	Name            string `json:"name"`
	ConnectorType   string `json:"connector_type"`
	BaseURL         string `json:"base_url"`
	Enabled         bool   `json:"enabled"`
	State           string `json:"state"`
	ChannelRevision string `json:"channel_revision"`
	PresetRevision  string `json:"preset_revision"`
	MappingCount    string `json:"mapping_count"`
}
type BindingPresetMapping struct {
	UpstreamModelID string  `json:"upstream_model_id"`
	CharityModelID  string  `json:"charity_model_id"`
	FullName        *string `json:"full_name"`
	BindingRevision *string `json:"binding_revision"`
}
type BindingPreset struct {
	ChannelID       string                 `json:"channel_id"`
	ChannelRevision string                 `json:"channel_revision"`
	PresetRevision  string                 `json:"preset_revision"`
	Mappings        []BindingPresetMapping `json:"mappings"`
}
type BindingPresetPair struct {
	UpstreamModelID string `json:"upstream_model_id"`
	CharityModelID  string `json:"charity_model_id"`
}
type BindingPresetInput struct {
	ExpectedChannelRevision string              `json:"expected_channel_revision"`
	ExpectedPresetRevision  string              `json:"expected_preset_revision"`
	Mappings                []BindingPresetPair `json:"mappings"`
}

const presetChannelSelect = `SELECT c.id,c.name,c.connector_type,c.canonical_base_url,c.enabled,c.state,c.revision,COALESCE(p.revision,0),(SELECT COUNT(*) FROM channel_charity_binding_mappings WHERE channel_id=c.id) FROM mainstream_channels c LEFT JOIN channel_charity_binding_presets p ON p.channel_id=c.id`

func scanPresetChannel(scan func(...any) error) (BindingPresetChannel, error) {
	var out BindingPresetChannel
	var revision, preset, count int64
	err := scan(&out.ChannelID, &out.Name, &out.ConnectorType, &out.BaseURL, &out.Enabled, &out.State, &revision, &preset, &count)
	out.ChannelRevision = strconv.FormatInt(revision, 10)
	out.PresetRevision = strconv.FormatInt(preset, 10)
	out.MappingCount = strconv.FormatInt(count, 10)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}
func readPresetChannel(ctx context.Context, tx *sql.Tx, id string) (BindingPresetChannel, error) {
	return scanPresetChannel(tx.QueryRowContext(ctx, presetChannelSelect+` WHERE c.id=?`, id).Scan)
}
func readBindingPreset(ctx context.Context, tx *sql.Tx, channel BindingPresetChannel) (BindingPreset, error) {
	out := BindingPreset{ChannelID: channel.ChannelID, ChannelRevision: channel.ChannelRevision, PresetRevision: channel.PresetRevision, Mappings: []BindingPresetMapping{}}
	rows, err := tx.QueryContext(ctx, `SELECT m.upstream_model_id,m.charity_model_id,c.full_name,c.binding_revision FROM channel_charity_binding_mappings m LEFT JOIN charity_models c ON c.id=m.charity_model_id WHERE m.channel_id=? ORDER BY m.ord`, channel.ChannelID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item BindingPresetMapping
		var id int64
		var rev sql.NullInt64
		if err := rows.Scan(&item.UpstreamModelID, &id, &item.FullName, &rev); err != nil {
			return out, err
		}
		item.CharityModelID = strconv.FormatInt(id, 10)
		if rev.Valid {
			v := strconv.FormatInt(rev.Int64, 10)
			item.BindingRevision = &v
		}
		out.Mappings = append(out.Mappings, item)
	}
	return out, rows.Err()
}
func (s *Service) bindingPresets(ctx context.Context, role roleKind, actorID int64, q, state string, page pagination.Request) (Page[BindingPresetChannel], error) {
	out := Page[BindingPresetChannel]{Data: []BindingPresetChannel{}}
	if ctx == nil || !page.Valid() || !utf8.ValidString(q) || utf8.RuneCountInString(q) > 128 || state != "active" && state != "retired" && state != "all" {
		return out, ErrInvalidRequest
	}
	tx, _, err := s.beginRoleTx(ctx, role, actorID)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	query := presetChannelSelect + ` WHERE instr(lower(c.name),lower(?))>0`
	args := []any{q}
	if state != "all" {
		query += ` AND c.state=?`
		args = append(args, state)
	}
	meta, offset, err := pageWindow(ctx, tx, query, args, page)
	if err != nil {
		return out, err
	}
	out.Pagination = &meta
	rows, err := tx.QueryContext(ctx, query+` ORDER BY c.name,c.id LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanPresetChannel(rows.Scan)
		if err != nil {
			return out, err
		}
		out.Data = append(out.Data, item)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) bindingPreset(ctx context.Context, role roleKind, actorID int64, id string) (BindingPreset, error) {
	var empty BindingPreset
	if ctx == nil || !db.ValidateOpaqueID(id, "mch_") {
		return empty, ErrInvalidRequest
	}
	tx, _, err := s.beginRoleTx(ctx, role, actorID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	channel, err := readPresetChannel(ctx, tx, id)
	if err != nil {
		return empty, err
	}
	out, err := readBindingPreset(ctx, tx, channel)
	if err != nil {
		return empty, err
	}
	return out, tx.Commit()
}
func (s *Service) saveBindingPreset(ctx context.Context, role roleKind, actorID int64, id string, mutation resources.ControlMutation, input BindingPresetInput) (resources.MutationResult[BindingPreset], error) {
	empty := resources.MutationResult[BindingPreset]{}
	route := routeAdminBindingPreset
	if role == roleSteward {
		route = routeStewardBindingPreset
	}
	_, err := parsePositiveID(input.ExpectedChannelRevision)
	expected, revErr := parseNonnegativeRevision(input.ExpectedPresetRevision)
	if ctx == nil || !db.ValidateOpaqueID(id, "mch_") || err != nil || revErr != nil || len(input.Mappings) > maxChannelBindingMappings || input.Mappings == nil || mutation.Method != http.MethodPut || mutation.Route != route || mutation.Query != "" || len(mutation.PathIDs) != 1 || mutation.PathIDs[0] != id {
		return empty, ErrInvalidRequest
	}
	seen := map[string]bool{}
	for _, mapping := range input.Mappings {
		_, err := parsePositiveID(mapping.CharityModelID)
		pair := mapping.CharityModelID + "\x00" + mapping.UpstreamModelID
		if err != nil || !validManualName(mapping.UpstreamModelID) || seen[pair] {
			return empty, ErrInvalidRequest
		}
		seen[pair] = true
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	tx, actor, err := s.beginRoleTx(ctx, role, actorID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	decision, err := beginMutation(ctx, tx, role, actor, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[BindingPreset](decision)
	}
	channel, err := readPresetChannel(ctx, tx, id)
	if err != nil {
		return empty, err
	}
	if channel.ChannelRevision != input.ExpectedChannelRevision || channel.PresetRevision != input.ExpectedPresetRevision || channel.State != "active" {
		return empty, ErrConflict
	}
	for _, mapping := range input.Mappings {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_models WHERE id=?)`, mapping.CharityModelID).Scan(&exists); err != nil {
			return empty, err
		}
		if !exists {
			return empty, ErrNotFound
		}
	}
	if expected == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO channel_charity_binding_presets(channel_id,revision) VALUES(?,1)`, id)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE channel_charity_binding_presets SET revision=revision+1 WHERE channel_id=? AND revision=?`, id, expected)
	}
	if err != nil {
		return empty, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM channel_charity_binding_mappings WHERE channel_id=?`, id); err != nil {
		return empty, err
	}
	for index, mapping := range input.Mappings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_charity_binding_mappings(channel_id,ord,upstream_model_id,charity_model_id) VALUES(?,?,?,?)`, id, index, mapping.UpstreamModelID, mapping.CharityModelID); err != nil {
			return empty, err
		}
	}
	channel.PresetRevision = strconv.FormatInt(expected+1, 10)
	value, err := readBindingPreset(ctx, tx, channel)
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

func (s *Service) applyBindingPreset(ctx context.Context, role roleKind, actorID, donationID int64, mutation resources.ControlMutation, input ApplyBindingPreset) (resources.MutationResult[BindingMatrixReceipt], error) {
	empty := resources.MutationResult[BindingMatrixReceipt]{}
	route := routeAdminApplyBindingPreset
	if role == roleSteward {
		route = routeStewardApplyBindingPreset
	}
	expected, err := parsePositiveID(input.ExpectedDonationRevision)
	if ctx == nil || err != nil || !db.ValidateOpaqueID(input.ChannelID, "mch_") || len(input.Models) > maxChannelBindingMappings || !validMutation(mutation, http.MethodPost, route, donationID) {
		return empty, ErrInvalidRequest
	}
	if _, err := parsePositiveID(input.ExpectedChannelRevision); err != nil {
		return empty, err
	}
	if _, err := parsePositiveID(input.ExpectedPresetRevision); err != nil {
		return empty, err
	}
	revisions := map[string]int64{}
	for _, model := range input.Models {
		if _, err := parsePositiveID(model.CharityModelID); err != nil {
			return empty, err
		}
		revision, err := parseNonnegativeRevision(model.ExpectedBindingRevision)
		if err != nil {
			return empty, err
		}
		if _, ok := revisions[model.CharityModelID]; ok {
			return empty, ErrInvalidRequest
		}
		revisions[model.CharityModelID] = revision
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	tx, actor, err := s.beginRoleTx(ctx, role, actorID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	revision, err := donationBindingRevision(ctx, tx, donationID)
	if err != nil {
		return empty, err
	}
	channel, err := readPresetChannel(ctx, tx, input.ChannelID)
	if err != nil {
		return empty, err
	}
	if !channel.Enabled || channel.State != "active" {
		return empty, ErrConflict
	}
	keys, err := readBindingKeys(ctx, tx, donationID, 0, now, &channel)
	if err != nil {
		return empty, err
	}
	decision, err := beginMutation(ctx, tx, role, actor, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[BindingMatrixReceipt](decision)
	}
	if revision != expected || channel.ChannelRevision != input.ExpectedChannelRevision || channel.PresetRevision != input.ExpectedPresetRevision {
		return empty, ErrConflict
	}
	preset, err := readBindingPreset(ctx, tx, channel)
	if err != nil {
		return empty, err
	}
	if len(preset.Mappings) == 0 {
		return empty, ErrConflict
	}
	targets := make([]bindingTarget, 0, len(preset.Mappings))
	used := map[string]bool{}
	for _, mapping := range preset.Mappings {
		id, _ := parsePositiveID(mapping.CharityModelID)
		expected := int64(0)
		if mapping.BindingRevision != nil {
			var ok bool
			expected, ok = revisions[mapping.CharityModelID]
			if !ok {
				return empty, ErrConflict
			}
			used[mapping.CharityModelID] = true
		} else if _, ok := revisions[mapping.CharityModelID]; ok {
			used[mapping.CharityModelID] = true
		}
		targets = append(targets, bindingTarget{id, mapping.UpstreamModelID, expected})
	}
	if len(used) != len(revisions) {
		return empty, ErrInvalidRequest
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
