package imageactivity

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type CheckInput struct {
	ModelID    string      `json:"model_id"`
	Draft      ModelInput  `json:"draft"`
	Parameters SubmitInput `json:"parameters"`
}

type CheckIssue struct {
	ModelID     string `json:"model_id"`
	FieldPath   string `json:"field_path"`
	Code        string `json:"code"`
	SafeMessage string `json:"safe_message"`
}

type CheckResult struct {
	Valid               bool                 `json:"valid"`
	Issues              []CheckIssue         `json:"issues"`
	EffectiveParameters map[ParameterKey]any `json:"effective_parameters,omitempty"`
	EffectiveSelection  *ResolvedSize        `json:"effective_selection,omitempty"`
	Quote               *PriceQuote          `json:"quote,omitempty"`
}

// CheckModel evaluates a draft against saved local configuration only. It
// neither reserves currency nor performs an upstream request.
func (s *Service) CheckModel(ctx context.Context, admin int64, input CheckInput) (CheckResult, error) {
	out := CheckResult{Issues: []CheckIssue{}}
	if !db.ValidateOpaqueID(input.ModelID, "imdl_") {
		return out, ErrInvalid
	}
	issue := func(path, code, message string) (CheckResult, error) {
		out.Issues = append(out.Issues, CheckIssue{ModelID: input.ModelID, FieldPath: path, Code: code, SafeMessage: message})
		return out, nil
	}
	draft, err := normalizeModel(input.Draft)
	if err != nil {
		return issue("draft", "invalid_rule", "Review the model rules, mapping, and base price.")
	}
	pricing := legacyPricing(draft.Price)
	if draft.Pricing != nil {
		pricing = *draft.Pricing
	}
	if pricing.Default != draft.Price || ValidatePricingPolicy(pricing) != nil {
		return issue("draft.pricing", "invalid_price", "Review the size pricing policy.")
	}
	if draft.SizeCapability != nil && draft.SizeCapability.Validate() != nil {
		return issue("draft.size_capability", "invalid_size", "Review the linked size capability.")
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return out, err
	}
	upstream, err := currentUpstreamTx(ctx, tx)
	if err != nil {
		return out, err
	}
	model, err := modelTx(ctx, tx, input.ModelID, 0)
	if err != nil {
		return out, err
	}
	if model.controlID != upstream.controlID {
		return out, ErrConflict
	}
	if draft.Enabled && !draft.CapabilityConfirmed && model.readiness != "legacy" && model.readiness != "ready" {
		return issue("draft.capability_confirmed", "capability_unconfirmed", "Confirm supported parameters before enabling this model.")
	}
	mapping := draft.Mapping
	if mapping.ModelPointer == "" {
		mapping = upstream.adapter.Submit.Mapping
	}
	for _, rule := range draft.Parameters {
		if rule.Supported && mapping.Parameters[rule.Key] == "" {
			return issue("draft.mapping.parameters."+string(rule.Key), "missing_mapping", "A supported parameter needs a destination mapping.")
		}
	}
	model.input = draft
	model.size = draft.SizeCapability
	model.pricing = pricing
	parameters := normalizeSubmitText(input.Parameters)
	parameters.ModelID = input.ModelID
	if model.revision == 0 {
		parameters.ExpectedModelRevision = "1"
	} else {
		parameters.ExpectedModelRevision = draft.ExpectedRevision
	}
	parameters, rules, err := linkedSubmission(parameters, model)
	if err != nil {
		return issue("parameters.size", "invalid_size", "Choose a supported linked size option.")
	}
	params, n, err := normalizeSubmit(parameters, rules, draft.Combinations)
	if err != nil {
		return issue("parameters", "invalid_parameters", "Review prompt, defaults, ranges, and parameter combinations.")
	}
	selection, quote, err := selectedPrice(model, params, n)
	if err != nil {
		return issue("draft.pricing", "unavailable_price", "No price is available for this size or count.")
	}
	if _, err = buildRequest(model.upstreamID, params, mapping); err != nil {
		return issue("draft.mapping", "invalid_mapping", "The effective parameters cannot be mapped to a request.")
	}
	// Round-trip the bounded projection so no accidental private adapter data
	// enters the response when this API grows.
	if _, err = json.Marshal(params); err != nil {
		return out, ErrInvariant
	}
	out.Valid, out.EffectiveParameters, out.EffectiveSelection, out.Quote = true, params, &selection, &quote
	return out, tx.Commit()
}
