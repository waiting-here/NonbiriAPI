package main

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/adminusers"
	"github.com/waiting-here/NonbiriAPI/internal/announcements"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type siteConfigRouteRegistrar struct{ runtime *auth.Runtime }

var _ adminapi.SiteConfigRouteRegistrar = siteConfigRouteRegistrar{}

func (registrar siteConfigRouteRegistrar) RegisterAdminRoute(method, pattern string, handler adminapi.SiteConfigAuthorizedAdminHandler) error {
	return registerAdminAdapter(registrar.runtime, method, pattern, handler, func(userID int64) adminapi.SiteConfigAdminPrincipal {
		return adminapi.SiteConfigAdminPrincipal{UserID: userID}
	})
}

type resourceAdminRouteRegistrar struct{ runtime *auth.Runtime }

var _ resources.AdminRouteRegistrar = resourceAdminRouteRegistrar{}

func (registrar resourceAdminRouteRegistrar) RegisterAdminRoute(method, pattern string, handler resources.AuthorizedAdminHandler) error {
	return registerAdminAdapter(registrar.runtime, method, pattern, handler, func(userID int64) resources.AdminPrincipal { return resources.AdminPrincipal{UserID: userID} })
}

type adminAlertRouteRegistrar struct{ runtime *auth.Runtime }

var _ adminalerts.AdminRouteRegistrar = adminAlertRouteRegistrar{}

func (registrar adminAlertRouteRegistrar) RegisterAdminRoute(method, pattern string, handler adminalerts.AuthorizedAdminHandler) error {
	return registerAdminAdapter(registrar.runtime, method, pattern, handler, func(userID int64) adminalerts.AdminPrincipal { return adminalerts.AdminPrincipal{UserID: userID} })
}

type adminUserRouteRegistrar struct{ runtime *auth.Runtime }

var _ adminusers.AdminRouteRegistrar = adminUserRouteRegistrar{}

func (registrar adminUserRouteRegistrar) RegisterAdminRoute(method, pattern string, handler adminusers.AuthorizedAdminHandler) error {
	return registerAdminAdapter(registrar.runtime, method, pattern, handler, func(userID int64) adminusers.AdminPrincipal { return adminusers.AdminPrincipal{UserID: userID} })
}

func (registrar adminUserRouteRegistrar) RegisterStewardRoute(method, pattern string, handler adminusers.AuthorizedAdminHandler) error {
	return registerUserAdapter(registrar.runtime, method, pattern, handler, func(userID int64) adminusers.AdminPrincipal { return adminusers.AdminPrincipal{UserID: userID} })
}

type activityRouteRegistrar struct{ runtime *auth.Runtime }

var _ activities.UserRouteRegistrar = activityRouteRegistrar{}
var _ activities.AdminRouteRegistrar = activityRouteRegistrar{}

func (registrar activityRouteRegistrar) RegisterUserRoute(method, pattern string, handler activities.AuthorizedUserHandler) error {
	return registerUserAdapter(registrar.runtime, method, pattern, handler, func(userID int64) activities.UserPrincipal { return activities.UserPrincipal{UserID: userID} })
}

func (registrar activityRouteRegistrar) RegisterAdminRoute(method, pattern string, handler activities.AuthorizedAdminHandler) error {
	return registerAdminAdapter(registrar.runtime, method, pattern, handler, func(userID int64) activities.AdminPrincipal { return activities.AdminPrincipal{UserID: userID} })
}

type announcementRouteRegistrar struct{ runtime *auth.Runtime }

var _ announcements.UserRouteRegistrar = announcementRouteRegistrar{}
var _ announcements.AdminRouteRegistrar = announcementRouteRegistrar{}

func (registrar announcementRouteRegistrar) RegisterUserRoute(method, pattern string, handler announcements.AuthorizedUserHandler) error {
	return registerUserAdapter(registrar.runtime, method, pattern, handler, func(userID int64) announcements.UserPrincipal { return announcements.UserPrincipal{UserID: userID} })
}

func (registrar announcementRouteRegistrar) RegisterAdminRoute(method, pattern string, handler announcements.AuthorizedAdminHandler) error {
	return registerAdminAdapter(registrar.runtime, method, pattern, handler, func(userID int64) announcements.AdminPrincipal { return announcements.AdminPrincipal{UserID: userID} })
}

func (registrar announcementRouteRegistrar) RegisterStewardRoute(method, pattern string, handler announcements.AuthorizedAdminHandler) error {
	return registerUserAdapter(registrar.runtime, method, pattern, handler, func(userID int64) announcements.AdminPrincipal { return announcements.AdminPrincipal{UserID: userID} })
}

type issueRouteRegistrar struct{ runtime *auth.Runtime }

var _ issues.UserRouteRegistrar = issueRouteRegistrar{}

func (registrar issueRouteRegistrar) RegisterUserRoute(method, pattern string, handler issues.AuthorizedUserHandler) error {
	return registerUserAdapter(registrar.runtime, method, pattern, handler, func(userID int64) issues.UserPrincipal { return issues.UserPrincipal{UserID: userID} })
}

type maintenanceRouteRegistrar struct{ runtime *auth.Runtime }

var _ maintenance.StewardRouteRegistrar = maintenanceRouteRegistrar{}
var _ maintenance.AdminRouteRegistrar = maintenanceRouteRegistrar{}

func (registrar maintenanceRouteRegistrar) RegisterStewardRoute(method, pattern string, handler maintenance.AuthorizedHTTPHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterUserRoute(method, pattern, func(writer http.ResponseWriter, request *http.Request, _ resources.UserPrincipal) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorUserSession || actor.UserID <= 0 {
			httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(writer, request, maintenance.HTTPPrincipal{Actor: actor})
	})
}

func (registrar maintenanceRouteRegistrar) RegisterAdminRoute(method, pattern string, handler maintenance.AuthorizedHTTPHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterAdminRoute(method, pattern, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(writer, request, maintenance.HTTPPrincipal{Actor: actor})
	}))
}
