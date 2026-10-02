package forward

import (
	"context"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

type GatewayModelReader interface {
	LoadMany(context.Context, []gatewaypolicy.Target) (map[gatewaypolicy.Target]gatewaypolicy.Model, error)
}

func (s *Service) freezeGatewayModels(ctx context.Context, plan *executionPlan) error {
	if s.gatewayModels == nil {
		return nil
	}
	targets := []gatewaypolicy.Target{}
	for _, c := range plan.candidates {
		if c.ConnectorType == contract.TypeAISDKGatewayV3 {
			targets = append(targets, gatewaypolicy.Target{BaseURL: c.CanonicalBaseURL, Model: c.UpstreamModelID})
		}
	}
	if len(targets) == 0 {
		return nil
	}
	models, err := s.gatewayModels.LoadMany(ctx, targets)
	if err != nil {
		return err
	}
	for i := range plan.candidates {
		c := &plan.candidates[i]
		if c.ConnectorType == contract.TypeAISDKGatewayV3 {
			model := models[gatewaypolicy.Target{BaseURL: c.CanonicalBaseURL, Model: c.UpstreamModelID}]
			c.Policy.GatewayModel = &model
		}
	}
	return nil
}
