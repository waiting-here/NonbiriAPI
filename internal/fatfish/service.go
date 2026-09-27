// Package fatfish owns the authoritative Fat Fish activity, verification, and economy.
package fatfish

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/fatfish/engine"
)

var (
	ErrInvalid      = errors.New("fatfish: invalid request")
	ErrUnauthorized = errors.New("fatfish: unauthorized")
	ErrForbidden    = errors.New("fatfish: forbidden")
	ErrNotFound     = errors.New("fatfish: not found")
	ErrConflict     = errors.New("fatfish: conflict")
	ErrClosed       = errors.New("fatfish: closed")
	ErrCapacity     = errors.New("fatfish: capacity exceeded")
	ErrUnavailable  = errors.New("fatfish: unavailable")
	ErrInvariant    = errors.New("fatfish: invariant violation")
)

type UserAuthorizer interface {
	AuthorizeUserMutation(context.Context, *sql.Tx, int64) error
}

type AdminAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
}

type AdmissionGate interface {
	AuthorizeUserActivity(context.Context, *sql.Tx, int64) error
}

type KeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

type Config struct {
	DB       *sql.DB
	Users    UserAuthorizer
	Admins   AdminAuthorizer
	Gate     AdmissionGate
	Identity *continuity.Service
	Keys     KeyDeriver
	Now      func() time.Time
	Random   io.Reader
}

type Service struct {
	db          *sql.DB
	users       UserAuthorizer
	admins      AdminAuthorizer
	gate        AdmissionGate
	identity    *continuity.Service
	keys        KeyDeriver
	now         func() time.Time
	random      io.Reader
	replay      func(engine.Level, [32]byte, []engine.InputTuple, engine.ReplayOptions) (engine.ReplayResult, error)
	jobsMu      sync.Mutex
	jobs        map[string]*verifyJob
	jobBytes    int
	workerSlots chan struct{}
	workers     sync.WaitGroup
	closed      bool
}

func New(config Config) (*Service, error) {
	if config.DB == nil || config.Users == nil || config.Admins == nil || config.Gate == nil || config.Identity == nil || config.Keys == nil {
		return nil, ErrInvalid
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &Service{db: config.DB, users: config.Users, admins: config.Admins, gate: config.Gate, identity: config.Identity, keys: config.Keys, now: config.Now, random: config.Random,
		jobs: make(map[string]*verifyJob), workerSlots: make(chan struct{}, 2), replay: engine.Replay}, nil
}
