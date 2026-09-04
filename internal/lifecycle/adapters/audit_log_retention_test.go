package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/announcements"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

type requestLogRetentionProbe struct {
	decisionNow int64
	limit       int
	deadline    time.Time
	result      logapi.LifecycleRetentionResult
	err         error
}

func (probe *requestLogRetentionProbe) RetainLifecycleRequestLogs(
	_ context.Context, decisionNow int64, limit int, deadline time.Time,
) (logapi.LifecycleRetentionResult, error) {
	probe.decisionNow, probe.limit, probe.deadline = decisionNow, limit, deadline
	return probe.result, probe.err
}

type maintenanceAuditRetentionProbe struct {
	order       *[]string
	decisionNow int64
	limit       int
	deadline    time.Time
	result      maintenance.LifecycleRetentionResult
	err         error
}

func (probe *maintenanceAuditRetentionProbe) RetainEvents(
	_ context.Context, decisionNow int64, limit int, deadline time.Time,
) (maintenance.LifecycleRetentionResult, error) {
	*probe.order = append(*probe.order, "maintenance")
	probe.decisionNow, probe.limit, probe.deadline = decisionNow, limit, deadline
	return probe.result, probe.err
}

type announcementAuditRetentionProbe struct {
	order       *[]string
	decisionNow int64
	limit       int
	deadline    time.Time
	result      announcements.LifecycleAuditRetentionResult
	err         error
}

func (probe *announcementAuditRetentionProbe) RetainLifecycleAudits(
	_ context.Context, decisionNow int64, limit int, deadline time.Time,
) (announcements.LifecycleAuditRetentionResult, error) {
	*probe.order = append(*probe.order, "announcements")
	probe.decisionNow, probe.limit, probe.deadline = decisionNow, limit, deadline
	return probe.result, probe.err
}

func TestRequestLogRetentionAdapterPreservesFrozenBudgetAndZeroesErrors(t *testing.T) {
	deadline := time.Unix(2_000, 0)
	probe := &requestLogRetentionProbe{result: logapi.LifecycleRetentionResult{
		RequestLogsDeleted: 3, Processed: 3, More: true,
	}}
	result, err := NewRequestLogRetention(probe).Retain(context.Background(), 1_000, 4, deadline)
	if err != nil || result != (lifecycle.WorkResult{Processed: 3, More: true}) ||
		probe.decisionNow != 1_000 || probe.limit != 4 || probe.deadline != deadline {
		t.Fatalf("request-log adapter = result:%+v probe:%+v err:%v", result, probe, err)
	}
	probe.err = logapi.ErrInvariant
	result, err = NewRequestLogRetention(probe).Retain(context.Background(), 1_000, 4, deadline)
	if !errors.Is(err, lifecycle.ErrInvariant) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("request-log adapter error = result:%+v err:%v", result, err)
	}
	probe.err = nil
	probe.result = logapi.LifecycleRetentionResult{More: true}
	if result, err = NewRequestLogRetention(probe).Retain(context.Background(), 1_000, 4, deadline); !errors.Is(err, lifecycle.ErrInvariant) || result != (lifecycle.WorkResult{}) {
		t.Fatalf("non-converging request-log result = %+v, %v", result, err)
	}
}

func TestAuditRetentionUsesStableOrderAndOneTotalBudget(t *testing.T) {
	order := []string{}
	deadline := time.Unix(3_000, 0)
	maintenanceOwner := &maintenanceAuditRetentionProbe{
		order: &order,
		result: maintenance.LifecycleRetentionResult{
			ActorsDeidentified: 1, Processed: 1,
		},
	}
	announcementOwner := &announcementAuditRetentionProbe{
		order: &order,
		result: announcements.LifecycleAuditRetentionResult{
			AuditsDeleted: 2, Processed: 2,
		},
	}
	result, err := NewAuditRetention(maintenanceOwner, announcementOwner).Retain(
		context.Background(), 2_000, 5, deadline,
	)
	if err != nil || result != (lifecycle.WorkResult{Processed: 3}) {
		t.Fatalf("combined audit retention = %+v, %v", result, err)
	}
	if len(order) != 2 || order[0] != "maintenance" || order[1] != "announcements" ||
		maintenanceOwner.decisionNow != 2_000 || maintenanceOwner.limit != 5 || maintenanceOwner.deadline != deadline ||
		announcementOwner.decisionNow != 2_000 || announcementOwner.limit != 4 || announcementOwner.deadline != deadline {
		t.Fatalf("combined audit order/budget = order:%v maintenance:%+v announcement:%+v",
			order, maintenanceOwner, announcementOwner)
	}
}

func TestAuditRetentionDrainsMaintenanceBeforeAnnouncements(t *testing.T) {
	deadline := time.Unix(4_000, 0)
	order := []string{}
	maintenanceOwner := &maintenanceAuditRetentionProbe{
		order: &order,
		result: maintenance.LifecycleRetentionResult{
			ActorsDeidentified: 1, Processed: 1, More: true,
		},
	}
	announcementOwner := &announcementAuditRetentionProbe{order: &order}
	result, err := NewAuditRetention(maintenanceOwner, announcementOwner).Retain(
		context.Background(), 3_000, 2, deadline,
	)
	if err != nil || result != (lifecycle.WorkResult{Processed: 1, More: true}) ||
		len(order) != 1 || order[0] != "maintenance" {
		t.Fatalf("maintenance-first audit retention = result:%+v order:%v err:%v", result, order, err)
	}

	order = []string{}
	maintenanceOwner.order = &order
	announcementOwner.order = &order
	maintenanceOwner.result = maintenance.LifecycleRetentionResult{EventsDeleted: 2, Processed: 2}
	result, err = NewAuditRetention(maintenanceOwner, announcementOwner).Retain(
		context.Background(), 3_000, 2, deadline,
	)
	if err != nil || result != (lifecycle.WorkResult{Processed: 2, More: true}) || len(order) != 1 {
		t.Fatalf("full maintenance budget continuation = result:%+v order:%v err:%v", result, order, err)
	}
}

func TestAuditRetentionZeroesPartialWorkOnLaterError(t *testing.T) {
	deadline := time.Unix(5_000, 0)
	order := []string{}
	maintenanceOwner := &maintenanceAuditRetentionProbe{
		order: &order,
		result: maintenance.LifecycleRetentionResult{
			ActorsDeidentified: 1, Processed: 1,
		},
	}
	announcementOwner := &announcementAuditRetentionProbe{
		order: &order,
		err:   announcements.ErrUnavailable,
	}
	result, err := NewAuditRetention(maintenanceOwner, announcementOwner).Retain(
		context.Background(), 4_000, 5, deadline,
	)
	if !errors.Is(err, lifecycle.ErrUnavailable) || result != (lifecycle.WorkResult{}) || len(order) != 2 {
		t.Fatalf("later audit owner error = result:%+v order:%v err:%v", result, order, err)
	}

	missing := []lifecycle.RetentionAdapter{
		NewRequestLogRetention(nil), NewAuditRetention(nil, announcementOwner), NewAuditRetention(maintenanceOwner, nil),
	}
	for _, adapter := range missing {
		if result, err := adapter.Retain(context.Background(), 4_000, 5, deadline); !errors.Is(err, lifecycle.ErrUnavailable) || result != (lifecycle.WorkResult{}) {
			t.Fatalf("missing retention owner = %+v, %v", result, err)
		}
	}
}
