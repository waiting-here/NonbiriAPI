package authz

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestRequiredMFAKeepsNarrowCallerAuthority(t *testing.T) {
	store := openAuthStore(t)
	level := int64(6)
	id := insertAuthUser(t, store.DB(), "mfa-caller", "mfa-session", "generation", false, &level)
	if _, err := store.DB().Exec(`INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at) VALUES(?,1,?,'head','tail',?,?)`, id, bytes.Repeat([]byte{1}, 32), authTestNow, authTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE site_config SET value='1' WHERE key='mfa_required_level_6'`); err != nil {
		t.Fatal(err)
	}
	ctx := WithStewardCaller(context.Background(), StewardCaller{UserID: id, Generation: 1})
	tx := beginAuthTx(t, store.DB())
	defer tx.Rollback()
	a := testAuthorizer(nil)
	if _, err := a.AuthorizeStewardCaller(ctx, tx, id); err != nil {
		t.Fatal("CallerKey unexpectedly needs MFA", err)
	}
	actor := Actor{Kind: ActorUserSession, UserID: id, SessionTokenHash: "mfa-session", SessionGeneration: "generation"}
	if _, err := a.Authorize(ctx, tx, actor, Requirement{Role: RoleSteward}); !errors.Is(err, ErrMFARequired) {
		t.Fatal("caller context bypassed browser MFA", err)
	}
	if _, err := a.AuthorizeMFAIdentity(ctx, tx, actor); err != nil {
		t.Fatal("challenge identity rejected", err)
	}
}
