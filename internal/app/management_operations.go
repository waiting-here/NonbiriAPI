package app

import (
	"context"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/stewardautomation"
)

func configureManagementOperations(store *db.Store, runtime *auth.Runtime, authorizer *roleFinalTxAuthorizer) (*managementlog.Service, error) {
	service := managementlog.New(store.DB(), store.ReadDB())
	service.Authorizer = authorizer
	if err := runtime.ConfigureManagementHistory(service); err != nil {
		return nil, err
	}
	if err := service.RegisterRoutes(runtime, func(request *http.Request) int64 {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorAdminSession {
			return 0
		}
		return actor.UserID
	}); err != nil {
		return nil, err
	}
	return service, nil
}
func startManagementCleanup(ctx context.Context, service *managementlog.Service) (context.CancelFunc, <-chan struct{}) {
	work, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); service.Run(work) }()
	return cancel, done
}

func wrapAutomationHistory(history *managementlog.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only these registered management paths belong to retained operations.
		// Personal CallerKey APIs and reads keep their existing request history.
		switch r.URL.Path {
		case stewardautomation.DonationsPath, stewardautomation.BindingsPath, stewardautomation.FailurePolicyPath:
			history.Wrap(r.Method, r.URL.Path, next).ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}
