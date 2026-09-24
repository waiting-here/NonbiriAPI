package idempotency

import "testing"

func TestStewardRenumberingPreservesReplayIdentity(t *testing.T) {
	legacy, err := ActorScopeHash("level5", "42")
	if err != nil {
		t.Fatal(err)
	}
	current, err := ActorScopeHash("level6", "42")
	if err != nil || current != legacy {
		t.Fatal("full steward replay identity changed", err)
	}
	for _, kind := range []string{"trainee5", "user", "admin"} {
		separate, err := ActorScopeHash(kind, "42")
		if err != nil || separate == legacy {
			t.Fatalf("%s can address a steward receipt: %v", kind, err)
		}
	}
	other, err := ActorScopeHash("level6", "43")
	if err != nil || other == legacy {
		t.Fatal("different actor shares a receipt identity", err)
	}
}
