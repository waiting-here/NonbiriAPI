package charityrouting

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func authorizeModelPatch(ctx context.Context, tx *sql.Tx, actorID int64, input ModelPatch) error {
	p, err := authz.ReadStewardPermissions(ctx, tx, actorID)
	if err != nil {
		return err
	}
	settings := input.Provider != nil || input.Model != nil || input.Enabled != nil || input.FlattenToolCalls != nil || input.CloudflareProxyEnabled != nil || input.RouteStrategy != nil || input.AllowedLevels != nil || input.PublicDescription != nil || input.IsMainstream != nil || input.ExcludedRequestFields != nil || input.ModelTypes != nil || input.TransportRule != nil || input.SessionHeaders != nil
	pricing := input.Pricing != nil || input.Discount != nil || input.RecurringDiscount != nil || input.TokenReserveCredits != nil
	if input.RolePolicy != nil && !p.Permissions["charity.manage"] || settings && !p.Permissions["charity.model_settings"] || pricing && !p.Permissions["charity.pricing"] || input.IsMainstream != nil && !*input.IsMainstream && !p.Permissions["charity.non_mainstream"] {
		return authz.ErrPermissionDenied
	}
	return nil
}
