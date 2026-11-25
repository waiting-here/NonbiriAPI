package logapi

import (
	"context"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type logReadRoleKey struct{}

func logReadContext(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, logReadRoleKey{}, role)
}

func observeLogRead(ctx context.Context, phase string) func(error) {
	role, _ := ctx.Value(logReadRoleKey{}).(string)
	return observability.LogReadPhase(ctx, role, phase)
}

func logListContext(ctx context.Context, role string, filter ListFilter) context.Context {
	var page int64
	if filter.Page != nil {
		page = filter.Page.Page
	}
	return observability.WithLogReadShape(logReadContext(ctx, role), observability.LogFilterShape{
		Operation: filter.Operation != "", Outcome: filter.Outcome != "", Phase: filter.Phase != "",
		User: filter.UserID != nil, Discord: filter.DiscordID != nil, EndpointKey: filter.EndpointKeyID != nil, EndpointURL: filter.EndpointBaseURL != nil,
		UpstreamModel: filter.UpstreamModel != nil, Model: filter.Model != nil, CharityModel: filter.CharityModel != nil,
		Status: filter.Status != nil, ErrorCode: filter.ErrorCode != nil, ErrorCategory: filter.ErrorCategory != "", ErrorPhase: filter.ErrorPhase != "", UsageMismatch: filter.UsageTotalMismatch != nil,
	}, page, filter.From, filter.To)
}
