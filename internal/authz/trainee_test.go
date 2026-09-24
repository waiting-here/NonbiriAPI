package authz

import (
	"context"
	"errors"
	"testing"
)

func TestTraineeAuthorityDoesNotGrantFullStewardAccess(t *testing.T) {
	store := openAuthStore(t)
	level := int64(5)
	user := insertAuthUser(t, store.DB(), "trainee-fixture", "trainee-session", "g1", false, &level)
	actor := Actor{Kind: ActorUserSession, UserID: user, SessionTokenHash: "trainee-session", SessionGeneration: "g1"}
	authorizer := testAuthorizer(nil)
	for _, transition := range []struct {
		level            int
		trainee, steward error
	}{{5, nil, ErrForbidden}, {6, ErrForbidden, nil}, {4, ErrForbidden, ErrForbidden}} {
		if _, err := store.DB().Exec("UPDATE users SET level=? WHERE id=?", transition.level, user); err != nil {
			t.Fatal(err)
		}
		tx := beginAuthTx(t, store.DB())
		_, traineeErr := authorizer.Authorize(context.Background(), tx, actor, Requirement{Role: RoleTrainee})
		_, stewardErr := authorizer.Authorize(context.Background(), tx, actor, Requirement{Role: RoleSteward})
		_ = tx.Rollback()
		if !errors.Is(traineeErr, transition.trainee) || !errors.Is(stewardErr, transition.steward) {
			t.Fatalf("level %d: trainee=%v steward=%v", transition.level, traineeErr, stewardErr)
		}
	}
}
