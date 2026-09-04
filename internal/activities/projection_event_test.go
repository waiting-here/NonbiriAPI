package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
)

type recordedActivityEvent struct {
	accountID int64
	plan      accountstream.ActivitiesPublishPlan
	event     accountstream.PublishedEvent
}

type recordingActivitySink struct {
	mu         sync.Mutex
	plan       accountstream.ActivitiesPublishPlan
	prepareErr error
	publishErr error
	prepares   []bool
	events     []recordedActivityEvent
}

func cloneActivitiesPublishPlan(plan accountstream.ActivitiesPublishPlan) accountstream.ActivitiesPublishPlan {
	plan.ActiveAccountIDs = append([]int64(nil), plan.ActiveAccountIDs...)
	return plan
}

func (sink *recordingActivitySink) PrepareActivitiesPublish(global bool) (accountstream.ActivitiesPublishPlan, error) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.prepares = append(sink.prepares, global)
	if sink.prepareErr != nil {
		return accountstream.ActivitiesPublishPlan{}, sink.prepareErr
	}
	plan := cloneActivitiesPublishPlan(sink.plan)
	if plan.Generation == 0 {
		plan.Generation = 1
	}
	return plan, nil
}

func (sink *recordingActivitySink) PublishActivitiesCommitted(
	_ context.Context,
	accountID int64,
	plan accountstream.ActivitiesPublishPlan,
	event accountstream.PublishedEvent,
) (accountstream.Frame, error) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	recordedEvent := event
	recordedEvent.Data = append(json.RawMessage(nil), event.Data...)
	if event.Revision != nil {
		revision := *event.Revision
		recordedEvent.Revision = &revision
	}
	sink.events = append(sink.events, recordedActivityEvent{
		accountID: accountID,
		plan:      cloneActivitiesPublishPlan(plan),
		event:     recordedEvent,
	})
	if sink.publishErr != nil {
		return accountstream.Frame{}, sink.publishErr
	}
	return accountstream.Frame{}, nil
}

var _ AccountEventSink = (*recordingActivitySink)(nil)

func TestActivitiesProjectionAndGlobalPublisherAreCompleteAndPrivate(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_100_000)
	first, _ := fixture.seedUser("event-first", false)
	second, _ := fixture.seedUser("event-second", false)
	inactive, _ := fixture.seedUser("event-inactive", false)
	fixture.setActivityConfig(true, true, false, 0, 0)
	projection, err := fixture.repository.ProjectActivities(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Revision == "" || len(projection.Data) > accountstream.MaxSnapshotBytes || !json.Valid(projection.Data) {
		t.Fatalf("projection revision=%q bytes=%d", projection.Revision, len(projection.Data))
	}
	for _, forbidden := range [][]byte{[]byte("participant_ref"), []byte("operation_id"), []byte("actor"), []byte("source")} {
		if bytes.Contains(projection.Data, forbidden) {
			t.Fatalf("projection leaked %q: %s", forbidden, projection.Data)
		}
	}
	if _, err := fixture.repository.Snapshot(context.Background(), first, accountstream.ChannelRPS); !errors.Is(err, accountstream.ErrSnapshot) {
		t.Fatalf("wrong-channel snapshot error = %v", err)
	}
	plan := accountstream.ActivitiesPublishPlan{
		Generation:       37,
		ActiveAccountIDs: []int64{second, second},
	}
	sink := &recordingActivitySink{plan: plan}
	publisher, err := NewAccountstreamPublisher(fixture.repository, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), PublishFacts{Global: true, AccountIDs: []int64{first}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sink.prepares, []bool{true}) {
		t.Fatalf("prepare calls = %v", sink.prepares)
	}
	if len(sink.events) != 2 || sink.events[0].accountID != first || sink.events[1].accountID != second {
		t.Fatalf("published events = %+v; inactive account %d must not be enumerated", sink.events, inactive)
	}
	for _, recorded := range sink.events {
		if recorded.plan.Generation != plan.Generation || !slices.Equal(recorded.plan.ActiveAccountIDs, plan.ActiveAccountIDs) {
			t.Fatalf("published plan = %+v, want %+v", recorded.plan, plan)
		}
		if recorded.event.Channel != accountstream.ChannelActivities || recorded.event.Type != accountstream.TypeDelta ||
			recorded.event.Revision == nil || *recorded.event.Revision == "" || recorded.event.IdentityEpoch != nil ||
			len(recorded.event.Data) > accountstream.MaxDeltaBytes {
			t.Fatalf("invalid activity event = %+v", recorded.event)
		}
		var snapshot ActivitiesSnapshot
		if err := json.Unmarshal(recorded.event.Data, &snapshot); err != nil {
			t.Fatalf("event is not complete snapshot: %v", err)
		}
	}
}

func TestActivitiesPublisherUsesCurrentPlanForNonGlobalFacts(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_150_000)
	explicit, _ := fixture.seedUser("event-explicit", false)
	activeOnly, _ := fixture.seedUser("event-active-only", false)
	plan := accountstream.ActivitiesPublishPlan{
		Generation:       51,
		ActiveAccountIDs: []int64{activeOnly},
	}
	sink := &recordingActivitySink{plan: plan}
	publisher, err := NewAccountstreamPublisher(fixture.repository, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), PublishFacts{AccountIDs: []int64{explicit, explicit}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sink.prepares, []bool{false}) || len(sink.events) != 1 || sink.events[0].accountID != explicit {
		t.Fatalf("non-global prepares=%v events=%+v", sink.prepares, sink.events)
	}
	if sink.events[0].plan.Generation != plan.Generation || !slices.Equal(sink.events[0].plan.ActiveAccountIDs, plan.ActiveAccountIDs) {
		t.Fatalf("non-global plan=%+v want=%+v", sink.events[0].plan, plan)
	}
}

type invalidatingActivitySink struct {
	mu            sync.Mutex
	hub           *accountstream.Hub
	preparedPlan  accountstream.ActivitiesPublishPlan
	publishedPlan accountstream.ActivitiesPublishPlan
}

func (sink *invalidatingActivitySink) PrepareActivitiesPublish(global bool) (accountstream.ActivitiesPublishPlan, error) {
	plan, err := sink.hub.PrepareActivitiesPublish(global)
	if err != nil {
		return accountstream.ActivitiesPublishPlan{}, err
	}
	sink.mu.Lock()
	sink.preparedPlan = cloneActivitiesPublishPlan(plan)
	sink.mu.Unlock()
	return plan, nil
}

func (sink *invalidatingActivitySink) PublishActivitiesCommitted(
	ctx context.Context,
	accountID int64,
	plan accountstream.ActivitiesPublishPlan,
	event accountstream.PublishedEvent,
) (accountstream.Frame, error) {
	sink.mu.Lock()
	sink.publishedPlan = cloneActivitiesPublishPlan(plan)
	sink.mu.Unlock()
	if _, err := sink.hub.PrepareActivitiesPublish(true); err != nil {
		return accountstream.Frame{}, err
	}
	return sink.hub.PublishActivitiesCommitted(ctx, accountID, plan, event)
}

func TestActivitiesPublisherReturnsStalePlanError(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_175_000)
	userID, _ := fixture.seedUser("event-error", false)
	hub, err := accountstream.New(fixture.repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	sink := &invalidatingActivitySink{hub: hub}
	publisher, err := NewAccountstreamPublisher(fixture.repository, sink)
	if err != nil {
		t.Fatal(err)
	}
	err = publisher.Publish(context.Background(), PublishFacts{AccountIDs: []int64{userID}})
	if !errors.Is(err, accountstream.ErrStaleActivitiesGeneration) {
		t.Fatalf("stale plan publish error=%v", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.preparedPlan.Generation == 0 || sink.publishedPlan.Generation != sink.preparedPlan.Generation ||
		!slices.Equal(sink.publishedPlan.ActiveAccountIDs, sink.preparedPlan.ActiveAccountIDs) {
		t.Fatalf("prepared plan=%+v published plan=%+v", sink.preparedPlan, sink.publishedPlan)
	}
}

func TestActivitiesPublisherReturnsSinkFailures(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_180_000)
	userID, _ := fixture.seedUser("event-sink-error", false)
	sinkFailure := errors.New("activity sink failed")
	for _, test := range []struct {
		name          string
		prepareErr    error
		publishErr    error
		publishedCall bool
	}{
		{name: "prepare_failure", prepareErr: sinkFailure},
		{name: "publish_failure", publishErr: sinkFailure, publishedCall: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			sink := &recordingActivitySink{
				plan:       accountstream.ActivitiesPublishPlan{Generation: 73},
				prepareErr: test.prepareErr,
				publishErr: test.publishErr,
			}
			publisher, err := NewAccountstreamPublisher(fixture.repository, sink)
			if err != nil {
				t.Fatal(err)
			}
			err = publisher.Publish(context.Background(), PublishFacts{AccountIDs: []int64{userID}})
			if !errors.Is(err, sinkFailure) {
				t.Fatalf("publish error=%v, want %v", err, sinkFailure)
			}
			if !slices.Equal(sink.prepares, []bool{false}) || (len(sink.events) == 1) != test.publishedCall {
				t.Fatalf("failed publish prepares=%v events=%+v", sink.prepares, sink.events)
			}
			if test.publishedCall && sink.events[0].plan.Generation != 73 {
				t.Fatalf("failed publish plan=%+v", sink.events[0].plan)
			}
		})
	}
}

type recordingPublishReporter struct {
	mu    sync.Mutex
	count int
}

func (reporter *recordingPublishReporter) ReportActivitiesPublishError(error) {
	reporter.mu.Lock()
	reporter.count++
	reporter.mu.Unlock()
}

func TestPostCommitPublishFailureDoesNotRollbackMutation(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_200_000)
	user, _ := fixture.seedUser("publish-failure", false)
	fixture.fundUser(user, -1)
	var welfarePool string
	_ = fixture.store.DB().QueryRow(`SELECT id FROM shared_pools WHERE pool_type='welfare'`).Scan(&welfarePool)
	fixture.fundPool(welfarePool, 1000)
	fixture.setActivityConfig(true, true, false, 0, 100)
	sink := &recordingActivitySink{publishErr: errors.New("sink unavailable")}
	publisher, _ := NewAccountstreamPublisher(fixture.repository, sink)
	reporter := &recordingPublishReporter{}
	service, err := NewService(ServiceConfig{Repository: fixture.repository, Publisher: publisher, Reporter: reporter})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ClaimWelfare(context.Background(), user, fixture.control("POST", routeWelfareClaims, nil))
	if err != nil || result.Value.Awarded != "0.1" {
		t.Fatalf("mutation result=%+v err=%v", result, err)
	}
	var claims int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM welfare_claims WHERE user_id=?`, user).Scan(&claims); err != nil || claims != 1 {
		t.Fatalf("committed claims=%d err=%v", claims, err)
	}
	if reporter.count != 1 {
		t.Fatalf("publish failures reported=%d", reporter.count)
	}
}
