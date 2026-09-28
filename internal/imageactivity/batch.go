package imageactivity

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type BatchModelItem struct {
	ID    string     `json:"id"`
	Input ModelInput `json:"input"`
}

type BatchModelInput struct {
	Models []BatchModelItem `json:"models"`
}

type BatchModelResult struct {
	Applied  bool           `json:"applied"`
	Receipts []ModelReceipt `json:"receipts"`
	Issues   []CheckIssue   `json:"issues"`
}

func batchIssue(id, path, code, message string) CheckIssue {
	return CheckIssue{ModelID: id, FieldPath: path, Code: code, SafeMessage: message}
}

// PutModels validates every row against one database snapshot, then writes all
// revisions in the same transaction. Any validation or write failure leaves
// every model and queued task unchanged.
func (s *Service) PutModels(ctx context.Context, admin int64, key string, input BatchModelInput) (MutationResult[BatchModelResult], error) {
	var out MutationResult[BatchModelResult]
	if len(input.Models) == 0 || len(input.Models) > 50 {
		return out, ErrInvalid
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
	d, err := s.beginReplay(ctx, tx, "admin", admin, key, http.MethodPost, adminPrefix+"/models/batch", input, now)
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
	upstream, err := currentUpstreamTx(ctx, tx)
	if err != nil {
		return out, err
	}
	type prepared struct {
		id              string
		input           ModelInput
		expected        int64
		parametersRaw   string
		combinationsRaw string
		mappingRaw      string
		paperMag        []byte
		brushMag        []byte
		policy          preparedModelPolicy
	}
	validated := make([]prepared, 0, len(input.Models))
	out.Value = BatchModelResult{Receipts: []ModelReceipt{}, Issues: []CheckIssue{}}
	seen := map[string]bool{}
	var configured int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM image_activity_models WHERE control_id=? AND current_revision IS NOT NULL", upstream.controlID).Scan(&configured); err != nil {
		return out, err
	}
	for _, item := range input.Models {
		if !db.ValidateOpaqueID(item.ID, "imdl_") {
			out.Value.Issues = append(out.Value.Issues, batchIssue("", "id", "invalid_model", "Choose a model from the current catalog."))
			continue
		}
		if seen[item.ID] {
			out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "id", "duplicate_model", "Choose each model only once."))
			continue
		}
		seen[item.ID] = true
		old, e := modelTx(ctx, tx, item.ID, 0)
		if errors.Is(e, ErrNotFound) {
			out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "id", "stale_catalog", "Reload the model from the current catalog."))
			continue
		}
		if e != nil {
			return out, e
		}
		if old.controlID != upstream.controlID {
			out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "id", "stale_catalog", "Reload the model from the current catalog."))
			continue
		}
		var draft ModelInput
		var validation error
		if item.Input.automatic {
			draft, validation = automaticModelInput(old, item.Input, true)
		} else {
			draft, validation = normalizeModel(item.Input)
		}
		if validation != nil {
			var policyErr *policyValidationError
			if errors.As(validation, &policyErr) {
				out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, policyErr.path, policyErr.code, policyErr.message))
			} else if _, revisionErr := decimalRevision(item.Input.ExpectedRevision, true); revisionErr != nil {
				out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "input.expected_revision", "invalid_revision", "Use the revision from the selected model."))
			} else if _, priceErr := parsePayment(item.Input.Price, 1); priceErr != nil {
				out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "input.price", "invalid_price", "Review the model's base price."))
			} else {
				out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "input", "invalid_rule", "Review this model's name, rules, and mapping."))
			}
			continue
		}
		startIssues := len(out.Value.Issues)
		expected, _ := decimalRevision(draft.ExpectedRevision, true)
		if expected != old.revision || expected == math.MaxInt64 {
			out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "input.expected_revision", "revision_conflict", "Reload this model before saving."))
		}
		pricing := legacyPricing(draft.Price)
		if draft.Pricing != nil {
			pricing = *draft.Pricing
		}
		if pricing.Default != draft.Price || ValidatePricingPolicy(pricing) != nil {
			out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "input.pricing", "invalid_price", "Review the model's size prices."))
		}
		if draft.SizeCapability != nil && draft.SizeCapability.Validate() != nil {
			out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "input.size_capability", "invalid_size", "Review the linked size capability."))
		}
		if !draft.automatic && draft.Enabled && old.readiness != "legacy" && old.readiness != "ready" && !draft.CapabilityConfirmed {
			out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "input.capability_confirmed", "capability_unconfirmed", "Confirm supported parameters before enabling this model."))
		}
		if draft.CatalogType != "" && draft.CatalogType != "image" && draft.CatalogType != "unknown" && draft.CatalogType != "other" {
			out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "input.catalog_type", "invalid_type", "Choose a supported catalog type."))
		}
		draft.Mapping = normalizeMapping(draft.Mapping)
		if draft.Combinations == nil {
			draft.Combinations = []CombinationRule{}
		}
		mapping := draft.Mapping
		if mapping.ModelPointer == "" {
			mapping = upstream.adapter.Submit.Mapping
		}
		for _, rule := range draft.Parameters {
			if rule.Supported && mapping.Parameters[rule.Key] == "" {
				out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "input.mapping.parameters."+string(rule.Key), "missing_mapping", "A supported parameter needs a destination mapping."))
			}
		}
		if len(out.Value.Issues) != startIssues {
			continue
		}
		policy, policyErr := prepareModelPolicyTx(ctx, tx, item.ID, old, draft)
		if policyErr != nil {
			var validationErr *policyValidationError
			if errors.As(policyErr, &validationErr) {
				out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, validationErr.path, validationErr.code, validationErr.message))
				continue
			}
			return out, policyErr
		}
		parameters, parametersErr := json.Marshal(draft.Parameters)
		combinations, combinationsErr := json.Marshal(draft.Combinations)
		mappingRaw, mappingErr := json.Marshal(draft.Mapping)
		if parametersErr != nil || combinationsErr != nil || mappingErr != nil {
			out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "input", "invalid_rule", "Review this model's rules and mapping."))
			continue
		}
		payment, paymentErr := parsePayment(draft.Price, 1)
		if paymentErr != nil {
			out.Value.Issues = append(out.Value.Issues, batchIssue(item.ID, "input.price", "invalid_price", "Review the model's base price."))
			continue
		}
		validated = append(validated, prepared{
			id: item.ID, input: draft, expected: expected,
			parametersRaw: string(parameters), combinationsRaw: string(combinations), mappingRaw: string(mappingRaw),
			paperMag: encodeAmount(payment.Paper), brushMag: encodeAmount(payment.Brush), policy: policy,
		})
	}
	remaining := 256 - configured
	for _, item := range validated {
		if item.expected == 0 {
			if remaining <= 0 {
				out.Value.Issues = append(out.Value.Issues, batchIssue(item.id, "id", "capacity", "The connection has reached its configured model limit."))
			} else {
				remaining--
			}
		}
	}
	if len(out.Value.Issues) > 0 {
		return out, nil
	}
	for _, item := range input.Models {
		if item.Input.automatic {
			if _, err = ensureFixedUpstreamTx(ctx, tx, upstream, now); err != nil {
				return out, err
			}
			break
		}
	}
	out.Value.Applied = true
	out.Value.Receipts = make([]ModelReceipt, 0, len(validated))
	var cancelled []string
	for _, item := range validated {
		next := item.expected + 1
		_, err = tx.ExecContext(ctx, `INSERT INTO image_model_revisions(model_id,revision,display_name,description,enabled,parameters_json,combinations_json,mapping_json,paper_price_mag,brush_price_mag,actor_user_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, item.id, next, item.input.DisplayName, item.input.Description, item.input.Enabled, item.parametersRaw, item.combinationsRaw, item.mappingRaw, item.paperMag, item.brushMag, admin, now)
		if err != nil {
			return out, err
		}
		if err = storePreparedModelPolicyTx(ctx, tx, item.id, next, item.policy, now); err != nil {
			return out, err
		}
		var current any = item.expected
		if item.expected == 0 {
			current = nil
		}
		if err = requireOne(tx.ExecContext(ctx, "UPDATE image_activity_models SET current_revision=? WHERE id=? AND current_revision IS ?", next, item.id, current)); err != nil {
			return out, err
		}
		if !item.input.Enabled {
			stopped, e := s.cancelQueuedTx(ctx, tx, "model_id=?", []any{item.id}, "model_unavailable", now)
			if e != nil {
				return out, e
			}
			cancelled = append(cancelled, stopped...)
		}
		out.Value.Receipts = append(out.Value.Receipts, ModelReceipt{ID: item.id, Revision: strconv.FormatInt(next, 10), CapabilityRevision: strconv.FormatInt(next, 10), PricingRevision: strconv.FormatInt(next, 10)})
	}
	if err = finishReplay(ctx, tx, d, out.Value); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	for _, task := range cancelled {
		s.memory.purge(task)
	}
	s.signal()
	return out, nil
}
