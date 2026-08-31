package activities

import (
	"context"
	"errors"
)

type ServiceConfig struct {
	Repository *Repository
	Publisher  PostCommitPublisher
	Reporter   PublishErrorReporter
}

type Service struct {
	repository *Repository
	publisher  PostCommitPublisher
	reporter   PublishErrorReporter
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Repository == nil {
		return nil, errors.New("activities: repository is required")
	}
	if isNilInterface(config.Publisher) {
		config.Publisher = nil
	}
	if isNilInterface(config.Reporter) {
		config.Reporter = nil
	}
	return &Service{repository: config.Repository, publisher: config.Publisher, reporter: config.Reporter}, nil
}

func (service *Service) Repository() *Repository {
	if service == nil {
		return nil
	}
	return service.repository
}

// PublishCommittedFacts is the shared hook for another domain that has
// already committed a typed pool transfer through RecordPoolTransfers.
func (service *Service) PublishCommittedFacts(ctx context.Context, facts PublishFacts) {
	if service == nil || facts.empty() || service.publisher == nil {
		return
	}
	if err := service.publisher.Publish(ctx, facts); err != nil && service.reporter != nil {
		service.reporter.ReportActivitiesPublishError(err)
	}
}

func (service *Service) GetActivities(ctx context.Context, userID int64) (ActivitiesSnapshot, error) {
	return service.repository.GetActivities(ctx, userID)
}

func (service *Service) GetThursday(ctx context.Context, userID int64) (ThursdayView, error) {
	return service.repository.GetThursday(ctx, userID)
}

func (service *Service) GetAdminThursday(ctx context.Context, adminID int64) (AdminThursdayState, error) {
	return service.repository.GetAdminThursday(ctx, adminID)
}

func (service *Service) GetActivitiesConfig(ctx context.Context) (ActivitiesConfig, error) {
	return service.repository.GetActivitiesConfig(ctx)
}

func (service *Service) ListPools(ctx context.Context, query PoolListQuery) (Page[Pool], error) {
	return service.repository.ListPools(ctx, query)
}

func (service *Service) ClaimWelfare(ctx context.Context, userID int64, mutation ControlMutation) (MutationResult[WelfareClaimResult], error) {
	result, facts, err := service.repository.ClaimWelfare(ctx, userID, mutation)
	if err == nil {
		service.PublishCommittedFacts(ctx, facts)
	}
	return result, err
}

func (service *Service) ContributeThursday(ctx context.Context, userID int64, mutation ControlMutation, input ThursdayContributionInput) (MutationResult[ThursdayContributionResult], error) {
	result, facts, err := service.repository.ContributeThursday(ctx, userID, mutation, input)
	if err == nil {
		service.PublishCommittedFacts(ctx, facts)
	}
	return result, err
}

func (service *Service) PatchActivitiesConfig(ctx context.Context, adminID int64, mutation ControlMutation, patch ActivitiesConfigPatch) (MutationResult[ActivitiesConfig], error) {
	result, facts, err := service.repository.PatchActivitiesConfig(ctx, adminID, mutation, patch)
	if err == nil {
		service.PublishCommittedFacts(ctx, facts)
	}
	return result, err
}

func (service *Service) PutThursdayNext(ctx context.Context, adminID int64, mutation ControlMutation, input ThursdayNextMutation) (MutationResult[Period], error) {
	result, facts, err := service.repository.PutThursdayNext(ctx, adminID, mutation, input)
	if err == nil {
		service.PublishCommittedFacts(ctx, facts)
	}
	return result, err
}

func (service *Service) AdjustPool(ctx context.Context, adminID int64, poolID string, mutation ControlMutation, input PoolAdjustment) (MutationResult[Pool], error) {
	result, facts, err := service.repository.AdjustPool(ctx, adminID, poolID, mutation, input)
	if err == nil {
		service.PublishCommittedFacts(ctx, facts)
	}
	return result, err
}

func (service *Service) ResumeThursday(ctx context.Context, adminID int64, periodID string, mutation ControlMutation, expectedRevision int64) (MutationResult[Period], error) {
	result, facts, err := service.repository.ResumeThursday(ctx, adminID, periodID, mutation, expectedRevision)
	if err == nil {
		service.PublishCommittedFacts(ctx, facts)
	}
	return result, err
}

func (service *Service) RunSettlementStep(ctx context.Context) (WorkerResult, error) {
	result, facts, err := service.repository.RunSettlementStep(ctx)
	if err == nil {
		service.PublishCommittedFacts(ctx, facts)
	}
	return result, err
}
