package debug

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type debugTestClock struct{ unix atomic.Int64 }

func newDebugTestClock(unix int64) *debugTestClock {
	clock := &debugTestClock{}
	clock.unix.Store(unix)
	return clock
}

func (clock *debugTestClock) Now() time.Time { return time.Unix(clock.unix.Load(), 0).UTC() }
func (clock *debugTestClock) Advance(delta time.Duration) {
	clock.unix.Add(int64(delta / time.Second))
}

type debugTestIDs struct {
	mu      sync.Mutex
	next    uint64
	current map[string]struct{}
}

func newDebugTestIDs() *debugTestIDs {
	return &debugTestIDs{next: 1, current: make(map[string]struct{})}
}

func (ids *debugTestIDs) opaque(prefix string) (string, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	value := testOpaqueID(prefix, ids.next)
	ids.next++
	return value, nil
}

func (ids *debugTestIDs) event() (string, error) {
	value, err := ids.opaque("dbe_")
	if err == nil {
		ids.mu.Lock()
		ids.current[value] = struct{}{}
		ids.mu.Unlock()
	}
	return value, err
}

func (ids *debugTestIDs) isCurrent(value string) bool {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	_, ok := ids.current[value]
	return ok
}

func testOpaqueID(prefix string, sequence uint64) string {
	var raw [16]byte
	copy(raw[:8], []byte("nonbiri!"))
	binary.BigEndian.PutUint64(raw[8:], sequence)
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:])
}

type debugTestVerifier struct {
	mu        sync.Mutex
	state     IdentityState
	err       error
	calls     int
	blockOnce bool
	entered   chan struct{}
	release   chan struct{}
}

func (verifier *debugTestVerifier) VerifyDebugIdentity(ctx context.Context, _ int64, _ string) (IdentityState, error) {
	verifier.mu.Lock()
	verifier.calls++
	state, err := verifier.state, verifier.err
	block := verifier.blockOnce
	if block {
		verifier.blockOnce = false
	}
	entered, release := verifier.entered, verifier.release
	verifier.mu.Unlock()
	if block {
		close(entered)
		select {
		case <-ctx.Done():
			return IdentityUncertain, ctx.Err()
		case <-release:
		}
	}
	return state, err
}

func (verifier *debugTestVerifier) set(state IdentityState, err error) {
	verifier.mu.Lock()
	verifier.state, verifier.err = state, err
	verifier.mu.Unlock()
}

func (verifier *debugTestVerifier) callCount() int {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	return verifier.calls
}

func newDebugTestHub(t *testing.T, clock *debugTestClock, verifier IdentityVerifier) (*Hub, *debugTestIDs) {
	t.Helper()
	ids := newDebugTestIDs()
	config := hubConfig{
		now: clock.Now, newOpaqueID: ids.opaque, newEventID: ids.event,
		eventIDIsCurrent: ids.isCurrent, verifier: verifier,
		ringAge: ringAge, heartbeat: heartbeatEvery, writeTimeout: writeTimeout,
		disableSweeper: true,
	}
	hub, err := newHub(config)
	if err != nil {
		t.Fatalf("newHub: %v", err)
	}
	t.Cleanup(func() {
		if err := hub.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return hub, ids
}

func mustStartDebug(t *testing.T, hub *Hub, userID int64, binding string) DebugSessionMetadata {
	t.Helper()
	metadata, created, err := hub.Start(userID, binding)
	if err != nil || !created {
		t.Fatalf("Start = (%+v,%v,%v)", metadata, created, err)
	}
	return metadata
}

func mustNextDebug(t *testing.T, subscription *Subscription) EventEnvelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := subscription.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return event
}

func decodeDebugData[T any](t *testing.T, event EventEnvelope) T {
	t.Helper()
	var value T
	if err := decodeClosedJSON(event.Data, &value); err != nil {
		t.Fatalf("decode %s: %v\n%s", event.Kind, err, event.Data)
	}
	return value
}

func marshalDebugGolden(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	return append(encoded, '\n')
}

func assertDebugGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v\n--- got ---\n%s", name, err, got)
	}
	if string(got) != string(want) {
		t.Fatalf("golden %s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func newDebugMutationDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	database.SetMaxOpenConns(1)
	_, err = database.Exec(`CREATE TABLE idempotency_records(
 scope TEXT NOT NULL, actor_scope_hash BLOB NOT NULL, key_hash BLOB NOT NULL,
 request_hash BLOB NOT NULL, lookup_fingerprint BLOB, state TEXT NOT NULL,
 http_status INTEGER NOT NULL, response_body BLOB NOT NULL, created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL, PRIMARY KEY(scope,actor_scope_hash,key_hash))`)
	if err != nil {
		_ = database.Close()
		t.Fatalf("create idempotency_records: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("database Close: %v", err)
		}
	})
	return database
}

func requireErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func noForbiddenDebugWire(t *testing.T, encoded []byte, forbidden ...string) {
	t.Helper()
	lower := strings.ToLower(string(encoded))
	for _, value := range forbidden {
		if strings.Contains(lower, strings.ToLower(value)) {
			t.Fatalf("forbidden value %q in wire: %s", value, encoded)
		}
	}
}

func debugMutationKey(seed byte) string { return strings.Repeat(string(seed), 22) }

func waitDebug(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for debug test synchronization")
	}
}

var debugTestSequence atomic.Uint64

func uniqueDebugLabel(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, debugTestSequence.Add(1))
}
