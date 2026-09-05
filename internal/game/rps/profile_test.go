package rps

import (
	"context"
	"testing"
)

func TestTutorialPreservesPublicGameProfile(t *testing.T) {
	fixture := newRPSFixture(t)
	userID, _ := fixture.seedUser("public-player", 0)
	if _, err := fixture.database.Exec(`UPDATE users SET game_profile_public=1 WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := fixture.service.MarkTutorialSeen(context.Background(), userID); err != nil {
			t.Fatal(err)
		}
	}
	var seen, public int
	if err := fixture.database.QueryRow(`SELECT tutorial_rps_seen,game_profile_public FROM game_user_preferences WHERE user_id=?`, userID).Scan(&seen, &public); err != nil {
		t.Fatal(err)
	}
	if seen != 1 || public != 1 {
		t.Fatalf("tutorial=%d public=%d; completing the tutorial changed privacy", seen, public)
	}
}
