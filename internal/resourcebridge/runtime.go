// Package resourcebridge joins the resource repository to the credential and
// dispatch owners without exposing a general secret lookup or claim API.
package resourcebridge

import (
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	fingerprintSubkeyInfo = "credential-report-fingerprint/v1"
	maxUnixSecond         = int64(253402300799)
)

var (
	ErrClosed       = errors.New("resource bridge: closed")
	ErrInvalidInput = errors.New("resource bridge: invalid input")
	ErrInterrupted  = errors.New("resource bridge: interrupted")
	ErrUnavailable  = errors.New("resource bridge: unavailable")
)

// Vault is the exact Generation 2 capability needed for persistence,
// dispatch, and the one-way reporting fingerprint.
type Vault interface {
	secret.GenerationTwoContextCodec
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

// Config contains only the owners needed by the two resource adapters. Now
// and Random are injectable so timestamp and context generation boundaries can
// be exercised deterministically.
type Config struct {
	Store   *db.Store
	Vault   Vault
	Claims  *claim.Service
	Backend backend.Backend
	Now     func() time.Time
	Random  io.Reader
}

func (Config) String() string   { return "[redacted resource bridge config]" }
func (Config) GoString() string { return "[redacted resource bridge config]" }
func (Config) LogValue() slog.Value {
	return slog.StringValue("[redacted resource bridge config]")
}

// Runtime owns the derived fingerprint key and coordinates its lifetime with
// in-flight writes and discoveries.
type Runtime struct {
	db      *db.Store
	vault   Vault
	claims  *claim.Service
	backend backend.Backend
	now     func() time.Time
	random  io.Reader

	randomMu sync.Mutex

	lifecycleMu sync.Mutex
	active      sync.WaitGroup
	closed      bool
	closeDone   chan struct{}

	fingerprintKey [32]byte
}

var (
	_ resources.SecretWriter       = (*Runtime)(nil)
	_ resources.DiscoveryClaimRail = (*Runtime)(nil)
)

// New derives the reporting key exactly once and copies it into runtime-owned
// storage. The returned runtime never retains the derivation slice.
func New(config Config) (*Runtime, error) {
	if config.Store == nil || config.Store.DB() == nil || nilInterface(config.Vault) ||
		config.Claims == nil || backend.IsNil(config.Backend) || config.Backend.MaxResponseBytes() <= 0 {
		return nil, ErrInvalidInput
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if nilInterface(config.Random) {
		config.Random = rand.Reader
	}
	derived, err := config.Vault.DeriveGenerationTwoSubkey([]byte(fingerprintSubkeyInfo))
	if err != nil {
		clear(derived)
		return nil, ErrUnavailable
	}
	defer clear(derived)
	if len(derived) != len(Runtime{}.fingerprintKey) {
		return nil, ErrUnavailable
	}
	runtime := &Runtime{
		db:        config.Store,
		vault:     config.Vault,
		claims:    config.Claims,
		backend:   config.Backend,
		now:       config.Now,
		random:    config.Random,
		closeDone: make(chan struct{}),
	}
	copy(runtime.fingerprintKey[:], derived)
	return runtime, nil
}

func (r *Runtime) begin() error {
	if r == nil {
		return ErrClosed
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.closed {
		return ErrClosed
	}
	r.active.Add(1)
	return nil
}

func (r *Runtime) end() { r.active.Done() }

// Close prevents new work, waits for admitted work, and clears the derived
// fingerprint key. It does not close the injected database, vault, or backend.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.lifecycleMu.Lock()
	if r.closed {
		done := r.closeDone
		r.lifecycleMu.Unlock()
		<-done
		return nil
	}
	r.closed = true
	done := r.closeDone
	r.lifecycleMu.Unlock()

	r.active.Wait()
	clear(r.fingerprintKey[:])
	close(done)
	return nil
}

func (r *Runtime) validDecisionSecond(value int64) bool {
	if r == nil || r.now == nil || value < 0 || value > maxUnixSecond {
		return false
	}
	now := r.now().Unix()
	return now >= 0 && now <= maxUnixSecond
}

func (*Runtime) String() string   { return "[redacted resource bridge]" }
func (*Runtime) GoString() string { return "[redacted resource bridge]" }
func (*Runtime) LogValue() slog.Value {
	return slog.StringValue("[redacted resource bridge]")
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
