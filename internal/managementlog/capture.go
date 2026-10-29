package managementlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// CaptureEventID is called by the database at a transaction boundary. Ordinary
// requests and idempotent replays do not activate business snapshots.
func CaptureEventID(ctx context.Context) string {
	state := stateFrom(ctx)
	if state == nil {
		return ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.replay || state.metadata.Domain == "" {
		return ""
	}
	return state.metadata.EventID
}

// StartCaptureTx changes the active event without discarding a parent event's
// samples. It is used when a rule or AI establishes its authority inside a tx.
func StartCaptureTx(ctx context.Context, tx *sql.Tx) error {
	return startCaptureEventTx(ctx, tx, CaptureEventID(ctx))
}
func startCaptureEventTx(ctx context.Context, tx *sql.Tx, eventID string) error {
	if eventID == "" {
		_, err := tx.ExecContext(ctx, "DELETE FROM management_capture_context")
		return err
	}
	if !tokenOK(eventID, 128) {
		return ErrInvalid
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO management_capture_context(id,event_id) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET event_id=excluded.event_id", eventID)
	return err
}

// StartRelatedCaptureTx activates a durable task's original authority before
// its business changes. Pre-upgrade tasks deliberately have no inferred parent.
func StartRelatedCaptureTx(ctx context.Context, tx *sql.Tx, kind, id string) error {
	var eventID string
	err := tx.QueryRowContext(ctx, "SELECT event_id FROM management_operations WHERE correlation_kind=? AND correlation_id=? AND cleaning=0 AND outcome='accepted' ORDER BY id DESC LIMIT 1", kind, id).Scan(&eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return startCaptureEventTx(ctx, tx, eventID)
}

type capturedRow struct {
	kind, id      string
	before, after map[string]*string
	facts         map[string]string
}

func (r capturedRow) name() string {
	if value := r.after["$name"]; value != nil {
		return *value
	}
	if value := r.before["$name"]; value != nil {
		return *value
	}
	return ""
}
func (r capturedRow) previousName() string {
	if value := r.before["$name"]; value != nil && *value != r.name() {
		return *value
	}
	return ""
}
func (r capturedRow) changes() ([]Change, bool) {
	names := map[string]bool{}
	for key := range r.before {
		names[key] = true
	}
	for key := range r.after {
		names[key] = true
	}
	for key := range r.facts {
		names[key] = true
	}
	delete(names, "$name")
	sorted := make([]string, 0, len(names))
	for key := range names {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	result := []Change{}
	for _, key := range sorted {
		before, after := r.before[key], r.after[key]
		equal := before == nil && after == nil || before != nil && after != nil && *before == *after
		if equal && r.facts[key] == "" {
			continue
		}
		op := "updated"
		if r.before == nil {
			op = "created"
		} else if r.after == nil {
			op = "deleted"
		} else if after == nil && before != nil {
			op = "cleared"
		}
		change := Change{Field: key, Operation: op, Before: before, After: after, Unit: fieldUnit(key)}
		if r.kind == "site_config" && key == "value" {
			change.Field, change.Unit = r.id, fieldUnit(r.id)
		}
		result = append(result, change)
	}
	return normalizeChanges(result)
}
func (r capturedRow) state() string {
	if r.before == nil && r.after == nil {
		return "unchanged"
	}
	if r.before == nil {
		return "created"
	}
	if r.after == nil {
		return "deleted"
	}
	changes, _ := r.changes()
	if len(changes) == 0 {
		return "unchanged"
	}
	return "updated"
}
func fieldUnit(field string) string {
	switch {
	case strings.HasSuffix(field, "_milli") || strings.Contains(field, "_user_price") || strings.Contains(field, "_donor_reward"):
		return "milli_credits"
	case field == "balance_mag":
		return "milli_credits"
	case strings.HasSuffix(field, "_percent"):
		return "percent"
	case strings.HasSuffix(field, "_bp") || strings.HasSuffix(field, "_bps"):
		return "basis_points"
	case strings.HasSuffix(field, "_seconds"):
		return "seconds"
	case strings.HasSuffix(field, "_days"):
		return "days"
	case strings.HasSuffix(field, "_at") || strings.HasSuffix(field, "_until"):
		return "unix_seconds"
	}
	return ""
}

func captureKind(kind string) string {
	kind = strings.ReplaceAll(kind, "-", "_")
	aliases := map[string]string{
		"charity_model": "charity_models", "charity_binding": "charity_model_bindings", "bindings": "charity_model_bindings",
		"donation": "donations", "donation_key": "donation_keys", "donation_use": "donation_uses", "discovery_source": "discovery_sources",
		"donation_source": "discovery_sources", "donation_sources": "discovery_sources", "source": "discovery_sources",
		"user": "users", "steward": "users", "mainstream_channel": "mainstream_channels", "announcement": "announcements",
		"reward_campaign": "reward_campaigns", "reward_claim": "reward_claims", "report_case": "report_cases", "reports": "report_cases", "report_target": "report_targets",
		"client_rule": "risk_client_rules", "risk_scan": "risk_client_scans", "blacklist": "discord_blacklist",
		"ai_config": "ai_agent_settings", "ai_memory": "ai_agent_memories", "ai_personal_memory": "ai_agent_personal_memories", "ai_plan": "ai_agent_plans",
		"driver": "agent_driver_profiles", "ai_driver": "agent_driver_profiles", "agent_driver": "agent_driver_profiles", "driver_binding": "agent_driver_bindings",
		"image_model": "image_activity_models", "playtest": "fatfish_playtests", "level": "fatfish_levels", "period": "fatfish_periods", "node": "fatfish_nodes",
		"drivers": "agent_driver_profiles", "memories": "ai_agent_memories", "personal_memories": "ai_agent_personal_memories", "caller_key": "caller_keys",
		"codex_connection": "agent_codex_session", "codex": "agent_codex_session",
		"legal_hold": "legal_holds", "config": "site_config", "maintenance": "site_config", "alert": "admin_alerts", "alerts": "admin_alerts",
		"charity_price_template": "charity_price_templates", "endpoint_tags": "admin_endpoint_tags",
		"endpoint_tag_rule": "endpoint_tag_rules", "endpoint_tag_rules": "endpoint_tag_rules",
		"levels": "fatfish_levels", "versions": "fatfish_level_versions", "periods": "fatfish_periods", "nodes": "fatfish_nodes", "playtests": "fatfish_playtests",
		"policies": "game_ai_policies", "bots": "game_ai_bots", "campaigns": "reward_campaigns", "claims": "reward_claims",
		"rewards": "reward_campaigns", "client_rules": "risk_client_rules", "client_scans": "risk_client_scans", "scans": "risk_client_scans",
		"charity_binding_presets": "channel_charity_binding_presets", "inactivity_policy": "inactivity_policy", "limited_activities": "limited_activity_configs",
		"risk_config": "risk_audit_config", "pools": "shared_pools", "image_models": "image_activity_models",
	}
	if value := aliases[kind]; value != "" {
		return value
	}
	return kind
}

// enrichCaptureTx consumes only this event's bounded journal in the same tx as
// the immutable audit write. Another event in the tx retains its own samples.
func enrichCaptureTx(ctx context.Context, tx *sql.Tx, eventID string, items []Item) ([]Item, bool, error) {
	var active bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM management_capture_context WHERE event_id=?) OR EXISTS(SELECT 1 FROM management_capture_totals WHERE event_id=?)", eventID, eventID).Scan(&active); err != nil {
		return nil, false, err
	}
	if !active {
		return items, false, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT kind,target_id,before_json,after_json,facts_json FROM management_capture_rows WHERE event_id=? ORDER BY kind,target_id", eventID)
	if err != nil {
		return nil, false, err
	}
	captured := []capturedRow{}
	for rows.Next() {
		var row capturedRow
		var before, after sql.NullString
		var facts string
		if err = rows.Scan(&row.kind, &row.id, &before, &after, &facts); err != nil {
			break
		}
		if before.Valid {
			err = json.Unmarshal([]byte(before.String), &row.before)
		}
		if err != nil {
			break
		}
		if after.Valid {
			err = json.Unmarshal([]byte(after.String), &row.after)
		}
		if err != nil {
			break
		}
		err = json.Unmarshal([]byte(facts), &row.facts)
		if err != nil {
			break
		}
		captured = append(captured, row)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	if readErr != nil {
		return nil, false, readErr
	}
	totals, err := tx.QueryContext(ctx, "SELECT kind,created,updated,deleted,(SELECT COUNT(*) FROM management_capture_rows r WHERE r.event_id=t.event_id AND r.kind=t.kind),omitted FROM management_capture_totals t WHERE event_id=? ORDER BY kind", eventID)
	if err != nil {
		return nil, false, err
	}
	impacts := []Impact{}
	for totals.Next() {
		var impact Impact
		if err = totals.Scan(&impact.Kind, &impact.Created, &impact.Updated, &impact.Deleted, &impact.Sampled, &impact.Omitted); err != nil {
			break
		}
		impacts = append(impacts, impact)
	}
	readErr = totals.Err()
	totals.Close()
	if err != nil {
		return nil, false, err
	}
	if readErr != nil {
		return nil, false, readErr
	}
	items = cloneItems(items)
	if len(items) == 0 {
		items = []Item{{Outcome: Succeeded}}
	}
	used := make([]bool, len(captured))
	allUnchanged := len(captured) > 0
	for _, row := range captured {
		allUnchanged = allUnchanged && row.state() == "unchanged"
	}
	for _, impact := range impacts {
		allUnchanged = allUnchanged && impact.Omitted == 0 && impact.Created == 0 && impact.Updated == 0 && impact.Deleted == 0
	}
	// Equal bounded projections cannot prove that a row was unchanged when
	// the transaction contains actual writes. Leave that state unspecified.
	snapshotState := func(row capturedRow) string {
		state := row.state()
		if state == "unchanged" && !allUnchanged {
			return ""
		}
		return state
	}
	for i := range items {
		item := &items[i]
		kind, id := captureKind(item.TargetKind), item.TargetID
		if strings.HasPrefix(item.Action, "picture-book.") && item.TargetKind == "models" {
			kind = "image_activity_models"
		}
		if item.TargetKind == "gacha_configuration" {
			kind, id = "gacha_settings", "1"
			if item.TargetID != "settings" {
				kind, id = "gacha_pools", item.TargetID
			}
			if parts := strings.SplitN(item.TargetID, ":", 2); len(parts) == 2 {
				if parts[0] == "pool" {
					kind, id = "gacha_pools", parts[1]
				}
				if parts[0] == "preset" {
					kind, id = "gacha_presets", parts[1]
				}
			}
		}
		if kind == "users" {
			for _, row := range captured {
				if row.kind == "steward_permissions" && row.id == id {
					kind = "steward_permissions"
					break
				}
			}
		}
		if len(items) == 1 && len(captured) == 1 && (id == "" || id == captured[0].id) {
			kind = captured[0].kind
		}
		// Request field names cannot establish a committed change.
		if item.Details.Version == 0 && (item.Outcome == Succeeded || item.Outcome == NoChange) {
			item.Changes = nil
		}
		for j, row := range captured {
			if used[j] || kind != row.kind || id != "" && id != row.id {
				continue
			}
			used[j] = true
			item.TargetID = row.id
			if !textOK(item.TargetID, 192) {
				item.TargetID = ""
			}
			item.TargetName = snapshotName(row.name())
			var dropped bool
			item.Changes, dropped = row.changes()
			item.Details = Details{Version: 1, PreviousName: snapshotName(row.previousName()), State: snapshotState(row), Truncated: dropped}
			if allUnchanged && item.Outcome == Succeeded {
				item.Outcome = NoChange
			}
			break
		}
	}
	related := []RelatedObject{}
	truncated := false
	for j, row := range captured {
		if used[j] {
			continue
		}
		if len(related) >= 32 {
			truncated = true
			continue
		}
		id, _ := boundedText(row.id, 192)
		changes, dropped := row.changes()
		truncated = truncated || dropped
		for k := range changes {
			for _, value := range []**string{&changes[k].Before, &changes[k].After} {
				if *value != nil {
					bounded, short := boundedText(**value, 128)
					*value = &bounded
					changes[k].Truncated = changes[k].Truncated || short
				}
			}
		}
		related = append(related, RelatedObject{Kind: row.kind, ID: id, Name: snapshotName(row.name()), PreviousName: snapshotName(row.previousName()), State: snapshotState(row), Changes: changes})
	}
	for _, impact := range impacts {
		if impact.Omitted > 0 {
			truncated = true
		}
	}
	items[0].Details.Impacts = impacts
	items[0].Details.Related = related
	items[0].Details.Truncated = items[0].Details.Truncated || truncated
	items[0].Details = normalizeDetails(items[0].Details)
	if len(impacts) > 0 {
		items[0].Details.Version = 1
	}
	for _, table := range []string{"management_capture_rows", "management_capture_totals", "management_capture_context"} {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE event_id=?", eventID); err != nil {
			return nil, false, err
		}
	}
	return items, len(impacts) > 0, nil
}

func insertTermsTx(ctx context.Context, tx *sql.Tx, id, ordinal int64, item Item) error {
	terms := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && textOK(value, 2048) {
			terms[value] = true
		}
	}
	add(item.TargetName)
	add(item.TargetID)
	add(item.Details.PreviousName)
	for _, change := range item.Changes {
		add(change.Field)
	}
	for _, related := range item.Details.Related {
		add(related.Name)
		add(related.ID)
		add(related.PreviousName)
		for _, change := range related.Changes {
			add(change.Field)
		}
	}
	for term := range terms {
		if _, err := tx.ExecContext(ctx, "INSERT INTO management_operation_terms(term,operation_id,ordinal) VALUES(?,?,?) ON CONFLICT DO NOTHING", term, id, ordinal); err != nil {
			return fmt.Errorf("index management snapshot: %w", err)
		}
	}
	return nil
}
