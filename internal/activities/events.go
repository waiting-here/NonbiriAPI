package activities

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
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
func (publisher *AccountstreamPublisher) Publish(ctx context.Context, facts PublishFacts) error {
	if publisher == nil || publisher.repository == nil || publisher.sink == nil || ctx == nil {
		return ErrUnavailable
	}
	if facts.empty() {
		return nil
	}
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
	ordered := make([]int64, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

	var failures []error
	publishOne := func(id int64) {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			return
		}
		projection, err := publisher.repository.ProjectActivities(ctx, id)
		if err != nil {
			failures = append(failures, fmt.Errorf("project account %d: %w", id, err))
			return
		}
		if len(projection.Data) > accountstream.MaxDeltaBytes {
			failures = append(failures, fmt.Errorf("project account %d: %w", id, ErrResourceLimit))
			return
		}
		revision := projection.Revision
		_, err = publisher.sink.PublishActivitiesCommitted(ctx, id, plan, accountstream.PublishedEvent{
			Channel: accountstream.ChannelActivities, Type: accountstream.TypeDelta,
			Revision: &revision, IdentityEpoch: nil, Data: projection.Data,
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("publish account %d: %w", id, err))
		}
	}
	for _, id := range ordered {
		publishOne(id)
	}
	return errors.Join(failures...)
}

var _ PostCommitPublisher = (*AccountstreamPublisher)(nil)
var _ accountstream.SnapshotAdapter = (*Repository)(nil)
