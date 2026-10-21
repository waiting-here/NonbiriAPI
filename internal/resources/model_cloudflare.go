package resources

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func (r *Repository) getModelTx(ctx context.Context, tx *sql.Tx, userID, modelID int64) (Model, error) {
	model, err := getModelTx(ctx, tx, userID, modelID)
	if err != nil {
		return Model{}, err
	}
	return r.modelWithCloudflareMode(model), nil
}

func (r *Repository) replayModelMutation(decision idempotency.Decision) (MutationResult[Model], error) {
	value := Model{CloudflareProxyEnabled: true}
	if err := json.Unmarshal(decision.ResponseBody, &value); err != nil {
		return MutationResult[Model]{}, ErrUnavailable
	}
	value = r.modelWithCloudflareMode(value)
	body, err := json.Marshal(value)
	if err != nil {
		return MutationResult[Model]{}, ErrUnavailable
	}
	return MutationResult[Model]{Value: value, Status: decision.HTTPStatus, Body: body, Replayed: true}, nil
}

func (r *Repository) replayManualModelUpdate(decision idempotency.Decision) (MutationResult[ManualUpdateResponse], error) {
	result, err := replayMutation[ManualUpdateResponse](decision)
	if err != nil {
		return result, err
	}
	// Only these documented nested models receive compatibility defaults.
	var stored struct {
		AffectedModels []struct {
			Model struct {
				CloudflareProxyEnabled *bool `json:"cloudflare_proxy_enabled"`
			} `json:"model"`
		} `json:"affected_models"`
	}
	if err := json.Unmarshal(decision.ResponseBody, &stored); err != nil {
		return MutationResult[ManualUpdateResponse]{}, ErrUnavailable
	}
	for i := range result.Value.AffectedModels {
		model := &result.Value.AffectedModels[i].Model
		if stored.AffectedModels[i].Model.CloudflareProxyEnabled == nil {
			model.CloudflareProxyEnabled = true
		}
		*model = r.modelWithCloudflareMode(*model)
	}
	result.Body, err = json.Marshal(result.Value)
	if err != nil {
		return MutationResult[ManualUpdateResponse]{}, ErrUnavailable
	}
	return result, nil
}
func (r *Repository) attachModelCloudflareMode(models []Model) {
	mode := "direct"
	if provider, ok := r.baseURLs.(interface{ CloudflareProxyMode() string }); ok {
		mode = provider.CloudflareProxyMode()
	}
	for i := range models {
		models[i].CloudflareProxyMode = mode
	}
}
func (r *Repository) modelWithCloudflareMode(model Model) Model {
	models := []Model{model}
	r.attachModelCloudflareMode(models)
	return models[0]
}
