package adapters

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/announcements"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/reports"
)

type adminObjectsReportOwner struct {
	tx                  *sql.Tx
	userID, decisionNow int64
	limit               int
	deadline            time.Time
	deleteCalls         int
	recoverCalls        int
	retainCalls         int
	inspectRef          string
	consumeRef          string
	readRef             string
	recoverResult       reports.LifecycleWorkResult
	retainResult        reports.LifecycleWorkResult
	heldState           reports.LifecycleHeldCaseState
	readExists          bool
	err                 error
}

func (owner *adminObjectsReportOwner) DetachUserForDeletion(
	_ context.Context, tx *sql.Tx, userID, decisionNow int64,
) error {
	owner.deleteCalls++
	owner.tx, owner.userID, owner.decisionNow = tx, userID, decisionNow
	return owner.err
}

func (owner *adminObjectsReportOwner) RecoverLifecycle(
	_ context.Context, decisionNow int64, limit int, deadline time.Time,
) (reports.LifecycleWorkResult, error) {
	owner.recoverCalls++
	owner.decisionNow, owner.limit, owner.deadline = decisionNow, limit, deadline
	return owner.recoverResult, owner.err
}

func (owner *adminObjectsReportOwner) RetainLifecycle(
	_ context.Context, decisionNow int64, limit int, deadline time.Time,
) (reports.LifecycleWorkResult, error) {
	owner.retainCalls++
	owner.decisionNow, owner.limit, owner.deadline = decisionNow, limit, deadline
	return owner.retainResult, owner.err
}

func (owner *adminObjectsReportOwner) InspectLifecycleHeldCase(
	_ context.Context, tx *sql.Tx, ref string, decisionNow int64,
) (reports.LifecycleHeldCaseState, error) {
	owner.tx, owner.inspectRef, owner.decisionNow = tx, ref, decisionNow
	return owner.heldState, owner.err
}

func (owner *adminObjectsReportOwner) ConsumeLifecycleHeldCaseMarker(
	_ context.Context, tx *sql.Tx, ref string,
) error {
	owner.tx, owner.consumeRef = tx, ref
	return owner.err
}

func (owner *adminObjectsReportOwner) ReadLifecycleHeldCase(
	_ context.Context, tx *sql.Tx, ref string, decisionNow int64,
) (bool, error) {
	owner.tx, owner.readRef, owner.decisionNow = tx, ref, decisionNow
	return owner.readExists, owner.err
}

func TestReportLifecycleDelegatesClosedSeams(t *testing.T) {
	owner := &adminObjectsReportOwner{
		recoverResult: reports.LifecycleWorkResult{Processed: 3, More: true},
		retainResult:  reports.LifecycleWorkResult{Processed: 2},
		heldState: reports.LifecycleHeldCaseState{
			Exists: true, OrdinaryDeadline: 500, LegalHoldConsumed: true,
		},
		readExists: true,
	}
	adapter := NewReportLifecycle(owner)
	tx := new(sql.Tx)
	request := lifecycle.DeleteRequest{UserID: 41, DecisionNow: 100}
	finalizer, err := adapter.PrepareDelete(context.Background(), tx, request)
	if err != nil || finalizer != nil || owner.deleteCalls != 1 || owner.tx != tx ||
		owner.userID != request.UserID || owner.decisionNow != request.DecisionNow {
		t.Fatalf("report delete delegation: finalizer=%v owner=%+v err=%v", finalizer, owner, err)
	}

	deadline := time.Unix(200, 0)
	recovered, err := adapter.RecoverBeforeListener(context.Background(), 101, 7, deadline)
	if err != nil || recovered != (lifecycle.WorkResult{Processed: 3, More: true}) ||
		owner.recoverCalls != 1 || owner.decisionNow != 101 || owner.limit != 7 || owner.deadline != deadline {
		t.Fatalf("report recovery delegation: result=%+v owner=%+v err=%v", recovered, owner, err)
	}
	retained, err := adapter.Retain(context.Background(), 102, 5, deadline)
	if err != nil || retained != (lifecycle.WorkResult{Processed: 2}) ||
		owner.retainCalls != 1 || owner.decisionNow != 102 || owner.limit != 5 {
		t.Fatalf("report retention delegation: result=%+v owner=%+v err=%v", retained, owner, err)
	}

	state, err := adapter.InspectForCreate(context.Background(), tx, "rpc_case", 103)
	if err != nil || state != (lifecycle.HeldObjectState{
		Exists: true, OrdinaryDeadline: 500, LegalHoldConsumed: true,
	}) || owner.inspectRef != "rpc_case" {
		t.Fatalf("report hold inspect: state=%+v owner=%+v err=%v", state, owner, err)
	}
	if err := adapter.ConsumeMarker(context.Background(), tx, "rpc_case"); err != nil || owner.consumeRef != "rpc_case" {
		t.Fatalf("report hold marker: owner=%+v err=%v", owner, err)
	}
	exists, err := adapter.ReadHeld(context.Background(), tx, "rpc_case", 104)
	if err != nil || !exists || owner.readRef != "rpc_case" || owner.decisionNow != 104 {
		t.Fatalf("report held read: exists=%v owner=%+v err=%v", exists, owner, err)
	}
}

type adminObjectsIssueDeleteOwner struct {
	tx                  *sql.Tx
	userID, decisionNow int64
	calls               int
	err                 error
}

func (owner *adminObjectsIssueDeleteOwner) PrepareAccountDeletion(
	_ context.Context, tx *sql.Tx, userID, decisionNow int64,
) error {
	owner.calls++
	owner.tx, owner.userID, owner.decisionNow = tx, userID, decisionNow
	return owner.err
}

type adminObjectsIssueRetentionOwner struct {
	decisionNow int64
	limit       int
	deadline    time.Time
	result      issues.RetentionResult
	err         error
}

func (owner *adminObjectsIssueRetentionOwner) RetainLifecycleClosed(
	_ context.Context, decisionNow int64, limit int, deadline time.Time,
) (issues.RetentionResult, error) {
	owner.decisionNow, owner.limit, owner.deadline = decisionNow, limit, deadline
	return owner.result, owner.err
}

func TestIssueAnnouncementDeleteAndIssueRetention(t *testing.T) {
	deleteOwner := &adminObjectsIssueDeleteOwner{}
	deleteAdapter := NewIssueAnnouncementDelete(deleteOwner)
	tx := new(sql.Tx)
	request := lifecycle.DeleteRequest{UserID: 77, DecisionNow: 800}
	finalizer, err := deleteAdapter.PrepareDelete(context.Background(), tx, request)
	if err != nil || finalizer != nil || deleteOwner.calls != 1 || deleteOwner.tx != tx ||
		deleteOwner.userID != request.UserID || deleteOwner.decisionNow != request.DecisionNow {
		t.Fatalf("issue/announcement delete delegation: finalizer=%v owner=%+v err=%v", finalizer, deleteOwner, err)
	}

	deadline := time.Unix(900, 0)
	retentionOwner := &adminObjectsIssueRetentionOwner{result: issues.RetentionResult{ClosedDeleted: 4}}
	retained, err := NewIssueRetention(retentionOwner).Retain(context.Background(), 801, 4, deadline)
	if err != nil || retained != (lifecycle.WorkResult{Processed: 4, More: true}) ||
		retentionOwner.decisionNow != 801 || retentionOwner.limit != 4 || retentionOwner.deadline != deadline {
		t.Fatalf("issue retention delegation: result=%+v owner=%+v err=%v", retained, retentionOwner, err)
	}
}

type adminObjectsAnnouncementOwner struct {
	auditID     int64
	decisionNow int64
	limit       int
	deadline    time.Time
	result      announcements.LifecycleAuditRetentionResult
	state       announcements.LifecycleHeldAuditState
	readExists  bool
	err         error
}

func (owner *adminObjectsAnnouncementOwner) RetainLifecycleAudits(
	_ context.Context, decisionNow int64, limit int, deadline time.Time,
) (announcements.LifecycleAuditRetentionResult, error) {
	owner.decisionNow, owner.limit, owner.deadline = decisionNow, limit, deadline
	return owner.result, owner.err
}

func (owner *adminObjectsAnnouncementOwner) InspectLifecycleHeldAudit(
	_ context.Context, _ *sql.Tx, auditID, decisionNow int64,
) (announcements.LifecycleHeldAuditState, error) {
	owner.auditID, owner.decisionNow = auditID, decisionNow
	return owner.state, owner.err
}

func (owner *adminObjectsAnnouncementOwner) ConsumeLifecycleHeldAuditMarker(
	_ context.Context, _ *sql.Tx, auditID int64,
) error {
	owner.auditID = auditID
	return owner.err
}

func (owner *adminObjectsAnnouncementOwner) ReadLifecycleHeldAudit(
	_ context.Context, _ *sql.Tx, auditID, decisionNow int64,
) (bool, error) {
	owner.auditID, owner.decisionNow = auditID, decisionNow
	return owner.readExists, owner.err
}

func TestAnnouncementAuditLifecycleIsContentFreeAndCanonical(t *testing.T) {
	owner := &adminObjectsAnnouncementOwner{
		result: announcements.LifecycleAuditRetentionResult{Processed: 6, More: true},
		state: announcements.LifecycleHeldAuditState{
			Exists: true, OrdinaryDeadline: 1200, LegalHoldConsumed: true,
		},
		readExists: true,
	}
	adapter := NewAnnouncementAuditLifecycle(owner)
	deadline := time.Unix(1100, 0)
	retained, err := adapter.Retain(context.Background(), 1000, 6, deadline)
	if err != nil || retained != (lifecycle.WorkResult{Processed: 6, More: true}) ||
		owner.decisionNow != 1000 || owner.limit != 6 || owner.deadline != deadline {
		t.Fatalf("announcement retention delegation: result=%+v owner=%+v err=%v", retained, owner, err)
	}

	tx := new(sql.Tx)
	state, err := adapter.InspectForCreate(context.Background(), tx, "42", 1001)
	if err != nil || owner.auditID != 42 || state != (lifecycle.HeldObjectState{
		Exists: true, OrdinaryDeadline: 1200, LegalHoldConsumed: true,
	}) {
		t.Fatalf("announcement hold inspect: state=%+v owner=%+v err=%v", state, owner, err)
	}
	if err := adapter.ConsumeMarker(context.Background(), tx, "42"); err != nil || owner.auditID != 42 {
		t.Fatalf("announcement hold marker: owner=%+v err=%v", owner, err)
	}
	exists, err := adapter.ReadHeld(context.Background(), tx, "42", 1002)
	if err != nil || !exists || owner.auditID != 42 || owner.decisionNow != 1002 {
		t.Fatalf("announcement held read: exists=%v owner=%+v err=%v", exists, owner, err)
	}

	for _, ref := range []string{"", "0", "01", "+1", "-1", " 1", "9223372036854775808"} {
		if _, err := adapter.InspectForCreate(context.Background(), tx, ref, 1003); !errors.Is(err, lifecycle.ErrInvalid) {
			t.Fatalf("non-canonical audit ref %q err=%v, want invalid", ref, err)
		}
	}
}

func TestAdminObjectAdapterValidationAndTranslation(t *testing.T) {
	deadline := time.Unix(100, 0)
	owner := &adminObjectsReportOwner{err: reports.ErrInvariant}
	adapter := NewReportLifecycle(owner)
	if _, err := adapter.RecoverBeforeListener(context.Background(), 1, 1, deadline); !errors.Is(err, lifecycle.ErrInvariant) {
		t.Fatalf("report invariant translation err=%v", err)
	}
	if _, err := (*ReportLifecycle)(nil).Retain(context.Background(), 1, 1, deadline); !errors.Is(err, lifecycle.ErrUnavailable) {
		t.Fatalf("nil report adapter err=%v", err)
	}
	if _, err := adapter.Retain(context.Background(), 1, 0, deadline); !errors.Is(err, lifecycle.ErrInvalid) {
		t.Fatalf("invalid worker limit err=%v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewIssueRetention(&adminObjectsIssueRetentionOwner{}).Retain(canceled, 1, 1, deadline); !errors.Is(err, lifecycle.ErrUnavailable) {
		t.Fatalf("canceled worker context err=%v", err)
	}
}
