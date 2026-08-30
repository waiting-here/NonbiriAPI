// Package accountstream provides the bounded, process-local shared account
// event hub used by activities and RPS. It stores no persistent event body;
// authoritative recovery always comes from the configured snapshot adapter.
package accountstream

import (
	"context"
	"encoding/json"
	"errors"
)

const (
	MaxGlobalConnections     = 512
	MaxConnectionsPerAccount = 2
	SubscriberQueueSize      = 64
	MaxRingEvents            = 256
	MaxRingBytes             = 512 * 1024
	MaxDeltaBytes            = 16 * 1024
	MaxSnapshotBytes         = 64 * 1024
)

var (
	ErrClosed             = errors.New("account event hub is closed")
	ErrCapacity           = errors.New("account event connection capacity exhausted")
	ErrInvalidEvent       = errors.New("invalid account event")
	ErrStaleIdentityEpoch = errors.New("stale account identity epoch")
	ErrSnapshot           = errors.New("authoritative account snapshot unavailable")
)

type Channel string

const (
	ChannelActivities Channel = "activities"
	ChannelRPS        Channel = "rps"
)

func (channel Channel) valid() bool {
	return channel == ChannelActivities || channel == ChannelRPS
}

type EventType string

const (
	TypeSnapshot EventType = "snapshot"
	TypeDelta    EventType = "delta"
	TypeGap      EventType = "gap"
)

type GapReason string

const (
	GapProcessRestart GapReason = "process_restart"
	GapRingExpired    GapReason = "ring_expired"
	GapRingEvicted    GapReason = "ring_evicted"
	GapSlowConsumer   GapReason = "slow_consumer"
)

// Frame is one shared SSE event. ID is serialized in the SSE id field, while
// the remaining fields form the data JSON object.
type Frame struct {
	ID            string          `json:"-"`
	Version       int             `json:"version"`
	Channel       Channel         `json:"channel"`
	Type          EventType       `json:"type"`
	Revision      *string         `json:"revision"`
	IdentityEpoch *string         `json:"identity_epoch"`
	OccurredAt    int64           `json:"occurred_at"`
	Data          json.RawMessage `json:"data"`
}

func (frame Frame) clone() Frame {
	copyFrame := frame
	copyFrame.Data = append(json.RawMessage(nil), frame.Data...)
	if frame.Revision != nil {
		revision := *frame.Revision
		copyFrame.Revision = &revision
	}
	if frame.IdentityEpoch != nil {
		epoch := *frame.IdentityEpoch
		copyFrame.IdentityEpoch = &epoch
	}
	return copyFrame
}

// PublishedEvent is supplied only after the domain transaction commits.
// Snapshot and delta payloads are complete authoritative projections; the hub
// does not merge partial state.
type PublishedEvent struct {
	Channel       Channel
	Type          EventType
	Revision      *string
	IdentityEpoch *string
	Data          json.RawMessage
}

// Snapshot is returned by the authoritative domain adapter.
type Snapshot struct {
	Revision      *string
	IdentityEpoch *string
	Data          json.RawMessage
}

// SnapshotAdapter rebuilds complete safe projections after a gap, reconnect,
// or synchronous identity purge. Implementations must honor context deadlines
// so bounded asynchronous slow-consumer recovery cannot outlive its budget.
type SnapshotAdapter interface {
	Snapshot(context.Context, int64, Channel) (Snapshot, error)
}

// IdentityEpochGuard re-reads the current RPS identity epoch. A nil epoch is
// the authoritative queue/idle/pending-result state. PublishCommitted checks
// it both before and while holding the account stream lock.
type IdentityEpochGuard interface {
	CurrentIdentityEpoch(context.Context, int64) (*string, error)
}

type SubscribeRequest struct {
	AccountID   int64
	Channels    []Channel
	LastEventID string
}
