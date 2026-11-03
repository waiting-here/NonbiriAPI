package authz

import (
	"context"
	"testing"
)

func TestAutomaticReviewAuthorityIsNarrowAndLive(t *testing.T) {
	store := openAuthStore(t)
	id := insertAuthUser(t, store.DB(), "9081", "auto-owner-session", "g", true, nil)
	if _, err := store.DB().Exec(`INSERT INTO ai_agent_settings(id,revision,mode,config_json,owner_id,owner_label,updated_at) VALUES(1,0,'preview','{}',?,'owner',0)`, id); err != nil {
		t.Fatal(err)
	}
	a := testAuthorizer(nil)
	actor := Actor{Kind: ActorAdminSession, UserID: id}
	ctx := WithAutomaticReviewAuthority(context.Background(), id)
	check := func(ctx context.Context, actor Actor, req Requirement, want bool) {
		t.Helper()
		tx, err := store.ReadDB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		_, err = a.Authorize(ctx, tx, actor, req)
		if (err == nil) != want {
			t.Fatalf("actor=%+v req=%+v allowed=%t err=%v", actor, req, want, err)
		}
	}
	req := Requirement{Role: RoleAdministrator}
	check(context.Background(), actor, req, false)
	check(ctx, actor, req, true)
	check(ctx, actor, Requirement{Role: RoleAdministrator, FreshElevation: true}, false)
	check(ctx, actor, Requirement{Role: RoleUser}, false)
	check(ctx, Actor{Kind: ActorAdminSession, UserID: id, SessionTokenHash: "auto-owner-session", SessionGeneration: "g"}, req, false)
	check(WithAutomaticReviewAuthority(context.Background(), id+1), actor, req, false)
	if _, err := store.DB().Exec("UPDATE ai_agent_settings SET mode='off' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	check(ctx, actor, req, false)
	if _, err := store.DB().Exec("UPDATE ai_agent_settings SET mode='preview' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	check(ctx, actor, req, false)
}
