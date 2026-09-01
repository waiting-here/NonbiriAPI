package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const maxUnixSecond = int64(253402300799)

type identityLifecycle interface {
	ExportLifecycleIdentity(context.Context, *sql.Tx, int64, int64, int) (auth.LifecycleIdentity, auth.LifecycleUsage, error)
	PrepareLifecycleAccountDeletion(context.Context, *sql.Tx, int64, int64) (auth.LifecycleDeletionFinalizer, error)
}

type resourceLifecycle interface {
	ExportLifecycleResources(context.Context, *sql.Tx, int64, int64, int) (resources.LifecycleResourceExport, error)
	PrepareLifecycleAccountDeletion(context.Context, *sql.Tx, int64, int64) error
}

type issueLifecycle interface {
	ExportLifecycleIssues(context.Context, *sql.Tx, int64, int64, int) ([]issues.LifecycleIssue, error)
	PrepareAccountDeletion(context.Context, *sql.Tx, int64, int64) error
}

type logLifecycle interface {
	ExportLifecycleSummary(context.Context, *sql.Tx, int64, int64, int) (logapi.LifecycleLogSummary, error)
	PrepareLifecycleAccountDeletion(context.Context, *sql.Tx, int64, int64) error
}

// AccountResources is the fixed account/auth, resource, issue, and request-log
// slice of the lifecycle coordinator. It exposes no callback or generic SQL
// seam and never owns the caller's transaction.
type AccountResources struct {
	identity  identityLifecycle
	resources resourceLifecycle
	issues    issueLifecycle
	logs      logLifecycle
}

func NewAccountResources(
	identity *auth.Runtime,
	resourceRepository *resources.Repository,
	issueSource *issues.SourceAdapter,
	logRepository *logapi.Repository,
) (*AccountResources, error) {
	if identity == nil || resourceRepository == nil || issueSource == nil || logRepository == nil {
		return nil, lifecycle.ErrInvalid
	}
	return &AccountResources{
		identity: identity, resources: resourceRepository, issues: issueSource, logs: logRepository,
	}, nil
}

var (
	_ lifecycle.IdentityExporter = (*AccountResources)(nil)
	_ lifecycle.ResourceExporter = (*AccountResources)(nil)
	_ lifecycle.IssueExporter    = (*AccountResources)(nil)
	_ lifecycle.DeleteAdapter    = (*AccountResources)(nil)
)

func (adapter *AccountResources) ExportIdentity(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.ExportRequest,
) (lifecycle.UserExport, lifecycle.UsageExport, lifecycle.LogSummaryExport, error) {
	if err := adapter.validateExport(ctx, tx, request); err != nil {
		return lifecycle.UserExport{}, lifecycle.UsageExport{}, lifecycle.LogSummaryExport{}, err
	}
	identity, usage, err := adapter.identity.ExportLifecycleIdentity(
		ctx, tx, request.UserID, request.DecisionNow, request.Limit,
	)
	if err != nil {
		return lifecycle.UserExport{}, lifecycle.UsageExport{}, lifecycle.LogSummaryExport{}, translateError(err)
	}
	logs, err := adapter.logs.ExportLifecycleSummary(ctx, tx, request.UserID, request.DecisionNow, request.Limit)
	if err != nil {
		return lifecycle.UserExport{}, lifecycle.UsageExport{}, lifecycle.LogSummaryExport{}, translateError(err)
	}
	return lifecycle.UserExport{
			ID: identity.ID, Username: identity.Username, Avatar: identity.Avatar, AvatarURL: identity.AvatarURL,
			GuildNick: identity.GuildNick, GuildAvatarURL: identity.GuildAvatarURL, Lang: identity.Lang,
			IsBanned: identity.IsBanned, BannedUntil: identity.BannedUntil,
			CharitySuspendedUntil: identity.CharitySuspendedUntil,
			EndpointLimit:         identity.EndpointLimit, EffectiveEndpointLimit: identity.EffectiveEndpointLimit,
			RPMLimit: identity.RPMLimit, EffectiveRPMLimit: identity.EffectiveRPMLimit,
			ConcurrencyLimit:          identity.ConcurrencyLimit,
			EffectiveConcurrencyLimit: identity.EffectiveConcurrencyLimit,
			Balance:                   identity.Balance, DonationCredit: identity.DonationCredit,
			EffectiveLevel: identity.EffectiveLevel, LevelDisplayName: identity.LevelDisplayName,
			GameProfilePublic: identity.GameProfilePublic, CreatedAt: identity.CreatedAt, UpdatedAt: identity.UpdatedAt,
		}, lifecycle.UsageExport{
			TotalRequests:              usage.TotalRequests,
			TotalUncachedInputTokens:   usage.TotalUncachedInputTokens,
			TotalCacheWriteInputTokens: usage.TotalCacheWriteInputTokens,
			TotalCacheReadInputTokens:  usage.TotalCacheReadInputTokens,
			TotalOutputTokens:          usage.TotalOutputTokens,
			TotalPromptTokens:          usage.TotalPromptTokens,
			TotalCompletionTokens:      usage.TotalCompletionTokens,
			TotalUnknownUsageRequests:  usage.TotalUnknownUsageRequests,
		}, lifecycle.LogSummaryExport{
			TotalLogs: logs.TotalLogs, LogsLast30Days: logs.LogsLast30Days, ErrorLogs: logs.ErrorLogs,
			UsageUnknownLogs: logs.UsageUnknownLogs, AverageDuration: logs.AverageDuration,
		}, nil
}

func (adapter *AccountResources) ExportResources(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.ExportRequest,
) ([]lifecycle.EndpointExport, []lifecycle.CatalogPairExport, []lifecycle.ModelExport, *lifecycle.CallerKeyExport, error) {
	if err := adapter.validateExport(ctx, tx, request); err != nil {
		return nil, nil, nil, nil, err
	}
	slice, err := adapter.resources.ExportLifecycleResources(
		ctx, tx, request.UserID, request.DecisionNow, request.Limit,
	)
	if err != nil {
		return nil, nil, nil, nil, translateError(err)
	}
	endpoints := make([]lifecycle.EndpointExport, 0, len(slice.Endpoints))
	for _, endpoint := range slice.Endpoints {
		item := lifecycle.EndpointExport{
			ID: endpoint.ID, ConnectorType: endpoint.ConnectorType, BaseURL: endpoint.BaseURL,
			Note: endpoint.Note, Enabled: endpoint.Enabled, CreatedAt: endpoint.CreatedAt,
			UpdatedAt: endpoint.UpdatedAt, Keys: make([]lifecycle.EndpointKeyExport, 0, len(endpoint.Keys)),
		}
		for _, key := range endpoint.Keys {
			item.Keys = append(item.Keys, lifecycle.EndpointKeyExport{
				ID: key.ID, DisplayHead: key.DisplayHead, DisplayTail: key.DisplayTail, Note: key.Note,
				Enabled: key.Enabled, ForceStoreFalse: key.ForceStoreFalse,
				SuspensionState: key.SuspensionState, CreatedAt: key.CreatedAt, UpdatedAt: key.UpdatedAt,
			})
		}
		endpoints = append(endpoints, item)
	}
	pairs := make([]lifecycle.CatalogPairExport, 0, len(slice.CatalogPairs))
	for _, pair := range slice.CatalogPairs {
		item := lifecycle.CatalogPairExport{
			EndpointID: pair.EndpointID, EndpointKeyID: pair.EndpointKeyID,
			Evidence: lifecycle.DiscoveryEvidenceExport{
				State: pair.Evidence.State, Result: pair.Evidence.Result, SafeClass: pair.Evidence.SafeClass,
				ObservedAt: pair.Evidence.ObservedAt, Count: pair.Evidence.Count,
			},
			AutomaticEntries: make([]lifecycle.CatalogEntryExport, 0, len(pair.AutomaticEntries)),
			ManualEntries:    make([]lifecycle.CatalogEntryExport, 0, len(pair.ManualEntries)),
		}
		for _, entry := range pair.AutomaticEntries {
			item.AutomaticEntries = append(item.AutomaticEntries, mapCatalogEntry(entry))
		}
		for _, entry := range pair.ManualEntries {
			item.ManualEntries = append(item.ManualEntries, mapCatalogEntry(entry))
		}
		pairs = append(pairs, item)
	}
	models := make([]lifecycle.ModelExport, 0, len(slice.Models))
	for _, model := range slice.Models {
		item := lifecycle.ModelExport{
			ID: model.ID, Provider: model.Provider, Model: model.Model, FullName: model.FullName,
			RouteStrategy: model.RouteStrategy, SilentRetry: model.SilentRetry,
			FlattenToolCalls: model.FlattenToolCalls, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
			Bindings: make([]lifecycle.BindingExport, 0, len(model.Bindings)),
		}
		for _, binding := range model.Bindings {
			item.Bindings = append(item.Bindings, lifecycle.BindingExport{
				ID: binding.ID, EndpointKeyID: binding.EndpointKeyID,
				EndpointBaseURL: binding.EndpointBaseURL, ConnectorType: binding.ConnectorType,
				EndpointNote:           binding.EndpointNote,
				EndpointKeyDisplayHead: binding.EndpointKeyDisplayHead,
				EndpointKeyDisplayTail: binding.EndpointKeyDisplayTail,
				EndpointKeyNote:        binding.EndpointKeyNote, UpstreamModelID: binding.UpstreamModelID,
				Ord: binding.Ord,
			})
		}
		models = append(models, item)
	}
	var callerKey *lifecycle.CallerKeyExport
	if slice.CallerKey != nil {
		callerKey = &lifecycle.CallerKeyExport{
			Display: slice.CallerKey.Display, Generation: slice.CallerKey.Generation,
			CreatedAt: slice.CallerKey.CreatedAt, UpdatedAt: slice.CallerKey.UpdatedAt,
		}
	}
	return endpoints, pairs, models, callerKey, nil
}

func mapCatalogEntry(entry resources.LifecycleCatalogEntry) lifecycle.CatalogEntryExport {
	return lifecycle.CatalogEntryExport{
		ID: entry.ID, SourceType: entry.SourceType, UpstreamModelID: entry.UpstreamModelID,
		Provider: entry.Provider, CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt,
	}
}

func (adapter *AccountResources) ExportIssues(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.ExportRequest,
) ([]lifecycle.IssueExport, error) {
	if err := adapter.validateExport(ctx, tx, request); err != nil {
		return nil, err
	}
	domainIssues, err := adapter.issues.ExportLifecycleIssues(
		ctx, tx, request.UserID, request.DecisionNow, request.Limit,
	)
	if err != nil {
		return nil, translateError(err)
	}
	items := make([]lifecycle.IssueExport, 0, len(domainIssues))
	for _, issue := range domainIssues {
		item := lifecycle.IssueExport{
			ID: issue.ID, State: issue.State, Source: issue.Source, ResourceKind: issue.ResourceKind,
			SummaryCode: issue.SummaryCode, SafeDetail: issue.SafeDetail,
			FirstSeenAt: issue.FirstSeenAt, LastSeenAt: issue.LastSeenAt,
			Count: issue.Count, ClosedAt: issue.ClosedAt,
		}
		if issue.DeepLink != nil {
			item.DeepLink = &lifecycle.IssueDeepLinkExport{
				RouteID: issue.DeepLink.RouteID, ResourceID: issue.DeepLink.ResourceID,
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func (adapter *AccountResources) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter == nil || adapter.identity == nil || adapter.resources == nil || adapter.issues == nil || adapter.logs == nil ||
		ctx == nil || tx == nil || request.UserID <= 0 || request.DecisionNow < 0 || request.DecisionNow > maxUnixSecond {
		return nil, lifecycle.ErrInvalid
	}
	finalizer, err := adapter.identity.PrepareLifecycleAccountDeletion(
		ctx, tx, request.UserID, request.DecisionNow,
	)
	if err != nil {
		return nil, translateError(err)
	}
	abort := func(err error) (lifecycle.DeleteFinalizer, error) {
		if finalizer != nil {
			_ = finalizer.Abort()
		}
		return nil, translateError(err)
	}
	if err := adapter.resources.PrepareLifecycleAccountDeletion(ctx, tx, request.UserID, request.DecisionNow); err != nil {
		return abort(err)
	}
	if err := adapter.issues.PrepareAccountDeletion(ctx, tx, request.UserID, request.DecisionNow); err != nil {
		return abort(err)
	}
	if err := adapter.logs.PrepareLifecycleAccountDeletion(ctx, tx, request.UserID, request.DecisionNow); err != nil {
		return abort(err)
	}
	return finalizer, nil
}

func (adapter *AccountResources) validateExport(ctx context.Context, tx *sql.Tx, request lifecycle.ExportRequest) error {
	if adapter == nil || adapter.identity == nil || adapter.resources == nil || adapter.issues == nil || adapter.logs == nil ||
		ctx == nil || tx == nil || request.UserID <= 0 || request.DecisionNow < 0 || request.DecisionNow > maxUnixSecond ||
		request.Limit < 1 || request.Limit > lifecycle.CollectionLimit {
		return lifecycle.ErrInvalid
	}
	return nil
}

func translateError(err error) error {
	if err == nil {
		return nil
	}
	var target error
	switch {
	case errors.Is(err, resources.ErrResourceLimit), errors.Is(err, issues.ErrResourceLimit), errors.Is(err, logapi.ErrCapacity):
		target = lifecycle.ErrTooLarge
	case errors.Is(err, auth.ErrLifecycleInvalid), errors.Is(err, resources.ErrInvalidRequest),
		errors.Is(err, issues.ErrInvalidRequest), errors.Is(err, logapi.ErrInvalid):
		target = lifecycle.ErrInvalid
	case errors.Is(err, resources.ErrUnauthorized), errors.Is(err, issues.ErrUnauthorized):
		target = lifecycle.ErrUnauthorized
	case errors.Is(err, resources.ErrForbidden), errors.Is(err, issues.ErrForbidden), errors.Is(err, logapi.ErrForbidden):
		target = lifecycle.ErrForbidden
	case errors.Is(err, auth.ErrLifecycleNotFound), errors.Is(err, resources.ErrNotFound),
		errors.Is(err, issues.ErrNotFound), errors.Is(err, logapi.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		target = lifecycle.ErrNotFound
	case errors.Is(err, resources.ErrConflict), errors.Is(err, issues.ErrConflict), errors.Is(err, logapi.ErrConflict):
		target = lifecycle.ErrConflict
	case errors.Is(err, logapi.ErrInvariant):
		target = lifecycle.ErrInvariant
	case errors.Is(err, resources.ErrUnavailable), errors.Is(err, issues.ErrUnavailable),
		errors.Is(err, logapi.ErrUnavailable), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		target = lifecycle.ErrUnavailable
	default:
		target = lifecycle.ErrUnavailable
	}
	return fmt.Errorf("%w: %v", target, err)
}
