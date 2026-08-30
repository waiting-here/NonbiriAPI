package resourcebridge

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestNewDerivesOnceClearsAliasAndCloseClearsRuntimeKey(t *testing.T) {
	fixture := newBridgeFixture(t)
	calls, alias, info := fixture.vault.derivationSnapshot()
	if calls != 1 {
		t.Fatalf("derivation calls = %d, want 1", calls)
	}
	if len(alias) != len(fixture.runtime.fingerprintKey) || !allZero(alias) {
		t.Fatal("constructor retained or failed to clear derivation alias")
	}
	if string(info) != fingerprintSubkeyInfo {
		t.Fatalf("derivation info = %q, want %q", info, fingerprintSubkeyInfo)
	}
	if allZero(fixture.runtime.fingerprintKey[:]) {
		t.Fatal("runtime fingerprint key is unexpectedly zero before Close")
	}
	if err := fixture.runtime.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := fixture.runtime.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !allZero(fixture.runtime.fingerprintKey[:]) {
		t.Fatal("Close did not clear runtime fingerprint key")
	}
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	plaintext := []byte("clear-even-after-close")
	_, err = fixture.runtime.WriteEndpointSecret(context.Background(), tx, resources.SecretWriteInput{
		CanonicalBaseURL: "https://closed.example.test/v1",
		ConnectorType:    "openai-compatible",
		Plaintext:        plaintext,
		CreatedAt:        bridgeTestNow,
	})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("WriteEndpointSecret after Close error = %v, want ErrClosed", err)
	}
	if !allZero(plaintext) {
		t.Fatal("closed runtime did not clear transferred plaintext")
	}
}

func TestNewRejectsMissingAndFailedDependencies(t *testing.T) {
	fixture := newBridgeFixture(t)
	base := Config{
		Store: fixture.store, Vault: fixture.vault, Claims: fixture.claims, Backend: fixture.backend,
		Now: func() time.Time { return time.Unix(bridgeTestNow, 0) },
	}
	var nilVault *trackingVault
	var nilBackend *testBackend
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "store", mutate: func(config *Config) { config.Store = nil }},
		{name: "vault", mutate: func(config *Config) { config.Vault = nil }},
		{name: "typed nil vault", mutate: func(config *Config) { config.Vault = nilVault }},
		{name: "claims", mutate: func(config *Config) { config.Claims = nil }},
		{name: "backend", mutate: func(config *Config) { config.Backend = nil }},
		{name: "typed nil backend", mutate: func(config *Config) { config.Backend = nilBackend }},
		{name: "zero backend limit", mutate: func(config *Config) { config.Backend = zeroLimitBackend{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base
			test.mutate(&config)
			runtime, err := New(config)
			if runtime != nil || !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("New = (%v, %v), want nil ErrInvalidInput", runtime, err)
			}
		})
	}

	fixture.vault.mu.Lock()
	fixture.vault.failDerive = true
	fixture.vault.errorText = "sensitive derivation failure"
	fixture.vault.mu.Unlock()
	if runtime, err := New(base); runtime != nil || !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error() {
		t.Fatalf("failed derivation New = (%v, %v)", runtime, err)
	}
	fixture.vault.mu.Lock()
	fixture.vault.failDerive = false
	fixture.vault.deriveLength = 31
	fixture.vault.mu.Unlock()
	if runtime, err := New(base); runtime != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("short derivation New = (%v, %v)", runtime, err)
	}
}

func TestCloseWaitsForAdmittedSecretWrite(t *testing.T) {
	random := &blockingReader{started: make(chan struct{}, 1), release: make(chan struct{})}
	fixture := newBridgeFixtureWithRandom(t, random)
	type writeResult struct {
		plaintext []byte
		err       error
	}
	written := make(chan writeResult, 1)
	go func() {
		tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			written <- writeResult{err: err}
			return
		}
		defer tx.Rollback()
		plaintext := []byte("blocking-secret-value")
		_, err = fixture.runtime.WriteEndpointSecret(context.Background(), tx, resources.SecretWriteInput{
			CanonicalBaseURL: "https://blocking.example.test/v1",
			ConnectorType:    "openai-compatible",
			Plaintext:        plaintext,
			CreatedAt:        bridgeTestNow,
		})
		written <- writeResult{plaintext: plaintext, err: err}
	}()
	select {
	case <-random.started:
	case <-time.After(5 * time.Second):
		t.Fatal("secret write did not reach random source")
	}
	closed := make(chan error, 1)
	go func() { closed <- fixture.runtime.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned before admitted write: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(random.release)
	select {
	case result := <-written:
		if result.err != nil {
			t.Fatalf("WriteEndpointSecret: %v", result.err)
		}
		if !allZero(result.plaintext) {
			t.Fatal("admitted write did not clear plaintext")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("admitted write did not finish")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish after write")
	}
}

func TestConcurrentCloseIsIdempotent(t *testing.T) {
	fixture := newBridgeFixture(t)
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 16)
	for index := 0; index < cap(errorsSeen); index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsSeen <- fixture.runtime.Close()
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent Close: %v", err)
		}
	}
	if !allZero(fixture.runtime.fingerprintKey[:]) {
		t.Fatal("concurrent Close did not clear fingerprint key")
	}
}

func TestCloseWaitsForDiscoveryAndRejectsTheNextOperation(t *testing.T) {
	fixture := newBridgeFixture(t)
	ownerID := fixture.seedUser(t, "close-discovery")
	key := fixture.seedEndpointKey(t, ownerID, "close-discovery", connectorcontract.TypeOpenAICompatible, "close-discovery-credential")
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	discovered := make(chan error, 1)
	go func() {
		_, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
			func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
				started <- struct{}{}
				<-release
				return connectorcontract.DiscoveryResult{
					Failure: connectorcontract.DiscoveryFailureNone, ResponseReceived: true, UpstreamStatus: 200,
				}
			},
		)))
		discovered <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("discoverer did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- fixture.runtime.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned during discovery: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-discovered:
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Discover did not finish")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish after discovery")
	}
	if _, err := fixture.runtime.Discover(context.Background(), fixture.discoveryInput(t, key, discovererFunc(
		func(context.Context, connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
			return connectorcontract.DiscoveryResult{}
		},
	))); !errors.Is(err, ErrClosed) {
		t.Fatalf("Discover after Close error = %v, want ErrClosed", err)
	}
}

type zeroLimitBackend struct{}

func (zeroLimitBackend) Open(string) (backend.EndpointClient, error) {
	return nil, errors.New("unused")
}
func (zeroLimitBackend) MaxResponseBytes() int64 { return 0 }

type blockingReader struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (reader *blockingReader) Read(value []byte) (int, error) {
	reader.once.Do(func() { reader.started <- struct{}{} })
	<-reader.release
	for index := range value {
		value[index] = byte(index + 1)
	}
	return len(value), nil
}

var _ io.Reader = (*blockingReader)(nil)
