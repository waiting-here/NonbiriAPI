package stewardautomation

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type Config struct {
	Database   *sql.DB
	Authorizer *authz.Authorizer
	Resources  *resources.Repository
	Donations  *donation.Service
	Charity    *charityrouting.Service
	Now        func() time.Time
}

type Service struct {
	db               *sql.DB
	auth             *authz.Authorizer
	resources        *resources.Repository
	donations        *donation.Service
	charity          *charityrouting.Service
	mu               sync.Mutex
	active           map[int64]bool
	cancels          map[int64]context.CancelFunc
	closed           bool
	now              func() time.Time
	createTimeout    time.Duration
	bindingTimeout   time.Duration
	discoveryTimeout time.Duration
}

func New(config Config) (*Service, error) {
	if config.Database == nil || config.Authorizer == nil || config.Resources == nil || config.Donations == nil || config.Charity == nil {
		return nil, errors.New("automation dependencies are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{db: config.Database, auth: config.Authorizer, resources: config.Resources, donations: config.Donations, charity: config.Charity,
		active: make(map[int64]bool), cancels: make(map[int64]context.CancelFunc), now: config.Now, createTimeout: 30 * time.Second, bindingTimeout: 60 * time.Second, discoveryTimeout: 15 * time.Second}, nil
}

func (s *Service) begin(ctx context.Context, userID int64) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := s.auth.AuthorizeStewardCaller(ctx, tx, userID); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *Service) admit(userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.active[userID] || len(s.active) >= 4 {
		return false
	}
	s.active[userID] = true
	return true
}

func (s *Service) release(userID int64) {
	s.mu.Lock()
	delete(s.active, userID)
	delete(s.cancels, userID)
	s.mu.Unlock()
}

func (s *Service) track(userID int64, cancel context.CancelFunc) {
	s.mu.Lock()
	closed := s.closed
	if !closed {
		s.cancels[userID] = cancel
	}
	s.mu.Unlock()
	if closed {
		cancel()
	}
}

// Close cancels active automation work and prevents new admission. Discovery
// worker shutdown and lifecycle retirement remain with their existing owners.
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	cancels := make([]context.CancelFunc, 0, len(s.cancels))
	for _, cancel := range s.cancels {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}
