package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/aiagent"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type agentAuthority struct{ authorizer *authz.Authorizer }

func sessionAgentActor(actor agentplan.Actor) authz.Actor {
	kind := authz.ActorUserSession
	if actor.Role == "admin" {
		kind = authz.ActorAdminSession
	}
	return authz.Actor{Kind: kind, UserID: actor.ID, SessionTokenHash: actor.SessionHash, SessionGeneration: actor.SessionGeneration, ElevationToken: actor.Elevation}
}

func agentPermission(action string) string {
	switch action {
	case "donations", "review_history":
		return "donations.browse"
	case "donation_models":
		return "donations.browse"
	case "charity_models":
		return "charity.read"
	case "charity_binding_presets", "charity.binding.apply_preset":
		return "charity.presets"
	case "users":
		return "users.read"
	case "site_status":
		return "pressure.read"
	case "donation.review":
		return "donations.review"
	case "donation.handle":
		return "donations.handle"
	case "donation.key.update", "donation.key.failure_policy", "donation.key.reset", "donation.use.reset":
		return "donations.manage_keys"
	case "charity.model.create":
		return "charity.create"
	case "charity.model.update":
		return "model_fields"
	case "charity.model.delete":
		return "charity.delete"
	case "charity.binding.add", "charity.binding.update", "charity.binding.delete":
		return "charity.manage"
	case "users.profile":
		return "user_fields"
	case "users.economy":
		return "user_fields"
	case "users.ban":
		return "users.ban"
	case "users.unban":
		return "users.unban"
	case "users.permissions":
		return "delegate"
	case "users.caller_key_revoke":
		return "users.caller_keys.revoke"
	default:
		return action
	}
}

func (a agentAuthority) Authorize(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, action string) error {
	if actor.ID <= 0 {
		return authz.ErrUnauthorized
	}
	if actor.Automatic {
		switch action {
		case "ai.auto", "donations", "donation_models", "review_history", "charity_models", "donation.review", "charity.binding.add":
		default:
			return authz.ErrForbidden
		}
		var allowed bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_agent_settings c JOIN users u ON u.id=c.owner_id WHERE c.id=1 AND c.mode<>'off' AND c.owner_id=? AND u.is_admin=1 AND (u.is_banned=0 OR u.banned_until<=?))`, actor.ID, time.Now().Unix()).Scan(&allowed)
		if err != nil {
			return err
		}
		if !allowed {
			return authz.ErrForbidden
		}
		return nil
	}
	permission := agentPermission(action)
	role := authz.RoleAdministrator
	if actor.Role != "admin" {
		switch action {
		case "ai.config", "ai.requests", "ai.logs", "ai.archives", "users.mfa_read", "users.mfa_reset", "users.delete", "fresh_elevation", "ai.auto":
			return authz.ErrForbidden
		}
		switch actor.Role {
		case "level5":
			role = authz.RoleTrainee
		case "level6":
			role = authz.RoleSteward
		default:
			return authz.ErrForbidden
		}
		ctx = authz.WithStewardPermission(ctx, permission)
	}
	_, err := a.authorizer.Authorize(ctx, tx, sessionAgentActor(actor), authz.Requirement{Role: role, FreshElevation: action == "fresh_elevation"})
	if err == nil && actor.Role != "admin" && (action == "charity_binding_presets" || action == "charity.binding.apply_preset") {
		err = authz.RequireStewardPermission(ctx, tx, actor.ID, "charity.manage")
	}
	return err
}

func agentContext(ctx context.Context, actor agentplan.Actor, action string) context.Context {
	ctx = auth.WithSessionActor(ctx, sessionAgentActor(actor))
	if actor.Role != "admin" {
		ctx = authz.WithStewardPermission(ctx, agentPermission(action))
	}
	return ctx
}

type agentRouteRegistrar struct {
	runtime  *auth.Runtime
	database *sql.DB
}

func (r agentRouteRegistrar) adapt(handler aiagent.AuthorizedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		session, ok := auth.ActorFromContext(request.Context())
		if !ok {
			httperr.WriteError(w, httperr.New(httperr.CodeUnauthorized, "请重新登录。"))
			return
		}
		var label string
		var level int
		err := r.database.QueryRowContext(request.Context(), `SELECT username,COALESCE(level,auto_level) FROM users WHERE id=?`, session.UserID).Scan(&label, &level)
		if err != nil {
			code := httperr.CodeInternal
			if errors.Is(err, sql.ErrNoRows) {
				code = httperr.CodeUnauthorized
			}
			httperr.WriteError(w, httperr.New(code, "账号暂不可用。"))
			return
		}
		role := "level" + strconv.Itoa(level)
		if session.Kind == authz.ActorAdminSession {
			role = "admin"
		}
		handler(w, request, agentplan.Actor{ID: session.UserID, Role: role, Label: label, SessionHash: session.SessionTokenHash, SessionGeneration: session.SessionGeneration, Elevation: session.ElevationToken})
	}
}
func (r agentRouteRegistrar) RegisterAdminRoute(method, path string, handler aiagent.AuthorizedHandler) error {
	return r.runtime.RegisterAdminRoute(method, path, r.adapt(handler))
}
func (r agentRouteRegistrar) RegisterStewardRoute(method, path string, handler aiagent.AuthorizedHandler) error {
	return r.runtime.RegisterUserRoute(method, path, func(w http.ResponseWriter, request *http.Request, _ resources.UserPrincipal) {
		r.adapt(handler)(w, request)
	})
}
