package app

import (
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/adminusers"
	"github.com/waiting-here/NonbiriAPI/internal/announcements"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/checkin"
	"github.com/waiting-here/NonbiriAPI/internal/creditapi"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/timeapi"
)

func (a *application) registerRoutes(store *db.Store, roleAuthorizer *roleFinalTxAuthorizer, debugMutations *debug.MutationRepository) error {
	if err := a.registerCloudflareRoutes(store); err != nil {
		return fmt.Errorf("register Cloudflare route status: %w", err)
	}
	if err := adminapi.RegisterSiteConfigRoutes(siteConfigRouteRegistrar{runtime: a.authRuntime}, a.adminConfig); err != nil {
		return fmt.Errorf("register administrator site configuration routes: %w", err)
	}
	if err := adminusers.RegisterRoutes(adminUserRouteRegistrar{runtime: a.authRuntime}, a.adminUsers); err != nil {
		return fmt.Errorf("register administrator user routes: %w", err)
	}
	if err := adminusers.RegisterStewardRoutes(adminUserRouteRegistrar{runtime: a.authRuntime}, a.adminUsers); err != nil {
		return fmt.Errorf("register steward user routes: %w", err)
	}
	if err := adminalerts.RegisterRoutes(adminAlertRouteRegistrar{runtime: a.authRuntime}, a.adminAlerts); err != nil {
		return fmt.Errorf("register administrator alert routes: %w", err)
	}
	if err := resources.RegisterRoutes(a.authRuntime, a.resourceRepo); err != nil {
		return fmt.Errorf("register resource routes: %w", err)
	}
	if err := resources.RegisterAdminRoutes(resourceAdminRouteRegistrar{runtime: a.authRuntime}, a.resourceRepo); err != nil {
		return fmt.Errorf("register resource administrator routes: %w", err)
	}
	if err := resources.RegisterManagedDiscoveryRoutes(a.authRuntime, resourceAdminRouteRegistrar{runtime: a.authRuntime}, a.resourceRepo); err != nil {
		return fmt.Errorf("register managed model discovery routes: %w", err)
	}
	if err := donation.RegisterOwnerRoutes(a.authRuntime, a.donations); err != nil {
		return fmt.Errorf("register donation owner routes: %w", err)
	}
	if err := donation.RegisterAdminRoutes(a.authRuntime, a.donations); err != nil {
		return fmt.Errorf("register donation administrator routes: %w", err)
	}
	if err := donation.RegisterStewardRoutes(a.authRuntime, a.donations); err != nil {
		return fmt.Errorf("register donation steward routes: %w", err)
	}
	if err := charityrouting.RegisterOwnerRoutes(a.authRuntime, a.charityRouting); err != nil {
		return fmt.Errorf("register charity capability routes: %w", err)
	}
	if err := charityrouting.RegisterAdminRoutes(a.authRuntime, a.charityRouting); err != nil {
		return fmt.Errorf("register charity administrator routes: %w", err)
	}
	if err := charityrouting.RegisterStewardRoutes(a.authRuntime, a.charityRouting); err != nil {
		return fmt.Errorf("register charity steward routes: %w", err)
	}
	if err := checkin.RegisterRoutes(a.authRuntime, a.checkin); err != nil {
		return fmt.Errorf("register check-in routes: %w", err)
	}
	activityRoutes := activityRouteRegistrar{runtime: a.authRuntime}
	if err := activities.RegisterRoutes(activityRoutes, activityRoutes, a.activities); err != nil {
		return fmt.Errorf("register activities routes: %w", err)
	}
	announcementRoutes := announcementRouteRegistrar{runtime: a.authRuntime}
	if err := announcements.RegisterRoutes(announcementRoutes, announcementRoutes, a.announcements); err != nil {
		return fmt.Errorf("register announcement routes: %w", err)
	}
	if err := announcements.RegisterStewardRoutes(announcementRoutes, a.announcements); err != nil {
		return fmt.Errorf("register steward announcement routes: %w", err)
	}
	if err := issues.RegisterRoutes(issueRouteRegistrar{runtime: a.authRuntime}, a.issues); err != nil {
		return fmt.Errorf("register issue routes: %w", err)
	}
	if err := a.reports.RegisterRoutes(a.authRuntime); err != nil {
		return fmt.Errorf("register report routes: %w", err)
	}
	maintenanceRoutes := maintenanceRouteRegistrar{runtime: a.authRuntime}
	if err := maintenance.RegisterRoutes(maintenanceRoutes, maintenanceRoutes, maintenance.HTTPOptions{
		Database: store.DB(), ReadDatabase: store.ReadDB(), Service: a.maintenance,
	}); err != nil {
		return fmt.Errorf("register maintenance routes: %w", err)
	}
	if err := debug.RegisterRoutes(debugRouteRegistrar{runtime: a.authRuntime, connections: a.accountEvents}, a.debug, debugMutations); err != nil {
		return fmt.Errorf("register Debug routes: %w", err)
	}
	if err := logapi.RegisterUserRoutes(a.authRuntime, a.logs); err != nil {
		return fmt.Errorf("register user log routes: %w", err)
	}
	if err := creditapi.RegisterUserRoutes(a.authRuntime, store.ReadDB()); err != nil {
		return fmt.Errorf("register credit history route: %w", err)
	}
	if err := logapi.RegisterStewardRoutes(a.authRuntime, a.logs, roleAuthorizer); err != nil {
		return fmt.Errorf("register steward log routes: %w", err)
	}
	if err := logapi.RegisterAdminRoutes(a.authRuntime, a.logs); err != nil {
		return fmt.Errorf("register administrator log routes: %w", err)
	}
	if err := a.audits.registerRoutes(a.authRuntime, a.logs, roleAuthorizer); err != nil {
		return fmt.Errorf("register audit routes: %w", err)
	}
	if err := a.activityRuntime.RegisterRoutes(a.authRuntime); err != nil {
		return fmt.Errorf("register limited activity routes: %w", err)
	}
	lifecycleRoutes := lifecycleRouteRegistrar{runtime: a.authRuntime}
	if err := lifecycle.RegisterRoutes(lifecycleRoutes, lifecycleRoutes, a.lifecycle); err != nil {
		return fmt.Errorf("register account lifecycle routes: %w", err)
	}
	if err := timeapi.RegisterRoutes(
		a.authRuntime,
		resourceAdminRouteRegistrar{runtime: a.authRuntime},
		&timeContextResolver{store: store, authority: roleAuthorizer},
	); err != nil {
		return fmt.Errorf("register time API routes: %w", err)
	}
	if err := registerAccountEventRoute(a.authRuntime, a.gate, a.games.AccountContinuation(), a.activityEvents, a.accountEvents); err != nil {
		return fmt.Errorf("register account event route: %w", err)
	}
	return nil
}
