package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestTypedRouteAdapterPreservesPrincipalRequestAndCancellation(t *testing.T) {
	type contextKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "request-value"))
	request := httptest.NewRequest(http.MethodPost, "/api/example/73", nil).WithContext(ctx)
	request.SetPathValue("id", "73")
	cancel()
	writer := httptest.NewRecorder()
	called := false
	handler := activities.AuthorizedUserHandler(func(gotWriter http.ResponseWriter, gotRequest *http.Request, principal activities.UserPrincipal) {
		called = true
		if gotWriter != writer || gotRequest != request || principal.UserID != 89 {
			t.Fatal("adapter changed request or principal")
		}
		if gotRequest.PathValue("id") != "73" || gotRequest.Context().Value(contextKey{}) != "request-value" || !errors.Is(gotRequest.Context().Err(), context.Canceled) {
			t.Fatal("adapter lost path values or cancellation")
		}
	})
	adaptUserPrincipal(handler, func(userID int64) activities.UserPrincipal { return activities.UserPrincipal{UserID: userID} })(writer, request, resources.UserPrincipal{UserID: 89})
	if !called {
		t.Fatal("domain handler was not called")
	}
}

func TestTypedAdminAdapterKeepsMissingActorProjection(t *testing.T) {
	called := false
	handler := resources.AuthorizedAdminHandler(func(http.ResponseWriter, *http.Request, resources.AdminPrincipal) { called = true })
	writer := httptest.NewRecorder()
	adaptAdminPrincipal(handler, func(userID int64) resources.AdminPrincipal { return resources.AdminPrincipal{UserID: userID} }).ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/admin/api/example", nil))
	if called || writer.Code != http.StatusUnauthorized || writer.Body.String() != "{\"error\":{\"code\":\"unauthorized\",\"source\":\"platform\",\"message\":\"[NonbiriAPI] authentication required\"}}\n" {
		t.Fatal("missing actor projection changed", writer.Code, writer.Body.String())
	}
}

func TestTypedRouteAdaptersRejectMissingDependencies(t *testing.T) {
	user := activities.AuthorizedUserHandler(func(http.ResponseWriter, *http.Request, activities.UserPrincipal) {})
	admin := resources.AuthorizedAdminHandler(func(http.ResponseWriter, *http.Request, resources.AdminPrincipal) {})
	if err := registerUserAdapter((*auth.Runtime)(nil), "GET", "/api/example", user, func(id int64) activities.UserPrincipal { return activities.UserPrincipal{UserID: id} }); !errors.Is(err, auth.ErrInvalidRoute) {
		t.Fatal(err)
	}
	if err := registerAdminAdapter((*auth.Runtime)(nil), "GET", "/admin/api/example", admin, func(id int64) resources.AdminPrincipal { return resources.AdminPrincipal{UserID: id} }); !errors.Is(err, auth.ErrInvalidRoute) {
		t.Fatal(err)
	}
}
