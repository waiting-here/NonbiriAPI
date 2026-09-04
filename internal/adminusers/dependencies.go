package adminusers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type CursorKeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

type AdminFinalAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
}

// PostCommitInvalidator revokes process-local account authority after commit.
// Implementations must be infallible and idempotent.
type PostCommitInvalidator interface {
	InvalidateUserAuthority(userID int64)
}

type AdminPrincipal struct {
	UserID int64
}

type AuthorizedAdminHandler func(http.ResponseWriter, *http.Request, AdminPrincipal)

// AdminRouteRegistrar is the independent production wiring seam. The root
// adapter remains responsible for admin-host and session-generation checks.
type AdminRouteRegistrar interface {
	RegisterAdminRoute(method, pattern string, handler AuthorizedAdminHandler) error
}

type ServiceConfig struct {
	Database    *sql.DB
	CursorKeys  CursorKeyDeriver
	FinalAuth   AdminFinalAuthorizer
	Invalidator PostCommitInvalidator
	Now         func() time.Time
	NewID       func(string) (string, error)
}

type Service struct {
	database    *sql.DB
	cursorKeys  CursorKeyDeriver
	finalAuth   AdminFinalAuthorizer
	invalidator PostCommitInvalidator
	now         func() time.Time
	newID       func(string) (string, error)
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Database == nil || nilDependency(config.CursorKeys) || nilDependency(config.FinalAuth) || nilDependency(config.Invalidator) {
		return nil, errors.New("adminusers: database, cursor keys, final authorizer, and invalidator are required")
	}
	key, err := config.CursorKeys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil || len(key) != 32 {
		clear(key)
		return nil, errors.New("adminusers: derive pagination cursor key")
	}
	clear(key)
	service := &Service{
		database: config.Database, cursorKeys: config.CursorKeys,
		finalAuth: config.FinalAuth, invalidator: config.Invalidator,
		now: config.Now, newID: config.NewID,
	}
	if service.now == nil {
		service.now = time.Now
	}
	if service.newID == nil {
		service.newID = db.GenerateOpaqueID
	}
	return service, nil
}

func nilDependency(value any) bool {
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
