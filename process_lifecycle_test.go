package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func waitForProcessTestSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for %s", name)
	}
}

func processTestVault(t *testing.T) *secret.Vault {
	t.Helper()
	key := bytes.Repeat([]byte{0x67}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create process vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

func processTestStore(t *testing.T, path string, vault *secret.Vault) *db.Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := db.OpenContext(ctx, path, vault)
	if err != nil {
		t.Fatalf("open process database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

type processTestListener struct {
	closed chan struct{}
	once   sync.Once
}

func newProcessTestListener() *processTestListener {
	return &processTestListener{closed: make(chan struct{})}
}

func (*processTestListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *processTestListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}
func (*processTestListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

func TestApplicationCloseCancelsEveryWorkerBeforeWaiting(t *testing.T) {
	rankingDone := make(chan struct{})
	lifecycleDone := make(chan struct{})
	releaseWorkers := sync.OnceFunc(func() {
		close(rankingDone)
		close(lifecycleDone)
	})
	workerCanceled := make(chan struct{})
	rankingCanceled := make(chan struct{})
	lifecycleCanceled := make(chan struct{})
	var workerCalls, rankingCalls, lifecycleCalls atomic.Int32
	readiness := &readinessState{}
	readiness.ready.Store(true)
	app := &application{
		readiness: readiness,
		workerCancel: func() {
			if workerCalls.Add(1) == 1 {
				close(workerCanceled)
			}
		},
		rankingCancel: func() {
			if rankingCalls.Add(1) == 1 {
				close(rankingCanceled)
			}
		},
		lifecycleCancel: func() {
			if lifecycleCalls.Add(1) == 1 {
				close(lifecycleCanceled)
			}
		},
		rankingDone:   rankingDone,
		lifecycleDone: lifecycleDone,
	}
	t.Cleanup(func() {
		releaseWorkers()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = app.CloseContext(ctx)
	})

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	err := app.CloseContext(canceled)
	var pending *applicationCleanupError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &pending) || pending.pending != app {
		t.Fatalf("CloseContext with canceled wait = %T %v, want pending application cleanup", err, err)
	}
	if readiness.ready.Load() {
		t.Fatal("close did not withdraw readiness before waiting")
	}
	waitForProcessTestSignal(t, workerCanceled, "shared worker cancellation")
	waitForProcessTestSignal(t, rankingCanceled, "ranking worker cancellation")
	waitForProcessTestSignal(t, lifecycleCanceled, "lifecycle worker cancellation")
	select {
	case <-app.closeDone:
		t.Fatal("application closed before both workers finished")
	default:
	}

	secondContext, secondCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer secondCancel()
	secondResult := make(chan error, 1)
	go func() { secondResult <- app.CloseContext(secondContext) }()
	select {
	case err := <-secondResult:
		t.Fatalf("second CloseContext completed before worker release: %v", err)
	default:
	}
	releaseWorkers()
	select {
	case err := <-secondResult:
		if err != nil {
			t.Fatalf("second CloseContext result: %v", err)
		}
	case <-secondContext.Done():
		t.Fatal("second CloseContext did not finish after worker release")
	}
	if err := app.CloseContext(secondContext); err != nil {
		t.Fatalf("repeated CloseContext changed the final result: %v", err)
	}
	if workerCalls.Load() != 1 || rankingCalls.Load() != 1 || lifecycleCalls.Load() != 1 {
		t.Fatalf("worker cancels repeated: shared=%d ranking=%d lifecycle=%d", workerCalls.Load(), rankingCalls.Load(), lifecycleCalls.Load())
	}
}

func TestProcessCloseTimeoutRetainsStoreAndVaultOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process-owned.sqlite")
	vault := processTestVault(t)
	store := processTestStore(t, path, vault)
	listener := newProcessTestListener()
	workerDone := make(chan struct{})
	releaseWorker := sync.OnceFunc(func() { close(workerDone) })
	workerCanceled := make(chan struct{})
	app := &application{
		workerCancel: func() { close(workerCanceled) },
		rankingDone:  workerDone,
	}
	resources := &processResources{app: app, store: store, vault: vault, listener: listener}
	t.Cleanup(func() {
		releaseWorker()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = resources.CloseContext(ctx)
	})

	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := resources.CloseContext(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("process close with held worker = %v, want deadline", err)
	}
	waitForProcessTestSignal(t, listener.closed, "listener close")
	waitForProcessTestSignal(t, workerCanceled, "process worker cancellation")
	ownershipCheck, cancelOwnershipCheck := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelOwnershipCheck()
	if duplicate, err := db.OpenContext(ownershipCheck, path, vault); !errors.Is(err, db.ErrDatabaseInUse) {
		if duplicate != nil {
			_ = duplicate.Close()
		}
		t.Fatalf("Store ownership released while process close was pending: %v", err)
	}
	var count int
	if err := store.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM site_config`).Scan(&count); err != nil || count == 0 {
		t.Fatalf("Store became unavailable before worker release: count=%d err=%v", count, err)
	}
	key, err := vault.DeriveGenerationTwoSubkey([]byte("process-close-test"))
	if err != nil {
		t.Fatalf("vault closed before Store: %v", err)
	}
	clear(key)

	releaseWorker()
	wait, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer waitCancel()
	if err := resources.CloseContext(wait); err != nil {
		t.Fatalf("process close did not await resource release: %v", err)
	}
	if _, err := vault.DeriveGenerationTwoSubkey([]byte("process-close-test")); !errors.Is(err, secret.ErrClosed) {
		t.Fatalf("vault remained open after final process close: %v", err)
	}
	reopenedVault := processTestVault(t)
	reopened, err := db.OpenContext(wait, path, reopenedVault)
	if err != nil {
		t.Fatalf("database did not reopen after final process close: %v", err)
	}
	if err := reopened.CloseContext(wait); err != nil {
		t.Fatalf("close reopened database: %v", err)
	}
}

func TestLateStartupResultIsCleanedByOriginalOwner(t *testing.T) {
	vault := processTestVault(t)
	listener := newProcessTestListener()
	resources := &processResources{vault: vault, listener: listener}
	entered := make(chan struct{})
	releaseInitialization := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseInitialization) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempt := beginStartup(ctx, func(context.Context) (*processResources, error) {
		close(entered)
		<-releaseInitialization
		return resources, errors.New("late synthetic initialization failure")
	})
	t.Cleanup(func() {
		release()
		select {
		case <-attempt.done:
		default:
			select {
			case <-attempt.result:
			case <-time.After(time.Second):
			}
		}
		wait, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer waitCancel()
		_ = resources.CloseContext(wait)
	})
	waitForProcessTestSignal(t, entered, "initializer entry")
	cancel()
	release()
	waitForProcessTestSignal(t, attempt.done, "late initialization owner completion")
	if resources.closeDone == nil {
		t.Fatal("canceled startup abandoned the late resources")
	}
	waitForProcessTestSignal(t, resources.closeDone, "late resource cleanup")
	waitForProcessTestSignal(t, listener.closed, "late listener cleanup")
	if _, err := vault.DeriveGenerationTwoSubkey([]byte("late-startup-test")); !errors.Is(err, secret.ErrClosed) {
		t.Fatalf("late initializer vault remained open: %v", err)
	}
}

func TestApplicationReadinessAndHealthStates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readiness.sqlite")
	dbfixture.Materialize(t, path)
	vault := processTestVault(t)
	store := processTestStore(t, path, vault)
	startup, cancelStartup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStartup()
	app, err := buildApplication(startup, auditConfig(), store, vault)
	if err != nil {
		t.Fatalf("build application: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = app.CloseContext(ctx)
	})
	check := func(path string, status int, state string) {
		t.Helper()
		response := testApplicationRequest(t, app.handler, http.MethodGet, auditUserHost, path, "", nil, nil)
		if response.Code != status {
			t.Fatalf("%s status=%d want=%d body=%s", path, response.Code, status, response.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || len(body) != 1 || body["status"] != state {
			t.Fatalf("%s response body=%s want status=%q: %v", path, response.Body.String(), state, err)
		}
		if path == "/readyz" && response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("ready response cache-control=%q", response.Header().Get("Cache-Control"))
		}
	}
	check("/readyz", http.StatusServiceUnavailable, "not_ready")
	check("/healthz", http.StatusOK, "ok")
	app.readiness.ready.Store(true)
	check("/readyz", http.StatusOK, "ready")
	check("/healthz", http.StatusOK, "ok")
	app.readiness.failed.Store(true)
	check("/readyz", http.StatusServiceUnavailable, "not_ready")
	check("/healthz", http.StatusOK, "ok")
	app.readiness.failed.Store(false)
	app.withdrawReadiness()
	check("/readyz", http.StatusServiceUnavailable, "not_ready")
	check("/healthz", http.StatusOK, "ok")
	app.readiness.ready.Store(true)
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := app.BeginShutdownContext(shutdown); err != nil {
		t.Fatalf("begin shutdown: %v", err)
	}
	check("/readyz", http.StatusServiceUnavailable, "not_ready")
	check("/healthz", http.StatusOK, "ok")
}

func TestInitializeProcessBindFailureKeepsResourcesOwnedForCleanup(t *testing.T) {
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve synthetic loopback address: %v", err)
	}
	t.Cleanup(func() { _ = occupied.Close() })
	path := filepath.Join(t.TempDir(), "bind-failure.sqlite")
	for name, value := range map[string]string{
		"NONBIRI_LISTEN_ADDR":              occupied.Addr().String(),
		"NONBIRI_DB_PATH":                  path,
		"NONBIRI_ADMIN_USERNAME":           "operator",
		"NONBIRI_ADMIN_PASSWORD":           "synthetic test password",
		"NONBIRI_DISCORD_CLIENT_ID":        "synthetic-client",
		"NONBIRI_DISCORD_CLIENT_SECRET":    "synthetic-secret",
		"NONBIRI_DISCORD_OAUTH_SCOPES":     "",
		"NONBIRI_SITE_BASE_URL":            "http://127.0.0.1",
		"NONBIRI_ADMIN_HOST":               "127.0.0.2",
		"NONBIRI_MASTER_KEY":               hex.EncodeToString(bytes.Repeat([]byte{0x67}, secret.MasterKeyBytes)),
		"NONBIRI_MASTER_KEY_FILE":          "",
		"NONBIRI_TRUSTED_PROXY_CIDRS":      "127.0.0.0/8",
		"NONBIRI_STARTUP_TIMEOUT_SECONDS":  "30",
		"NONBIRI_SHUTDOWN_TIMEOUT_SECONDS": "30",
	} {
		t.Setenv(name, value)
	}
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	startup, cancelStartup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStartup()
	resources, err := initializeProcess(startup)
	if resources != nil {
		t.Cleanup(func() {
			wait, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = resources.CloseContext(wait)
		})
	}
	var listenError *net.OpError
	if !errors.As(err, &listenError) || resources == nil || resources.store == nil || resources.app == nil || resources.vault == nil || resources.listener != nil {
		t.Fatalf("expected bind failure after app recovery with no listener: err=%T %v", err, err)
	}
	if resources.app.readiness == nil || resources.app.readiness.ready.Load() {
		t.Fatal("failed bind exposed a ready application")
	}
	wait, cancelWait := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelWait()
	if err := resources.CloseContext(wait); err != nil {
		t.Fatalf("cleanup after failed bind: %v", err)
	}
	if _, err := resources.vault.DeriveGenerationTwoSubkey([]byte("bind-failure-test")); !errors.Is(err, secret.ErrClosed) {
		t.Fatalf("bind failure left vault open: %v", err)
	}
	reopenedVault := processTestVault(t)
	reopened, err := db.OpenContext(wait, path, reopenedVault)
	if err != nil {
		t.Fatalf("bind failure left database owned: %v", err)
	}
	if err := reopened.CloseContext(wait); err != nil {
		t.Fatalf("close reopened bind-failure database: %v", err)
	}
}
