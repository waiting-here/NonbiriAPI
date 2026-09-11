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
}

type Service struct {
	db               *sql.DB
	auth             *authz.Authorizer
	resources        *resources.Repository
	donations        *donation.Service
	charity          *charityrouting.Service
	mu               sync.Mutex
	active           map[int64]bool
	createTimeout    time.Duration
	bindingTimeout   time.Duration
	discoveryTimeout time.Duration
}

func New(config Config) (*Service, error) {
	if config.Database == nil || config.Authorizer == nil || config.Resources == nil || config.Donations == nil || config.Charity == nil {
		return nil, errors.New("automation dependencies are required")
	}
	return &Service{db: config.Database, auth: config.Authorizer, resources: config.Resources, donations: config.Donations, charity: config.Charity,
		active: make(map[int64]bool), createTimeout: 30 * time.Second, bindingTimeout: 60 * time.Second, discoveryTimeout: 15 * time.Second}, nil
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
	if s.active[userID] || len(s.active) >= 4 {
		return false
	}
	s.active[userID] = true
	return true
}

func (s *Service) release(userID int64) {
	s.mu.Lock()
	delete(s.active, userID)
	s.mu.Unlock()
}
