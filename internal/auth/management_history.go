package auth

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

// ConfigureManagementHistory installs the shared history service before serving
// requests. Authentication and final transaction authorization remain unchanged.
func (r *Runtime) ConfigureManagementHistory(service *managementlog.Service) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen || r.closed {
		return ErrFrozen
	}
	r.managementLog = service
	return nil
}
func (r *Runtime) managementBoundary(method, pattern string, next http.Handler) http.Handler {
	if _, enabled := managementlog.DescribeRoute(method, pattern); !enabled && !managementlog.ConditionalRoute(method, pattern) {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		r.mu.Lock()
		service := r.managementLog
		r.mu.Unlock()
		if service == nil {
			next.ServeHTTP(w, request)
			return
		}
		service.Wrap(method, pattern, next).ServeHTTP(w, request)
	})
}
