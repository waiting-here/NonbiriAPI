package auth

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"net/http"
	"testing"
)

func TestAgentMFAUsesLiveSessionVersionAndCommitInvalidation(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	observer := &mfaChangeObserver{}
	if err := f.runtime.AttachUserSessionInvalidationObserver(observer); err != nil {
		t.Fatal(err)
	}
	userCookie := loginUser(t, f, "agent-mfa-user", "")
	id, userActor := userSessionIdentity(t, f, userCookie)
	login := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	if login.Code != 200 {
		t.Fatalf("admin login=%d", login.Code)
	}
	adminCookie := responseCookie(t, login, AdminSessionCookieName)
	adminID, adminActor := userSessionIdentity(t, f, adminCookie)
	adminActor.Kind = authz.ActorAdminSession
	ctx := withActor(t.Context(), adminActor)
	actor := agentplan.Actor{ID: adminID, Role: "admin"}
	elevation := 0
	authorize := func(context.Context, *sql.Tx, agentplan.Actor, string) error { return nil }
	tx, _ := f.store.DB().BeginTx(ctx, nil)
	old, err := f.runtime.AgentReadMFA(ctx, tx, actor, id, authorize)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	enrollMFA(t, f, userActor)
	var raw []byte
	if err = f.store.DB().QueryRow("SELECT revision FROM users WHERE id=?", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	revision, err := db.DecodeU128(raw)
	if err != nil {
		t.Fatal(err)
	}
	authorize = func(_ context.Context, _ *sql.Tx, _ agentplan.Actor, kind string) error {
		if kind == "fresh_elevation" {
			elevation++
		}
		return nil
	}
	tx, _ = f.store.DB().BeginTx(ctx, nil)
	if _, err = f.runtime.AgentResetMFA(ctx, tx, actor, id, revision, old.Version, authorize); !errors.Is(err, errMFAConflict) {
		t.Fatalf("new enrollment stale reset=%v", err)
	}
	tx.Rollback()
	if elevation != 0 {
		t.Fatal("stale plan consumed elevation")
	}
	for _, commit := range []bool{false, true} {
		tx, _ = f.store.DB().BeginTx(ctx, nil)
		current, err := f.runtime.AgentReadMFA(ctx, tx, actor, id, authorize)
		if err != nil {
			t.Fatal(err)
		}
		finalize, err := f.runtime.AgentResetMFA(ctx, tx, actor, id, revision, current.Version, authorize)
		if err != nil {
			t.Fatal(err)
		}
		if len(observer.revoked) != 0 {
			t.Fatal("revoked before commit")
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		finalize(commit)
		finalize(commit)
		var count int
		if err = f.store.DB().QueryRow("SELECT count(*) FROM account_mfa_credentials WHERE user_id=?", id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if commit {
			if count != 0 || len(observer.revoked) != 1 {
				t.Fatalf("commit credentials=%d revoked=%v", count, observer.revoked)
			}
		} else {
			if count != 1 || len(observer.revoked) != 0 {
				t.Fatal("rollback changed MFA")
			}
		}
	}
	// Reusing a revoked real administrator session is not authorized by a
	// permissive outer callback.
	if _, err = f.store.DB().Exec("DELETE FROM sessions WHERE user_id=?", adminID); err != nil {
		t.Fatal(err)
	}
	tx, _ = f.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err = f.runtime.AgentReadMFA(ctx, tx, actor, id, authorize); err == nil {
		t.Fatal("revoked administrator session accepted")
	}
}
