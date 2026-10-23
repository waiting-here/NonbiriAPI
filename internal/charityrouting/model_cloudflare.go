package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) attachModelCloudflareMode(models []AdminCharityModel) {
	mode := "direct"
	if provider, ok := s.baseURLs.(interface{ CloudflareProxyMode() string }); ok {
		mode = provider.CloudflareProxyMode()
	}
	for i := range models {
		models[i].CloudflareProxyMode = mode
	}
}

func (s *Service) replayModel(decision idempotency.Decision) (resources.MutationResult[AdminCharityModel], error) {
	result, err := replayModel(decision)
	if err != nil {
		return result, err
	}
	models := []AdminCharityModel{result.Value}
	s.attachModelCloudflareMode(models)
	result.Value = models[0]
	result.Body, err = json.Marshal(result.Value)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvariant
	}
	return result, nil
}
func (s *Service) getModelWithCloudflareMode(ctx context.Context, tx *sql.Tx, modelID, at int64) (AdminCharityModel, error) {
	model, err := getAdminModelTx(ctx, tx, modelID, at)
	if err != nil {
		return AdminCharityModel{}, err
	}
	models := []AdminCharityModel{model}
	s.attachModelCloudflareMode(models)
	return models[0], nil
}
