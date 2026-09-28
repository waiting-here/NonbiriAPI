package imageactivity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// ConnectionInput contains the only writable image-service connection fields.
type ConnectionInput struct {
	ExpectedRevision string      `json:"expected_revision"`
	BaseURL          string      `json:"base_url"`
	Secret           SecretInput `json:"secret"`
}

// ModelSettingsInput leaves capability facts under the upstream's authority.
type ModelSettingsInput struct {
	ExpectedRevision string         `json:"expected_revision"`
	Enabled          bool           `json:"enabled"`
	Price            Price          `json:"price"`
	Pricing          *PricingPolicy `json:"pricing,omitempty"`
}

func (input *ModelSettingsInput) UnmarshalJSON(raw []byte) error {
	type settings ModelSettingsInput
	var decoded settings
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ErrInvalid
	}
	for _, key := range []string{"expected_revision", "enabled", "price"} {
		if value, exists := fields[key]; !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalid
		}
	}
	*input = ModelSettingsInput(decoded)
	return nil
}

type ModelSettingsItem struct {
	ID    string             `json:"id"`
	Input ModelSettingsInput `json:"input"`
}

type ModelSettingsBatch struct {
	Models []ModelSettingsItem `json:"models"`
}

type ModelSettingsCheck struct {
	ModelID    string             `json:"model_id"`
	Draft      ModelSettingsInput `json:"draft"`
	Parameters SubmitInput        `json:"parameters"`
}

func fixedMapping() Mapping {
	parameters := map[ParameterKey]string{}
	for _, key := range []ParameterKey{Prompt, NegativePrompt, N, Size, AspectRatio, Resolution, Seed, Steps, Guidance, Quality} {
		parameters[key] = "/" + string(key)
	}
	parameters[Guidance] = "/cfg_scale"
	return Mapping{ModelPointer: "/model", Parameters: parameters, Constants: []Constant{
		{Pointer: "/async", Value: json.RawMessage("true")},
		{Pointer: "/response_format", Value: json.RawMessage(`"b64_json"`)},
	}}
}

func fixedAdapter(base string) Adapter {
	prefix := "v1/"
	if parsed, err := url.Parse(base); err == nil && strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/v1") {
		prefix = ""
	}
	metadata, taskID, state, base64 := "", "/task_id", "/status", "/b64_json"
	return normalizeAdapter(Adapter{
		Discovery: DiscoveryAdapter{Method: http.MethodGet, Path: prefix + "models", ItemsPointer: "/data", IDPointer: "/id", MetadataPointer: &metadata},
		Submit:    SubmitAdapter{Method: http.MethodPost, Path: prefix + "images/generations", Mapping: fixedMapping(), Receipt: &ReceiptAdapter{IndicatorPointer: "/async", IndicatorValue: json.RawMessage("true")}},
		Poll:      &PollAdapter{Method: http.MethodGet, Path: prefix + "images/jobs/{task_id}"},
		Response:  ResponseAdapter{TaskIDPointer: &taskID, StatePointer: &state, WorkingStates: []string{"queued", "running"}, SuccessStates: []string{"done"}, FailureStates: []string{"error"}, ImagesPointer: "/data", Base64Pointer: &base64},
	})
}

func isFixedAdapter(snapshot upstreamSnapshot) bool {
	return reflect.DeepEqual(snapshot.adapter, fixedAdapter(snapshot.baseURL))
}

// PutConnection keeps existing internal budgets and physical endpoint identity.
// Its immutable replacement cannot change any accepted task's configuration.
func (s *Service) PutConnection(ctx context.Context, admin int64, key string, input ConnectionInput) (MutationResult[UpstreamReceipt], error) {
	settings := UpstreamInput{ExpectedRevision: input.ExpectedRevision, BaseURL: input.BaseURL, Secret: input.Secret, RPM: 100, Concurrency: 2, PerUserLimit: 1, GlobalLimit: 100, QueueTimeoutSeconds: 1800, ExecutionTimeoutSeconds: 1800, MemoryBudgetMiB: 512, ImageOrigins: []string{}, Adapter: fixedAdapter(input.BaseURL), fixed: true}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return MutationResult[UpstreamReceipt]{}, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return MutationResult[UpstreamReceipt]{}, err
	}
	previous, err := currentUpstreamTx(ctx, tx)
	if err != nil && !errors.Is(err, ErrUnavailable) {
		return MutationResult[UpstreamReceipt]{}, err
	}
	if err == nil {
		settings.RPM, settings.Concurrency = previous.rpm, previous.concurrency
		settings.PerUserLimit, settings.GlobalLimit = previous.userLimit, previous.globalLimit
		settings.QueueTimeoutSeconds, settings.ExecutionTimeoutSeconds, settings.MemoryBudgetMiB = previous.queueSeconds, previous.executionSeconds, previous.memoryMiB
	}
	if err = tx.Commit(); err != nil {
		return MutationResult[UpstreamReceipt]{}, err
	}
	return s.PutUpstream(ctx, admin, key, settings)
}

// A refresh explicitly opts the current connection into the fixed protocol.
// Appending a revision preserves the original base, key, control, and budgets.
func ensureFixedUpstreamTx(ctx context.Context, tx *sql.Tx, previous upstreamSnapshot, now int64) (upstreamSnapshot, error) {
	if isFixedAdapter(previous) && len(previous.origins) == 0 {
		return previous, nil
	}
	if previous.stateRevision == math.MaxInt64 {
		return previous, ErrCapacity
	}
	adapter, _ := json.Marshal(fixedAdapter(previous.baseURL))
	next := previous.stateRevision + 1
	_, err := tx.ExecContext(ctx, `INSERT INTO image_upstream_revisions
	 (revision,control_id,base_url,secret_context,secret_ciphertext,adapter_json,image_origins_json,per_user_limit,global_limit,queue_timeout_seconds,execution_timeout_seconds,memory_budget_mib,actor_user_id,created_at)
	 SELECT ?,control_id,base_url,secret_context,secret_ciphertext,?,'[]',per_user_limit,global_limit,queue_timeout_seconds,execution_timeout_seconds,memory_budget_mib,actor_user_id,?
	 FROM image_upstream_revisions WHERE revision=?`, next, string(adapter), now, previous.revision)
	if err != nil {
		return previous, err
	}
	if err = requireOne(tx.ExecContext(ctx, "UPDATE image_activity_state SET revision=?,upstream_revision=?,updated_at=? WHERE id=1 AND revision=?", next, next, now, previous.stateRevision)); err != nil {
		return previous, err
	}
	updated, err := upstreamTx(ctx, tx, next)
	updated.stateRevision = next
	return updated, err
}

func modelSettingsInput(input ModelSettingsInput) ModelInput {
	return ModelInput{ExpectedRevision: input.ExpectedRevision, Enabled: input.Enabled, Price: input.Price, Pricing: input.Pricing, automatic: true}
}

func (s *Service) PutModelSettings(ctx context.Context, admin int64, id, key string, input ModelSettingsInput) (MutationResult[ModelReceipt], error) {
	return s.PutModel(ctx, admin, id, key, modelSettingsInput(input))
}

func (s *Service) PutModelsSettings(ctx context.Context, admin int64, key string, input ModelSettingsBatch) (MutationResult[BatchModelResult], error) {
	batch := BatchModelInput{Models: make([]BatchModelItem, 0, len(input.Models))}
	for _, item := range input.Models {
		batch.Models = append(batch.Models, BatchModelItem{ID: item.ID, Input: modelSettingsInput(item.Input)})
	}
	return s.PutModels(ctx, admin, key, batch)
}

func automaticModelInput(previous modelSnapshot, requested ModelInput, preserveEnabled bool) (ModelInput, error) {
	capability := compileFixedCapability(previous.metadata)
	input := previous.input
	input.ExpectedRevision, input.Enabled, input.Price, input.Pricing = requested.ExpectedRevision, requested.Enabled, requested.Price, requested.Pricing
	input.automatic = true
	if requested.Pricing == nil && previous.revision > 0 {
		pricing := previous.pricing
		pricing.Default = input.Price
		input.Pricing = &pricing
	}
	input.Mapping = fixedMapping()
	if !capability.recognized && previous.revision > 0 && previous.readiness != "pending" {
		// Missing new metadata cannot manufacture a capability for a new model,
		// nor revoke an existing immutable policy that remains valid locally.
		input.SizeCapability, input.CatalogType = previous.size, previous.catalogType
		return normalizeModel(input)
	}
	input.Parameters, input.Combinations = capability.rules, []CombinationRule{}
	input.SizeCapability, input.CatalogType = capability.compiled.Size, capability.compiled.CatalogType
	input.CapabilityConfirmed = capability.compiled.Readiness == "ready"
	input.automaticSource = &capability.compiled
	if previous.revision == 0 {
		input.DisplayName = fixedDisplayName(previous.upstreamID, capability.name)
		input.Description = ""
	}
	if input.Enabled && !input.CapabilityConfirmed && !(preserveEnabled && previous.input.Enabled) {
		return input, invalidPolicy("input.enabled", "unsupported_metadata", "This model cannot be enabled because its upstream metadata is missing or unsupported. Refresh the catalog or choose another image model.")
	}
	return normalizeModel(input)
}

func prepareAutomaticPolicy(input ModelInput, metadata json.RawMessage) (preparedModelPolicy, error) {
	pricing := legacyPricing(input.Price)
	if input.Pricing != nil {
		pricing = *input.Pricing
	}
	if pricing.Default != input.Price || ValidatePricingPolicy(pricing) != nil {
		return preparedModelPolicy{}, invalidPolicy("input.pricing", "invalid_price", "Review the model's default and size prices.")
	}
	unit, err := parsePayment(pricing.Default, 1)
	if err != nil {
		return preparedModelPolicy{}, err
	}
	source, err := json.Marshal(input.automaticSource)
	if err != nil || len(source) > maxJSON {
		return preparedModelPolicy{}, ErrInvalid
	}
	effective, err := json.Marshal(effectivePolicy{input.Parameters, input.Combinations, input.Mapping, input.SizeCapability, input.CatalogType})
	if err != nil || len(effective) > maxJSON {
		return preparedModelPolicy{}, ErrInvalid
	}
	manual, _ := json.Marshal(effectivePolicy{Parameters: []ParameterRule{}, Combinations: []CombinationRule{}, Mapping: normalizeMapping(Mapping{})})
	tiers, _ := json.Marshal(pricing.Tiers)
	sizes, _ := json.Marshal(pricing.Sizes)
	if len(tiers) > 16384 || len(sizes) > maxJSON {
		return preparedModelPolicy{}, invalidPolicy("input.pricing", "invalid_price", "Review the model's tier and exact-size prices.")
	}
	hash := sha256.Sum256(append(append([]byte(nil), source...), metadata...))
	return preparedModelPolicy{schema: 2, readiness: input.automaticSource.Readiness, sourceRaw: string(source), manualRaw: string(manual), effectiveRaw: string(effective), candidateHash: hash[:], fallback: pricing.Fallback, tiersRaw: string(tiers), sizesRaw: string(sizes), paperMag: encodeAmount(unit.Paper), brushMag: encodeAmount(unit.Brush)}, nil
}

func refreshAutomaticModelsTx(ctx context.Context, tx *sql.Tx, control string, now int64) error {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM image_activity_models WHERE control_id=? AND current_revision IS NOT NULL ORDER BY id", control)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err = refreshAutomaticModelTx(ctx, tx, id, now); err != nil {
			return err
		}
	}
	return nil
}

func refreshAutomaticModelTx(ctx context.Context, tx *sql.Tx, id string, now int64) error {
	previous, err := modelTx(ctx, tx, id, 0)
	if err != nil || previous.revision == 0 {
		return err
	}
	capability := compileFixedCapability(previous.metadata)
	if !capability.recognized {
		return nil
	}
	pricing := previous.pricing
	input, err := automaticModelInput(previous, ModelInput{ExpectedRevision: strconv.FormatInt(previous.revision, 10), Enabled: previous.input.Enabled, Price: previous.input.Price, Pricing: &pricing}, true)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(previous.input.Parameters, input.Parameters) && reflect.DeepEqual(previous.input.Combinations, input.Combinations) && reflect.DeepEqual(previous.input.Mapping, input.Mapping) && reflect.DeepEqual(previous.size, input.SizeCapability) && previous.readiness == capability.compiled.Readiness && previous.catalogType == input.CatalogType {
		return nil
	}
	if previous.revision == math.MaxInt64 {
		return ErrCapacity
	}
	next := previous.revision + 1
	parameters, _ := json.Marshal(input.Parameters)
	combinations, _ := json.Marshal(input.Combinations)
	mapping, _ := json.Marshal(input.Mapping)
	_, err = tx.ExecContext(ctx, `INSERT INTO image_model_revisions
	 (model_id,revision,display_name,description,enabled,parameters_json,combinations_json,mapping_json,paper_price_mag,brush_price_mag,actor_user_id,created_at)
	 SELECT model_id,?,display_name,description,enabled,?,?,?,paper_price_mag,brush_price_mag,actor_user_id,?
	 FROM image_model_revisions WHERE model_id=? AND revision=?`, next, string(parameters), string(combinations), string(mapping), now, id, previous.revision)
	if err != nil {
		return err
	}
	prepared, err := prepareAutomaticPolicy(input, previous.metadata)
	if err != nil {
		return err
	}
	if err = storePreparedModelPolicyTx(ctx, tx, id, next, prepared, now); err != nil {
		return err
	}
	return requireOne(tx.ExecContext(ctx, "UPDATE image_activity_models SET current_revision=? WHERE id=? AND current_revision=?", next, id, previous.revision))
}

func (s *Service) CheckModelSettings(ctx context.Context, admin int64, input ModelSettingsCheck) (CheckResult, error) {
	return s.CheckModel(ctx, admin, CheckInput{ModelID: input.ModelID, Draft: modelSettingsInput(input.Draft), Parameters: input.Parameters})
}
