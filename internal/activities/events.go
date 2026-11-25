package activities

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type AccountstreamPublisher struct {
	repository *Repository
	sink       AccountEventSink
}

func NewAccountstreamPublisher(repository *Repository, sink AccountEventSink) (*AccountstreamPublisher, error) {
	if repository == nil || isNilInterface(sink) {
		return nil, errors.New("activities: repository and account event sink are required")
	}
	return &AccountstreamPublisher{repository: repository, sink: sink}, nil
}

// Publish consumes only committed safe facts and rebuilds one complete
// projection per account. Individual failures do not stop other accounts from
// receiving their authoritative replacement snapshot.
func (publisher *AccountstreamPublisher) Publish(ctx context.Context, facts PublishFacts) (resultErr error) {
	if publisher == nil || publisher.repository == nil || publisher.sink == nil || ctx == nil {
		return ErrUnavailable
	}
	if facts.empty() {
		return nil
	}
	// The change is already committed and can affect other subscribers.
	ctx = context.WithoutCancel(ctx)
	finish := observability.DatabaseOperation(ctx, "activities_publish")
	defer func() { finish(resultErr) }()
	ids := make(map[int64]struct{}, len(facts.AccountIDs))
	for _, id := range facts.AccountIDs {
		if id <= 0 {
			return ErrInvalidRequest
		}
		ids[id] = struct{}{}
	}
	plan, err := publisher.sink.PrepareActivitiesPublish(facts.Global)
	if err != nil {
		return fmt.Errorf("prepare activities publish: %w", err)
	}
	if facts.Global {
		for _, id := range plan.ActiveAccountIDs {
			if id <= 0 {
				return ErrInvalidRequest
			}
			ids[id] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ordered := make([]int64, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

	var failures []error
	publishOne := func(id int64) error {
		projection, err := publisher.repository.ProjectActivities(ctx, id)
		if err != nil {
			return fmt.Errorf("project account %d: %w", id, err)
		}
		if len(projection.Data) > accountstream.MaxDeltaBytes {
			return fmt.Errorf("project account %d: %w", id, ErrResourceLimit)
		}
		revision := projection.Revision
		_, err = publisher.sink.PublishActivitiesCommitted(ctx, id, plan, accountstream.PublishedEvent{
			Channel: accountstream.ChannelActivities, Type: accountstream.TypeDelta,
			Revision: &revision, IdentityEpoch: nil, Data: projection.Data,
		})
		if err != nil {
			return fmt.Errorf("publish account %d: %w", id, err)
		}
		return nil
	}
	for _, id := range ordered {
		if ctx.Err() != nil {
			failures = append(failures, ctx.Err())
			break
		}
		err := publishOne(id)
		if errors.Is(err, accountstream.ErrStaleActivitiesGeneration) {
			// A newer global change overtook our read. Rebuild from the database
			// against its generation without invalidating anyone else's events.
			latest, planErr := publisher.sink.PrepareActivitiesPublish(false)
			if planErr != nil {
				err = fmt.Errorf("refresh activities publish plan: %w", planErr)
			} else {
				plan = latest
				err = publishOne(id)
			}
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

var _ PostCommitPublisher = (*AccountstreamPublisher)(nil)
var _ accountstream.SnapshotAdapter = (*Repository)(nil)
