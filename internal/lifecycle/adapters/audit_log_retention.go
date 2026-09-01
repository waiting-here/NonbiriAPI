package adapters

import (
	"context"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/announcements"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

type RequestLogRetentionOwner interface {
	RetainLifecycleRequestLogs(context.Context, int64, int, time.Time) (logapi.LifecycleRetentionResult, error)
}

type MaintenanceAuditRetentionOwner interface {
	RetainEvents(context.Context, int64, int, time.Time) (maintenance.LifecycleRetentionResult, error)
}

type AnnouncementAuditRetentionOwner interface {
	RetainLifecycleAudits(context.Context, int64, int, time.Time) (announcements.LifecycleAuditRetentionResult, error)
}

type RequestLogRetention struct {
	owner RequestLogRetentionOwner
}

func NewRequestLogRetention(owner RequestLogRetentionOwner) *RequestLogRetention {
	return &RequestLogRetention{owner: owner}
}

func (adapter *RequestLogRetention) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	if err := validateAdminObjectWorker(ctx, decisionNow, limit, budgetDeadline); err != nil {
		return lifecycle.WorkResult{}, err
	}
	result, err := adapter.owner.RetainLifecycleRequestLogs(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, translateHeldObjectError(err)
	}
	if !validRetentionResult(result.Processed, result.More, limit) || result.RequestLogsDeleted != result.Processed {
		return lifecycle.WorkResult{}, lifecycle.ErrInvariant
	}
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, nil
}

// AuditRetention composes the fixed maintenance-event and announcement-audit
// owners. Maintenance always drains first; announcements receive only the
// remaining total budget and the same frozen time/deadline.
type AuditRetention struct {
	maintenance   MaintenanceAuditRetentionOwner
	announcements AnnouncementAuditRetentionOwner
}

func NewAuditRetention(
	maintenanceOwner MaintenanceAuditRetentionOwner,
	announcementOwner AnnouncementAuditRetentionOwner,
) *AuditRetention {
	return &AuditRetention{maintenance: maintenanceOwner, announcements: announcementOwner}
}

func (adapter *AuditRetention) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.maintenance == nil || adapter.announcements == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	if err := validateAdminObjectWorker(ctx, decisionNow, limit, budgetDeadline); err != nil {
		return lifecycle.WorkResult{}, err
	}

	maintenanceResult, err := adapter.maintenance.RetainEvents(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, translateHeldObjectError(err)
	}
	if !validRetentionResult(maintenanceResult.Processed, maintenanceResult.More, limit) ||
		maintenanceResult.Processed != maintenanceResult.ActorsDeidentified+maintenanceResult.EventsDeleted {
		return lifecycle.WorkResult{}, lifecycle.ErrInvariant
	}
	if maintenanceResult.More {
		return lifecycle.WorkResult{Processed: maintenanceResult.Processed, More: true}, nil
	}
	remaining := limit - maintenanceResult.Processed
	if remaining == 0 {
		// The stable next owner has not yet been observed, so schedule one
		// bounded continuation rather than silently skipping it.
		return lifecycle.WorkResult{Processed: maintenanceResult.Processed, More: true}, nil
	}

	announcementResult, err := adapter.announcements.RetainLifecycleAudits(
		ctx, decisionNow, remaining, budgetDeadline,
	)
	if err != nil {
		return lifecycle.WorkResult{}, translateAdminObjectError(err)
	}
	if !validRetentionResult(announcementResult.Processed, announcementResult.More, remaining) ||
		announcementResult.Processed != announcementResult.ActorsDeidentified+announcementResult.AuditsDeleted {
		return lifecycle.WorkResult{}, lifecycle.ErrInvariant
	}
	return lifecycle.WorkResult{
		Processed: maintenanceResult.Processed + announcementResult.Processed,
		More:      announcementResult.More,
	}, nil
}

func validRetentionResult(processed int, more bool, limit int) bool {
	return processed >= 0 && processed <= limit && (!more || processed > 0)
}

var (
	_ RequestLogRetentionOwner        = (*logapi.Repository)(nil)
	_ MaintenanceAuditRetentionOwner  = (*maintenance.LifecycleRetention)(nil)
	_ AnnouncementAuditRetentionOwner = (*announcements.Repository)(nil)
	_ lifecycle.RetentionAdapter      = (*RequestLogRetention)(nil)
	_ lifecycle.RetentionAdapter      = (*AuditRetention)(nil)
)
