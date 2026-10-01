package stewardautomation

import (
	"context"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

func strictPersonalJSON(body []byte) error {
	return strictjson.ValidateObjectWithFieldLimit(body, 16384)
}
func (s *Service) readAutomation(ctx context.Context, userID int64, route automationRoute, ids []int64, q string, page pagination.Request) (any, error) {
	switch route.kind {
	case "endpoints":
		return s.resources.AutomationEndpoints(ctx, userID, q, page)
	case "endpoints_one":
		return s.resources.AutomationEndpoint(ctx, userID, ids[0])
	case "keys":
		return s.resources.AutomationKeys(ctx, userID, ids[0], q, page)
	case "key_one":
		return s.resources.AutomationKey(ctx, userID, ids[0], ids[1])
	case "models":
		return s.resources.AutomationModels(ctx, userID, q, page)
	case "models_one":
		return s.resources.AutomationModel(ctx, userID, ids[0])
	case "bindings":
		return s.resources.AutomationBindings(ctx, userID, ids[0], q, page)
	case "donations":
		return s.donations.AutomationDonations(ctx, userID, q, page)
	case "donations_one":
		return s.donations.AutomationDonation(ctx, userID, ids[0])
	case "donation_keys":
		return s.donations.AutomationKeys(ctx, userID, ids[0], q, page)
	case "donation_key":
		return s.donations.AutomationKey(ctx, userID, ids[0], ids[1])
	case "donation_catalog":
		return s.charity.AutomationCatalog(ctx, userID, ids[0], ids[1], q, page)
	case "charity_models":
		return s.charity.AutomationModels(ctx, userID, q, page)
	case "charity-models_one":
		return s.charity.GetSteward(ctx, userID, ids[0])
	case "charity_bindings":
		return s.charity.AutomationBindings(ctx, userID, ids[0], q, page)
	case "charity_candidates":
		return s.charity.AutomationCandidates(ctx, userID, ids[0], q, page)
	default:
		return nil, errInvalid
	}
}
