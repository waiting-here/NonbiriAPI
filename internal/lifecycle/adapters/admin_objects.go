package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/announcements"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/reports"
)

// ReportLifecycleOwner keeps report SQL, reducers, and aggregate boundaries in
// the report domain. In particular, account deletion must use the report
// package's authoritative DetachUserForDeletion operation.
type ReportLifecycleOwner interface {
	DetachUserForDeletion(context.Context, *sql.Tx, int64, int64) error
	RecoverLifecycle(context.Context, int64, int, time.Time) (reports.LifecycleWorkResult, error)
	RetainLifecycle(context.Context, int64, int, time.Time) (reports.LifecycleWorkResult, error)
	InspectLifecycleHeldCase(context.Context, *sql.Tx, string, int64) (reports.LifecycleHeldCaseState, error)
	ConsumeLifecycleHeldCaseMarker(context.Context, *sql.Tx, string) error
	ReadLifecycleHeldCase(context.Context, *sql.Tx, string, int64) (bool, error)
}

type IssueAccountDeletionOwner interface {
	PrepareAccountDeletion(context.Context, *sql.Tx, int64, int64) error
}

type IssueRetentionOwner interface {
	RetainLifecycleClosed(context.Context, int64, int, time.Time) (issues.RetentionResult, error)
}

type AnnouncementAuditLifecycleOwner interface {
	RetainLifecycleAudits(context.Context, int64, int, time.Time) (announcements.LifecycleAuditRetentionResult, error)
	InspectLifecycleHeldAudit(context.Context, *sql.Tx, int64, int64) (announcements.LifecycleHeldAuditState, error)
	ConsumeLifecycleHeldAuditMarker(context.Context, *sql.Tx, int64) error
	ReadLifecycleHeldAudit(context.Context, *sql.Tx, int64, int64) (bool, error)
}

type ReportLifecycle struct{ owner ReportLifecycleOwner }

func NewReportLifecycle(owner ReportLifecycleOwner) *ReportLifecycle {
	return &ReportLifecycle{owner: owner}
}

func (adapter *ReportLifecycle) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter == nil || adapter.owner == nil {
		return nil, lifecycle.ErrUnavailable
	}
	if err := validateAdminObjectDelete(ctx, tx, request); err != nil {
		return nil, err
	}
	if err := adapter.owner.DetachUserForDeletion(
		ctx, tx, request.UserID, request.DecisionNow,
	); err != nil {
		return nil, translateAdminObjectError(err)
	}
	return nil, nil
}

func (adapter *ReportLifecycle) RecoverBeforeListener(
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
	result, err := adapter.owner.RecoverLifecycle(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, translateAdminObjectError(err)
	}
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, nil
}

func (adapter *ReportLifecycle) Retain(
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
	result, err := adapter.owner.RetainLifecycle(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, translateAdminObjectError(err)
	}
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, nil
}

func (adapter *ReportLifecycle) InspectForCreate(
	ctx context.Context,
	tx *sql.Tx,
	ref string,
	decisionNow int64,
) (lifecycle.HeldObjectState, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.HeldObjectState{}, lifecycle.ErrUnavailable
	}
	state, err := adapter.owner.InspectLifecycleHeldCase(ctx, tx, ref, decisionNow)
	if err != nil {
		return lifecycle.HeldObjectState{}, translateAdminObjectError(err)
	}
	return lifecycle.HeldObjectState{
		Exists: state.Exists, OrdinaryDeadline: state.OrdinaryDeadline,
		LegalHoldConsumed: state.LegalHoldConsumed,
	}, nil
}

func (adapter *ReportLifecycle) ConsumeMarker(ctx context.Context, tx *sql.Tx, ref string) error {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.ErrUnavailable
	}
	return translateAdminObjectError(adapter.owner.ConsumeLifecycleHeldCaseMarker(ctx, tx, ref))
}

func (adapter *ReportLifecycle) ReadHeld(
	ctx context.Context,
	tx *sql.Tx,
	ref string,
	decisionNow int64,
) (bool, error) {
	if adapter == nil || adapter.owner == nil {
		return false, lifecycle.ErrUnavailable
	}
	exists, err := adapter.owner.ReadLifecycleHeldCase(ctx, tx, ref, decisionNow)
	return exists, translateAdminObjectError(err)
}

// IssueAnnouncementDelete is the fixed combined deletion slot. Issues own
// user-private projections and are deleted through their authoritative seam.
// Announcements are instance-owned; deleting the user deidentifies audit
// actors through the existing foreign-key action, so there is no second or
// synthetic announcement mutation here.
type IssueAnnouncementDelete struct{ issues IssueAccountDeletionOwner }

func NewIssueAnnouncementDelete(owner IssueAccountDeletionOwner) *IssueAnnouncementDelete {
	return &IssueAnnouncementDelete{issues: owner}
}

func (adapter *IssueAnnouncementDelete) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter == nil || adapter.issues == nil {
		return nil, lifecycle.ErrUnavailable
	}
	if err := validateAdminObjectDelete(ctx, tx, request); err != nil {
		return nil, err
	}
	if err := adapter.issues.PrepareAccountDeletion(
		ctx, tx, request.UserID, request.DecisionNow,
	); err != nil {
		return nil, translateAdminObjectError(err)
	}
	return nil, nil
}

type IssueRetention struct{ owner IssueRetentionOwner }

func NewIssueRetention(owner IssueRetentionOwner) *IssueRetention {
	return &IssueRetention{owner: owner}
}

func (adapter *IssueRetention) Retain(
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
	result, err := adapter.owner.RetainLifecycleClosed(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, translateAdminObjectError(err)
	}
	return lifecycle.WorkResult{
		Processed: result.ClosedDeleted,
		More:      result.ClosedDeleted == limit,
	}, nil
}

// AnnouncementAuditLifecycle covers only content-free audit facts. It never
// owns or exposes announcement bodies, drafts, or localized content.
type AnnouncementAuditLifecycle struct {
	owner AnnouncementAuditLifecycleOwner
}

func NewAnnouncementAuditLifecycle(owner AnnouncementAuditLifecycleOwner) *AnnouncementAuditLifecycle {
	return &AnnouncementAuditLifecycle{owner: owner}
}

func (adapter *AnnouncementAuditLifecycle) Retain(
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
	result, err := adapter.owner.RetainLifecycleAudits(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, translateAdminObjectError(err)
	}
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, nil
}

func (adapter *AnnouncementAuditLifecycle) InspectForCreate(
	ctx context.Context,
	tx *sql.Tx,
	ref string,
	decisionNow int64,
) (lifecycle.HeldObjectState, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.HeldObjectState{}, lifecycle.ErrUnavailable
	}
	auditID, err := parseAnnouncementAuditRef(ref)
	if err != nil {
		return lifecycle.HeldObjectState{}, err
	}
	state, err := adapter.owner.InspectLifecycleHeldAudit(ctx, tx, auditID, decisionNow)
	if err != nil {
		return lifecycle.HeldObjectState{}, translateAdminObjectError(err)
	}
	return lifecycle.HeldObjectState{
		Exists: state.Exists, OrdinaryDeadline: state.OrdinaryDeadline,
		LegalHoldConsumed: state.LegalHoldConsumed,
	}, nil
}

func (adapter *AnnouncementAuditLifecycle) ConsumeMarker(
	ctx context.Context,
	tx *sql.Tx,
	ref string,
) error {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.ErrUnavailable
	}
	auditID, err := parseAnnouncementAuditRef(ref)
	if err != nil {
		return err
	}
	return translateAdminObjectError(adapter.owner.ConsumeLifecycleHeldAuditMarker(ctx, tx, auditID))
}

func (adapter *AnnouncementAuditLifecycle) ReadHeld(
	ctx context.Context,
	tx *sql.Tx,
	ref string,
	decisionNow int64,
) (bool, error) {
	if adapter == nil || adapter.owner == nil {
		return false, lifecycle.ErrUnavailable
	}
	auditID, err := parseAnnouncementAuditRef(ref)
	if err != nil {
		return false, err
	}
	exists, err := adapter.owner.ReadLifecycleHeldAudit(ctx, tx, auditID, decisionNow)
	return exists, translateAdminObjectError(err)
}

func validateAdminObjectDelete(ctx context.Context, tx *sql.Tx, request lifecycle.DeleteRequest) error {
	if ctx == nil || tx == nil || request.UserID <= 0 ||
		request.DecisionNow < 0 || request.DecisionNow > maxUnixSecond {
		return lifecycle.ErrInvalid
	}
	if ctx.Err() != nil {
		return lifecycle.ErrUnavailable
	}
	return nil
}

func validateAdminObjectWorker(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) error {
	if ctx == nil || decisionNow < 0 || decisionNow > maxUnixSecond ||
		limit < 1 || limit > lifecycle.WorkerBatchLimit || budgetDeadline.IsZero() {
		return lifecycle.ErrInvalid
	}
	if ctx.Err() != nil {
		return lifecycle.ErrUnavailable
	}
	return nil
}

func parseAnnouncementAuditRef(ref string) (int64, error) {
	auditID, err := strconv.ParseInt(ref, 10, 64)
	if err != nil || auditID < 1 || strconv.FormatInt(auditID, 10) != ref {
		return 0, lifecycle.ErrInvalid
	}
	return auditID, nil
}

func translateAdminObjectError(err error) error {
	if err == nil {
		return nil
	}
	var target error
	switch {
	case errors.Is(err, reports.ErrInvalidRequest), errors.Is(err, announcements.ErrInvalidRequest),
		errors.Is(err, issues.ErrInvalidRequest):
		target = lifecycle.ErrInvalid
	case errors.Is(err, reports.ErrUnauthorized), errors.Is(err, announcements.ErrUnauthorized),
		errors.Is(err, issues.ErrUnauthorized):
		target = lifecycle.ErrUnauthorized
	case errors.Is(err, reports.ErrForbidden), errors.Is(err, announcements.ErrForbidden),
		errors.Is(err, issues.ErrForbidden):
		target = lifecycle.ErrForbidden
	case errors.Is(err, reports.ErrNotFound), errors.Is(err, announcements.ErrNotFound),
		errors.Is(err, issues.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		target = lifecycle.ErrNotFound
	case errors.Is(err, reports.ErrConflict), errors.Is(err, announcements.ErrConflict),
		errors.Is(err, issues.ErrConflict):
		target = lifecycle.ErrConflict
	case errors.Is(err, reports.ErrInvariant):
		target = lifecycle.ErrInvariant
	case errors.Is(err, reports.ErrClosed):
		target = lifecycle.ErrClosed
	case errors.Is(err, announcements.ErrResourceLimit):
		target = lifecycle.ErrTooLarge
	case errors.Is(err, reports.ErrUnavailable), errors.Is(err, announcements.ErrUnavailable),
		errors.Is(err, issues.ErrUnavailable), errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		target = lifecycle.ErrUnavailable
	default:
		target = lifecycle.ErrUnavailable
	}
	return fmt.Errorf("%w: %v", target, err)
}

var (
	_ ReportLifecycleOwner            = (*reports.Repository)(nil)
	_ IssueAccountDeletionOwner       = (*issues.SourceAdapter)(nil)
	_ IssueRetentionOwner             = (*issues.Service)(nil)
	_ AnnouncementAuditLifecycleOwner = (*announcements.Repository)(nil)
	_ lifecycle.DeleteAdapter         = (*ReportLifecycle)(nil)
	_ lifecycle.RecoveryAdapter       = (*ReportLifecycle)(nil)
	_ lifecycle.RetentionAdapter      = (*ReportLifecycle)(nil)
	_ lifecycle.HeldObjectAdapter     = (*ReportLifecycle)(nil)
	_ lifecycle.DeleteAdapter         = (*IssueAnnouncementDelete)(nil)
	_ lifecycle.RetentionAdapter      = (*IssueRetention)(nil)
	_ lifecycle.RetentionAdapter      = (*AnnouncementAuditLifecycle)(nil)
	_ lifecycle.HeldObjectAdapter     = (*AnnouncementAuditLifecycle)(nil)
)
