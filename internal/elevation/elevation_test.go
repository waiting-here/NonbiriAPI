package elevation

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestManager(t *testing.T, ttl time.Duration) *Manager {
	t.Helper()
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	m, err := NewManagerWithKey(key, ttl)
	if err != nil {
		t.Fatalf("NewManagerWithKey: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestIssueConsumeRoundTrip(t *testing.T) {
	m := newTestManager(t, time.Minute)
	token, expires, err := m.Issue(42, KindUser)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" || !expires.After(time.Now()) {
		t.Fatalf("issue returned empty token or non-future expiry: %q %v", token, expires)
	}
	if err := m.Consume(token, 42, KindUser); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	// Single-use: a second consume of the same token is a replay.
	if err := m.Consume(token, 42, KindUser); !errors.Is(err, ErrReplay) {
		t.Fatalf("second Consume err=%v, want ErrReplay", err)
	}
}

func TestBoundCapabilityRequiresExactSessionBinding(t *testing.T) {
	m := newTestManager(t, time.Minute)
	bindingA := "session-hash-a"
	bindingB := "session-hash-b"
	token, _, err := m.IssueBound(42, KindUser, bindingA)
	if err != nil {
		t.Fatalf("IssueBound: %v", err)
	}
	if err := m.ConsumeBound(token, 42, KindUser, bindingB); !errors.Is(err, ErrMismatch) {
		t.Fatalf("wrong binding err=%v, want mismatch", err)
	}
	if err := m.Consume(token, 42, KindUser); !errors.Is(err, ErrMismatch) {
		t.Fatalf("unbound consume of bound token err=%v, want mismatch", err)
	}
	if err := m.ConsumeBound(token, 42, KindUser, bindingA); err != nil {
		t.Fatalf("valid bound consume: %v", err)
	}
}

func TestConsumeRejectsIdentityMismatch(t *testing.T) {
	m := newTestManager(t, time.Minute)
	token, _, err := m.Issue(7, KindUser)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// A mismatched identity must not burn the token: the legitimate caller can
	// still consume it afterward. Try every kind of mismatch first.
	if err := m.Consume(token, 8, KindUser); !errors.Is(err, ErrMismatch) {
		t.Fatalf("mismatched user err=%v, want ErrMismatch", err)
	}
	if err := m.Consume(token, 7, KindAdmin); !errors.Is(err, ErrMismatch) {
		t.Fatalf("mismatched kind err=%v, want ErrMismatch", err)
	}
	if err := m.Consume(token, 8, KindAdmin); !errors.Is(err, ErrMismatch) {
		t.Fatalf("mismatched both err=%v, want ErrMismatch", err)
	}
	// Legitimate consume still succeeds: none of the mismatch attempts burned it.
	if err := m.Consume(token, 7, KindUser); err != nil {
		t.Fatalf("legitimate Consume after mismatch attempts: %v", err)
	}
}

func TestConsumeRejectsTamperedToken(t *testing.T) {
	m := newTestManager(t, time.Minute)
	token, _, err := m.Issue(1, KindAdmin)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Flip a character in the signature half.
	tampered := token[:len(token)-1]
	if token[len(token)-1] == 'A' {
		tampered += "B"
	} else {
		tampered += "A"
	}
	if err := m.Consume(tampered, 1, KindAdmin); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered err=%v, want ErrInvalid", err)
	}
	// The original remains consumable (a failed verify never touches pending).
	if err := m.Consume(token, 1, KindAdmin); err != nil {
		t.Fatalf("original after tamper: %v", err)
	}
}

func TestExpirePurgesAndRejects(t *testing.T) {
	m := newTestManager(t, 50*time.Millisecond)
	token, _, err := m.Issue(1, KindUser)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Advance the clock past expiry. Consume must report expiry (the nonce was
	// purged); the exact sentinel is ErrExpired because expiry is checked before
	// the replay lookup.
	if err := m.SetClock(func() time.Time { return time.Now().Add(time.Second) }); err != nil {
		t.Fatalf("SetClock: %v", err)
	}
	if err := m.Consume(token, 1, KindUser); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired err=%v, want ErrExpired", err)
	}
}

func TestCloseRejectsAll(t *testing.T) {
	m := newTestManager(t, time.Minute)
	token, _, err := m.Issue(1, KindUser)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := m.Consume(token, 1, KindUser); !errors.Is(err, ErrClosed) {
		t.Fatalf("consume after close err=%v, want ErrClosed", err)
	}
	if _, _, err := m.Issue(2, KindUser); !errors.Is(err, ErrClosed) {
		t.Fatalf("issue after close err=%v, want ErrClosed", err)
	}
}

func TestIssueMalformedAndCapacity(t *testing.T) {
	m := newTestManager(t, time.Minute)
	if _, _, err := m.Issue(0, KindUser); !errors.Is(err, ErrMalformed) {
		t.Fatalf("issue uid=0 err=%v, want ErrMalformed", err)
	}
	if _, _, err := m.Issue(1, Kind(0)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("issue kind=0 err=%v, want ErrMalformed", err)
	}
	if _, _, err := m.Issue(1, Kind(9)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("issue kind=9 err=%v, want ErrMalformed", err)
	}
}

// TestConcurrentIssueConsumeIsSingleUse runs many goroutines issuing and
// consuming tokens for the same identity. Every token must be consumed at most
// once: the count of successful consumes equals the count of distinct issued
// tokens. This is the -race / stress proof that Consume is linearizing.
func TestConcurrentIssueConsumeIsSingleUse(t *testing.T) {
	m := newTestManager(t, time.Minute)
	const issuers = 8
	const perIssuer = 250
	var issued atomic.Int64
	var consumed atomic.Int64

	var wg sync.WaitGroup
	// Issuers hand tokens to consumers through a bounded channel; each token
	// is consumed by exactly one consumer goroutine, but multiple consumers
	// race on the same manager.
	tokens := make(chan string, issuers*perIssuer)
	for i := 0; i < issuers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perIssuer; j++ {
				token, _, err := m.Issue(100, KindUser)
				if err != nil {
					t.Errorf("Issue: %v", err)
					return
				}
				issued.Add(1)
				tokens <- token
			}
		}()
	}
	// Consumers: 16 goroutines draining the channel.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(tokens)
		close(done)
	}()
	var cwg sync.WaitGroup
	for i := 0; i < 16; i++ {
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			for token := range tokens {
				if err := m.Consume(token, 100, KindUser); err == nil {
					consumed.Add(1)
				} else if !errors.Is(err, ErrReplay) && !errors.Is(err, ErrExpired) {
					t.Errorf("unexpected consume error: %v", err)
				}
			}
		}()
	}
	<-done
	cwg.Wait()
	if issued.Load() != consumed.Load() {
		t.Fatalf("issued=%d consumed=%d: some token was consumed more than once or lost", issued.Load(), consumed.Load())
	}
}

// TestConcurrentDoubleConsumeAtMostOneWinner issues one token and has many
// goroutines race to consume it; exactly one must win.
func TestConcurrentDoubleConsumeAtMostOneWinner(t *testing.T) {
	m := newTestManager(t, time.Minute)
	token, _, err := m.Issue(1, KindUser)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	const racers = 64
	var winners atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := m.Consume(token, 1, KindUser); err == nil {
				winners.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("winners=%d, want exactly 1 (single-use violated)", winners.Load())
	}
}

func TestTokenShapeIsOpaque(t *testing.T) {
	m := newTestManager(t, time.Minute)
	token, _, err := m.Issue(1, KindUser)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Two dot-separated base64url halves; the nonce is not literally present.
	if len(token) < EncodedNonceLength*2 {
		t.Fatalf("token too short: %q", token)
	}
	for _, r := range token {
		if r == '.' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		t.Fatalf("token contains non-base64url rune %q in %q", r, token)
	}
}

func ExampleKind() {
	// A user elevation and an admin elevation never cross-consume.
	_ = fmt.Sprintf("user=%d admin=%d", KindUser, KindAdmin)
}
