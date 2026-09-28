package imageactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
)

type effectivePolicy struct {
	Parameters   []ParameterRule   `json:"parameters"`
	Combinations []CombinationRule `json:"combinations"`
	Mapping      Mapping           `json:"mapping"`
	Size         *SizeCapability   `json:"size,omitempty"`
	CatalogType  string            `json:"catalog_type,omitempty"`
}

func loadModelPolicyTx(ctx context.Context, tx *sql.Tx, model *modelSnapshot) error {
	var effective, manual, sourceRaw, fallback, tiers, sizes, readiness string
	var schema int
	var paper, brush []byte
	err := tx.QueryRowContext(ctx, `SELECT p.capability_revision,p.pricing_revision,c.readiness,c.schema_version,c.source_json,c.effective_json,c.manual_json,
	 q.default_paper_mag,q.default_brush_mag,q.fallback,q.tiers_json,q.sizes_json
	 FROM image_model_revision_policies p
	 JOIN image_model_capability_revisions c ON c.model_id=p.model_id AND c.revision=p.capability_revision
	 JOIN image_model_pricing_revisions q ON q.model_id=p.model_id AND q.revision=p.pricing_revision
	 WHERE p.model_id=? AND p.model_revision=?`, model.id, model.revision).
		Scan(&model.capabilityRevision, &model.pricingRevision, &readiness, &schema, &sourceRaw, &effective, &manual, &paper, &brush, &fallback, &tiers, &sizes)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvariant
	}
	if err != nil {
		return err
	}
	model.readiness = readiness
	amount, err := decodeAmount(paper)
	if err != nil {
		return err
	}
	model.pricing.Default.Paper = whole(amount)
	amount, err = decodeAmount(brush)
	if err != nil {
		return err
	}
	model.pricing.Default.Brush = whole(amount)
	model.pricing.Fallback = fallback
	if json.Unmarshal([]byte(tiers), &model.pricing.Tiers) != nil || json.Unmarshal([]byte(sizes), &model.pricing.Sizes) != nil || ValidatePricingPolicy(model.pricing) != nil {
		return ErrInvariant
	}
	if model.pricing.Default != model.input.Price {
		return ErrInvariant
	}
	var compiled effectivePolicy
	if json.Unmarshal([]byte(effective), &compiled) != nil {
		return ErrInvariant
	}
	var savedManual effectivePolicy
	if json.Unmarshal([]byte(manual), &savedManual) != nil {
		return ErrInvariant
	}
	var savedSource CompiledCapability
	if json.Unmarshal([]byte(sourceRaw), &savedSource) != nil {
		return ErrInvariant
	}
	model.manualPolicy = savedManual
	model.parameterCapabilities = parameterCapabilityView(savedSource, savedManual, model.input.Parameters, schema == 1 || readiness == "legacy")
	for _, rule := range savedManual.Parameters {
		support := CapabilityUnsupported
		if rule.Supported {
			support = CapabilitySupported
		}
		model.manual = append(model.manual, ManualCapability{Rule: rule, Support: support})
	}
	if compiled.Size != nil && compiled.Size.Validate() != nil {
		return ErrInvariant
	}
	if readiness == "legacy" && compiled.Size != nil || readiness != "legacy" && readiness != "ready" && readiness != "pending" {
		return ErrInvariant
	}
	model.size = compiled.Size
	model.catalogType = compiled.CatalogType
	if model.catalogType == "" && readiness == "legacy" {
		model.catalogType = "image"
	}
	if model.catalogType != "" && model.catalogType != "image" && model.catalogType != "unknown" && model.catalogType != "other" {
		return ErrInvariant
	}
	return nil
}

// Read accepted provenance only from the accepted source and explicit manual
// revision. A generated legacy manual mirror is not evidence of an override.
func parameterCapabilityView(source CompiledCapability, manual effectivePolicy, rules []ParameterRule, legacy bool) []AdminParameterCapability {
	out := make([]AdminParameterCapability, 0, len(rules))
	old := make(map[ParameterKey]CapabilityField, len(source.Parameters))
	for _, field := range source.Parameters {
		old[field.Rule.Key] = field
	}
	overrides := make(map[ParameterKey]ParameterRule, len(manual.Parameters))
	for _, rule := range manual.Parameters {
		overrides[rule.Key] = rule
	}
	for _, rule := range rules {
		status := AdminParameterCapability{Key: rule.Key, Source: "unknown", Support: CapabilityUnsupported}
		if rule.Supported {
			status.Support = CapabilitySupported
		}
		if legacy {
			status.Source = "legacy"
			out = append(out, status)
			continue
		}
		field, sourceExists := old[rule.Key]
		validSource := sourceExists && (field.Source == "discovered" || field.Source == "profile")
		manualRule, hasManual := overrides[rule.Key]
		manualMatches := hasManual && reflect.DeepEqual(manualRule, rule)
		if validSource && reflect.DeepEqual(field.Rule, rule) && (!manualMatches || field.Support == status.Support) {
			status.Source = field.Source
			status.Support = field.Support
		} else if manualMatches {
			status.Source = "manual"
			status.Overridden = validSource
			if validSource {
				status.Conflict = field.Support == CapabilityUnsupported && rule.Supported ||
					field.Support == CapabilitySupported && rule.Supported && !narrowerThanSource(field.Rule, rule)
			}
		}
		out = append(out, status)
	}
	return out
}

type preparedModelPolicy struct {
	schema        int
	readiness     string
	sourceRaw     string
	manualRaw     string
	effectiveRaw  string
	candidateHash []byte
	fallback      string
	tiersRaw      string
	sizesRaw      string
	paperMag      []byte
	brushMag      []byte
}

type policyValidationError struct {
	path, code, message string
	cause               error
}

func (e *policyValidationError) Error() string { return e.cause.Error() }
func (e *policyValidationError) Unwrap() error { return e.cause }

func invalidPolicy(path, code, message string) error {
	return &policyValidationError{path: path, code: code, message: message, cause: ErrInvalid}
}

func conflictPolicy(path, message string) error {
	return &policyValidationError{path: path, code: "capability_conflict", message: message, cause: ErrConflict}
}

// prepareModelPolicyTx performs every policy check and source read before the
// caller writes a model revision. It does not mutate the database.
func prepareModelPolicyTx(ctx context.Context, tx *sql.Tx, modelID string, previous modelSnapshot, input ModelInput) (preparedModelPolicy, error) {
	if input.automaticSource != nil {
		return prepareAutomaticPolicy(input, previous.metadata)
	}
	var prepared preparedModelPolicy
	pricing := legacyPricing(input.Price)
	if input.Pricing != nil {
		pricing = *input.Pricing
	}
	if pricing.Default != input.Price || ValidatePricingPolicy(pricing) != nil {
		return prepared, invalidPolicy("input.pricing", "invalid_price", "Review the model's default and size prices.")
	}
	readiness, schema := "pending", 2
	if input.SizeCapability != nil {
		if input.SizeCapability.Validate() != nil {
			return prepared, invalidPolicy("input.size_capability", "invalid_size", "Review the linked size capability.")
		}
	}
	if previous.readiness == "legacy" && input.SizeCapability == nil {
		readiness, schema = "legacy", 1
	} else if input.CapabilityConfirmed || previous.readiness == "ready" {
		readiness = "ready"
	}
	if input.Enabled && readiness == "pending" {
		return prepared, invalidPolicy("input.capability_confirmed", "capability_unconfirmed", "Confirm supported parameters before enabling this model.")
	}
	modelType := input.CatalogType
	if modelType == "" {
		modelType = previous.catalogType
	}
	if modelType == "" {
		modelType = "unknown"
	}
	if modelType != "image" && modelType != "unknown" && modelType != "other" {
		return prepared, invalidPolicy("input.catalog_type", "invalid_type", "Choose a supported catalog type.")
	}
	effective, err := json.Marshal(effectivePolicy{input.Parameters, input.Combinations, input.Mapping, input.SizeCapability, modelType})
	if err != nil || len(effective) > 262144 {
		return prepared, invalidPolicy("input.parameters", "invalid_rule", "Review this model's effective rules.")
	}
	sourceRaw := "{}"
	var candidateHash []byte
	manual := effectivePolicy{
		Parameters: input.Parameters, Combinations: input.Combinations, Mapping: input.Mapping,
		Size: input.SizeCapability, CatalogType: modelType,
	}
	if previous.revision > 0 {
		err = tx.QueryRowContext(ctx, `SELECT source_json,candidate_hash FROM image_model_capability_revisions WHERE model_id=? AND revision=?`, modelID, previous.capabilityRevision).Scan(&sourceRaw, &candidateHash)
		if err != nil {
			return prepared, err
		}
		var source CompiledCapability
		if json.Unmarshal([]byte(sourceRaw), &source) != nil {
			return prepared, ErrInvariant
		}
		manual = previous.manualPolicy
		manual.Combinations, manual.Mapping = input.Combinations, input.Mapping
		oldRules := make(map[ParameterKey]ParameterRule, len(previous.input.Parameters))
		for _, rule := range previous.input.Parameters {
			oldRules[rule.Key] = rule
		}
		sourceRules := make(map[ParameterKey]CapabilityField, len(source.Parameters))
		for _, field := range source.Parameters {
			sourceRules[field.Rule.Key] = field
		}
		manualRules := make(map[ParameterKey]ParameterRule, len(manual.Parameters))
		for _, rule := range manual.Parameters {
			manualRules[rule.Key] = rule
		}
		requestedRules := make(map[ParameterKey]bool, len(input.Parameters))
		for _, rule := range input.Parameters {
			requestedRules[rule.Key] = true
			if old, exists := oldRules[rule.Key]; exists && reflect.DeepEqual(old, rule) {
				continue
			}
			if sourceField, exists := sourceRules[rule.Key]; exists {
				if reflect.DeepEqual(sourceField.Rule, rule) {
					delete(manualRules, rule.Key)
					continue
				}
				if sourceField.Support == CapabilityUnsupported && rule.Supported ||
					sourceField.Support == CapabilitySupported && rule.Supported && !narrowerThanSource(sourceField.Rule, rule) {
					return prepared, conflictPolicy("input.parameters."+string(rule.Key), "This manual rule conflicts with the accepted source capability.")
				}
			}
			manualRules[rule.Key] = rule
		}
		for key := range oldRules {
			if !requestedRules[key] && sourceRules[key].Rule.Key != "" {
				// A discovered field must be explicitly disabled, not silently omitted.
				return prepared, conflictPolicy("input.parameters."+string(key), "A discovered field must be disabled explicitly, not omitted.")
			}
			if !requestedRules[key] {
				delete(manualRules, key)
			}
		}
		ordered := make([]ParameterRule, 0, len(manualRules))
		for _, rule := range manual.Parameters {
			if current, exists := manualRules[rule.Key]; exists {
				ordered = append(ordered, current)
				delete(manualRules, rule.Key)
			}
		}
		for _, rule := range input.Parameters {
			if current, exists := manualRules[rule.Key]; exists {
				ordered = append(ordered, current)
				delete(manualRules, rule.Key)
			}
		}
		manual.Parameters = ordered
		if !reflect.DeepEqual(previous.size, input.SizeCapability) {
			if reflect.DeepEqual(source.Size, input.SizeCapability) {
				manual.Size = nil
			} else {
				if source.Size != nil && !sizeWithinSource(source.Size, input.SizeCapability) {
					return prepared, conflictPolicy("input.size_capability", "The manual size choices conflict with the accepted source capability.")
				}
				manual.Size = input.SizeCapability
			}
		}
		if previous.catalogType != modelType {
			if source.CatalogType == modelType {
				manual.CatalogType = ""
			} else {
				manual.CatalogType = modelType
			}
		}
	}
	manualRaw, err := json.Marshal(manual)
	if err != nil || len(manualRaw) > 262144 {
		return prepared, invalidPolicy("input.parameters", "invalid_rule", "Review this model's manual rules.")
	}
	tiers, err := json.Marshal(pricing.Tiers)
	if err != nil || len(tiers) > 16384 {
		return prepared, invalidPolicy("input.pricing", "invalid_price", "Review the model's tier prices.")
	}
	sizes, err := json.Marshal(pricing.Sizes)
	if err != nil || len(sizes) > 262144 {
		return prepared, invalidPolicy("input.pricing", "invalid_price", "Review the model's exact-size prices.")
	}
	unit, err := parsePayment(pricing.Default, 1)
	if err != nil {
		return prepared, invalidPolicy("input.pricing", "invalid_price", "Review the model's default price.")
	}
	return preparedModelPolicy{
		schema: schema, readiness: readiness, sourceRaw: sourceRaw,
		manualRaw: string(manualRaw), effectiveRaw: string(effective), candidateHash: candidateHash, fallback: pricing.Fallback,
		tiersRaw: string(tiers), sizesRaw: string(sizes),
		paperMag: encodeAmount(unit.Paper), brushMag: encodeAmount(unit.Brush),
	}, nil
}

func storePreparedModelPolicyTx(ctx context.Context, tx *sql.Tx, modelID string, revision int64, prepared preparedModelPolicy, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO image_model_capability_revisions
	 (model_id,revision,schema_version,readiness,source_json,manual_json,effective_json,candidate_hash,created_at)
	 VALUES(?,?,?,?,?,?,?,?,?)`, modelID, revision, prepared.schema, prepared.readiness, prepared.sourceRaw, prepared.manualRaw, prepared.effectiveRaw, prepared.candidateHash, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO image_model_pricing_revisions
	 (model_id,revision,default_paper_mag,default_brush_mag,fallback,tiers_json,sizes_json,created_at)
	 VALUES(?,?,?,?,?,?,?,?)`, modelID, revision, prepared.paperMag, prepared.brushMag, prepared.fallback, prepared.tiersRaw, prepared.sizesRaw, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO image_model_revision_policies(model_id,model_revision,capability_revision,pricing_revision)
	 VALUES(?,?,?,?)`, modelID, revision, revision, revision)
	return err
}

func storeModelPolicyTx(ctx context.Context, tx *sql.Tx, modelID string, revision int64, previous modelSnapshot, input ModelInput, now int64) error {
	prepared, err := prepareModelPolicyTx(ctx, tx, modelID, previous, input)
	if err != nil {
		return err
	}
	return storePreparedModelPolicyTx(ctx, tx, modelID, revision, prepared, now)
}

// A manual size override may remove choices or narrow an axis. It cannot add
// choices, relabel price tiers, or relax a newly accepted source constraint.
func sizeWithinSource(source, manual *SizeCapability) bool {
	if manual == nil || manual.Validate() != nil {
		return false
	}
	if source == nil {
		return true
	}
	if source.Validate() != nil || source.Mode != manual.Mode || manual.Auto && !source.Auto {
		return false
	}
	if source.Mode == WidthHeight {
		axisWithin := func(old, next *DimensionRange) bool {
			return old != nil && next != nil &&
				next.Minimum >= old.Minimum && next.Maximum <= old.Maximum &&
				(next.Minimum-old.Minimum)%old.Step == 0 && next.Step%old.Step == 0
		}
		if !axisWithin(source.Width, manual.Width) || !axisWithin(source.Height, manual.Height) ||
			source.MaxPixels > 0 && (manual.MaxPixels == 0 || manual.MaxPixels > source.MaxPixels) {
			return false
		}
	}
	if len(source.Combinations) == 0 {
		return source.Mode == WidthHeight || len(manual.Combinations) == 0
	}
	if source.Mode == WidthHeight && len(manual.Combinations) == 0 {
		return false
	}
	allowed := make(map[SizeCombination]bool, len(source.Combinations))
	for _, row := range source.Combinations {
		allowed[row] = true
	}
	for _, row := range manual.Combinations {
		if !allowed[row] {
			return false
		}
	}
	return true
}
