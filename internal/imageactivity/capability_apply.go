package imageactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"reflect"
	"sort"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type CapabilityChange struct {
	Key      ParameterKey `json:"key"`
	Kind     string       `json:"kind"`
	Conflict bool         `json:"conflict"`
}

type CapabilityReview struct {
	SnapshotID string             `json:"snapshot_id"`
	ExpiresAt  int64              `json:"expires_at"`
	Source     CompiledCapability `json:"source"`
	Changes    []CapabilityChange `json:"changes"`
}

type CapabilityApplyInput struct {
	SnapshotID       string `json:"snapshot_id"`
	ExpectedRevision string `json:"expected_revision"`
	Confirm          bool   `json:"confirm"`
}

func candidateTx(ctx context.Context, tx *sql.Tx, modelID, snapshotID string, now int64) (CapabilityReview, []byte, error) {
	out := CapabilityReview{Changes: []CapabilityChange{}}
	var raw, metadata string
	query := `SELECT s.id,s.expires_at,c.source_json,c.metadata_json FROM image_capability_snapshots s
	 JOIN image_capability_candidates c ON c.snapshot_id=s.id
	 WHERE c.model_id=? AND s.expires_at>?`
	args := []any{modelID, now}
	if snapshotID != "" {
		query += " AND s.id=?"
		args = append(args, snapshotID)
	}
	query += " ORDER BY s.rowid DESC LIMIT 1"
	err := tx.QueryRowContext(ctx, query, args...).Scan(&out.SnapshotID, &out.ExpiresAt, &raw, &metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil, ErrNotFound
	}
	if err != nil {
		return out, nil, err
	}
	if json.Unmarshal([]byte(raw), &out.Source) != nil {
		return out, nil, ErrInvariant
	}
	return out, []byte(metadata), nil
}

func capabilityChanges(oldSource, newSource CompiledCapability, manual []ManualCapability) []CapabilityChange {
	previous := map[ParameterKey]CapabilityField{}
	for _, field := range oldSource.Parameters {
		previous[field.Rule.Key] = field
	}
	override := map[ParameterKey]bool{}
	for _, field := range manual {
		override[field.Rule.Key] = true
	}
	changes := make([]CapabilityChange, 0)
	seen := map[ParameterKey]bool{}
	for _, field := range newSource.Parameters {
		key := field.Rule.Key
		seen[key] = true
		old, exists := previous[key]
		if !exists || !reflect.DeepEqual(old, field) {
			kind := "changed"
			if !exists {
				kind = "added"
			} else if old.Support == CapabilitySupported && field.Support != CapabilitySupported {
				kind = "removed"
			} else if old.Rule.Default != nil && !reflect.DeepEqual(old.Rule.Default, field.Rule.Default) {
				kind = "default_changed"
			} else if old.Rule.Minimum != nil && field.Rule.Minimum != nil && *field.Rule.Minimum > *old.Rule.Minimum || old.Rule.Maximum != nil && field.Rule.Maximum != nil && *field.Rule.Maximum < *old.Rule.Maximum {
				kind = "range_narrowed"
			}
			changes = append(changes, CapabilityChange{Key: key, Kind: kind, Conflict: override[key]})
		}
	}
	for key := range previous {
		if !seen[key] {
			changes = append(changes, CapabilityChange{Key: key, Kind: "removed", Conflict: override[key]})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Key < changes[j].Key })
	return changes
}

func manualFromModel(m modelSnapshot) []ManualCapability {
	if m.revision > 0 {
		return append([]ManualCapability(nil), m.manual...)
	}
	out := make([]ManualCapability, 0, len(m.input.Parameters))
	for _, rule := range m.input.Parameters {
		support := CapabilityUnsupported
		if rule.Supported {
			support = CapabilitySupported
		}
		out = append(out, ManualCapability{Rule: rule, Support: support})
	}
	return out
}

func (s *Service) ReviewCapabilities(ctx context.Context, admin int64, modelID string) (CapabilityReview, error) {
	var out CapabilityReview
	if !db.ValidateOpaqueID(modelID, "imdl_") {
		return out, ErrInvalid
	}
	now, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return out, err
	}
	model, err := modelTx(ctx, tx, modelID, 0)
	if err != nil {
		return out, err
	}
	out, _, err = candidateTx(ctx, tx, modelID, "", now)
	if err != nil {
		return out, err
	}
	var oldRaw string
	if model.revision > 0 {
		if err = tx.QueryRowContext(ctx, `SELECT source_json FROM image_model_capability_revisions WHERE model_id=? AND revision=?`, modelID, model.capabilityRevision).Scan(&oldRaw); err != nil {
			return out, err
		}
	}
	var previous CompiledCapability
	if oldRaw != "" && json.Unmarshal([]byte(oldRaw), &previous) != nil {
		return out, ErrInvariant
	}
	out.Changes = capabilityChanges(previous, out.Source, manualFromModel(model))
	return out, tx.Commit()
}

func (s *Service) ApplyCapabilities(ctx context.Context, admin int64, key, modelID string, input CapabilityApplyInput) (MutationResult[ModelReceipt], error) {
	var out MutationResult[ModelReceipt]
	if !db.ValidateOpaqueID(modelID, "imdl_") || !db.ValidateOpaqueID(input.SnapshotID, "ics_") || !input.Confirm {
		return out, ErrInvalid
	}
	expected, err := decimalRevision(input.ExpectedRevision, false)
	if err != nil {
		return out, err
	}
	now, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return out, err
	}
	d, err := s.beginReplay(ctx, tx, "admin", admin, key, http.MethodPost, adminPrefix+"/models/"+modelID+"/capabilities/apply", input, now)
	if err != nil {
		return out, err
	}
	if d.Kind == idempotency.Replay {
		if json.Unmarshal(d.ResponseBody, &out.Value) != nil {
			return out, ErrInvariant
		}
		out.Replayed = true
		return out, tx.Commit()
	}
	model, err := modelTx(ctx, tx, modelID, 0)
	if err != nil {
		return out, err
	}
	if model.revision != expected || expected == math.MaxInt64 {
		return out, ErrConflict
	}
	candidate, metadata, err := candidateTx(ctx, tx, modelID, input.SnapshotID, now)
	if err != nil {
		return out, err
	}
	profile, err := profileTx(ctx, tx, model.controlID)
	if err != nil || profile.Profile == nil {
		return out, ErrConflict
	}
	var snapshotProfileRevision int64
	if err = tx.QueryRowContext(ctx, `SELECT profile_revision FROM image_capability_snapshots WHERE id=? AND control_id=?`, input.SnapshotID, model.controlID).Scan(&snapshotProfileRevision); err != nil {
		return out, err
	}
	if profile.Revision != strconv.FormatInt(snapshotProfileRevision, 10) {
		return out, ErrConflict
	}
	manual := manualFromModel(model)
	compiled, err := CompileCapability(*profile.Profile, metadata, manual)
	if err != nil {
		return out, err
	}
	if compiled.Readiness != "ready" && model.input.Enabled {
		return out, ErrConflict
	}
	rules := make([]ParameterRule, 0, len(compiled.Parameters))
	for _, field := range compiled.Parameters {
		rules = append(rules, field.Rule)
	}
	if compiled.Readiness == "ready" {
		if err = validateParameters(rules, model.input.Combinations); err != nil {
			return out, ErrConflict
		}
	}
	effectiveSize := compiled.Size
	if model.manualPolicy.Size != nil {
		if !sizeWithinSource(compiled.Size, model.manualPolicy.Size) {
			return out, ErrConflict
		}
		effectiveSize = model.manualPolicy.Size
	}
	next := expected + 1
	parameters, _ := json.Marshal(rules)
	combinations, _ := json.Marshal(model.input.Combinations)
	mapping, _ := json.Marshal(model.input.Mapping)
	modelType := compiled.CatalogType
	if model.manualPolicy.CatalogType != "" {
		modelType = model.manualPolicy.CatalogType
	}
	effective, _ := json.Marshal(effectivePolicy{rules, model.input.Combinations, model.input.Mapping, effectiveSize, modelType})
	manualRaw, _ := json.Marshal(model.manualPolicy)
	sourceRaw, _ := json.Marshal(candidate.Source)
	if len(effective) > 262144 || len(manualRaw) > 262144 || len(sourceRaw) > 262144 {
		return out, ErrInvalid
	}
	var candidateHash []byte
	if err = tx.QueryRowContext(ctx, `SELECT candidate_hash FROM image_capability_candidates WHERE snapshot_id=? AND model_id=?`, candidate.SnapshotID, modelID).Scan(&candidateHash); err != nil || len(candidateHash) != 32 {
		return out, ErrInvariant
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO image_model_revisions
	 (model_id,revision,display_name,description,enabled,parameters_json,combinations_json,mapping_json,paper_price_mag,brush_price_mag,actor_user_id,created_at)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, modelID, next, model.input.DisplayName, model.input.Description, model.input.Enabled,
		string(parameters), string(combinations), string(mapping), encodeAmount(model.payment.Paper), encodeAmount(model.payment.Brush), admin, now)
	if err != nil {
		return out, err
	}
	readiness := compiled.Readiness
	_, err = tx.ExecContext(ctx, `INSERT INTO image_model_capability_revisions
	 (model_id,revision,schema_version,readiness,source_json,manual_json,effective_json,candidate_hash,created_at)
	 VALUES(?,?,?,?,?,?,?,?,?)`, modelID, next, 2, readiness, string(sourceRaw), string(manualRaw), string(effective), candidateHash, now)
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO image_model_revision_policies(model_id,model_revision,capability_revision,pricing_revision)
	 VALUES(?,?,?,?)`, modelID, next, next, model.pricingRevision)
	if err != nil {
		return out, err
	}
	if err = requireOne(tx.ExecContext(ctx, `UPDATE image_activity_models SET current_revision=? WHERE id=? AND current_revision=?`, next, modelID, expected)); err != nil {
		return out, err
	}
	out.Value = ModelReceipt{ID: modelID, Revision: strconv.FormatInt(next, 10), CapabilityRevision: strconv.FormatInt(next, 10), PricingRevision: strconv.FormatInt(model.pricingRevision, 10)}
	if err = finishReplay(ctx, tx, d, out.Value); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
