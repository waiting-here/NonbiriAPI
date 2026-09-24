package authz

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestStewardCallerRechecksLiveAuthority(t *testing.T) {
	for _, test := range []struct {
		name, update string
		generation   int64
		want         error
	}{
		{"active", "", 1, nil},
		{"rotated", "UPDATE caller_keys SET generation=2,key_hash=zeroblob(32),display_head='newh',display_tail='newt' WHERE user_id=?", 1, ErrUnauthorized},
		{"revoked", "UPDATE caller_keys SET generation=2,key_hash=NULL,display_head='',display_tail='',key_created_at=NULL WHERE user_id=?", 1, ErrUnauthorized},
		{"demoted", "UPDATE users SET level=4 WHERE id=?", 1, ErrForbidden},
		{"trainee", "UPDATE users SET level=5 WHERE id=?", 1, ErrForbidden},
		{"automatic level", "UPDATE users SET level=NULL WHERE id=?", 1, ErrForbidden},
		{"banned", "UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?", 1, ErrForbidden},
		{"expired ban", "UPDATE users SET is_banned=1,banned_until=1 WHERE id=?", 1, nil},
		{"wrong generation", "", 2, ErrUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openAuthStore(t)
			level := int64(6)
			userID := insertAuthUser(t, store.DB(), "caller-fixture", "caller-session", "g1", false, &level)
			if _, err := store.DB().Exec(`INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at) VALUES(?,1,?,'head','tail',?,?)`, userID, bytes.Repeat([]byte{1}, 32), authTestNow, authTestNow); err != nil {
				t.Fatal(err)
			}
			ctx := WithStewardCaller(context.Background(), StewardCaller{UserID: userID, Generation: test.generation})
			if test.update != "" {
				if _, err := store.DB().Exec(test.update, userID); err != nil {
					t.Fatal(err)
				}
			}
			tx := beginAuthTx(t, store.DB())
			defer tx.Rollback()
			authorizer := testAuthorizer(nil)
			principal, err := authorizer.AuthorizeStewardCaller(ctx, tx, userID)
			if !errors.Is(err, test.want) {
				t.Fatalf("authority = %v, want %v", err, test.want)
			}
			if err == nil && (principal.EffectiveLevel != 6 || principal.Role != RoleSteward || principal.SessionBinding != "") {
				t.Fatalf("wrong principal: %#v", principal)
			}
			if _, err := authorizer.AuthorizeStewardCaller(ctx, tx, userID+1); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("foreign identity accepted: %v", err)
			}
			if _, err := authorizer.Authorize(ctx, tx, Actor{}, Requirement{Role: RoleAdministrator}); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("caller replaced session authority: %v", err)
			}
		})
	}
}
