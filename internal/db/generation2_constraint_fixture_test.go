package db

import (
	"fmt"
	"sync"
	"testing"
)

func TestGenerationTwoConstraintFixtureIsolation(t *testing.T) {
	var paths sync.Map
	t.Run("parallel-private-copies", func(t *testing.T) {
		for index := range 4 {
			t.Run(fmt.Sprintf("copy-%d", index), func(t *testing.T) {
				t.Parallel()
				database, path := openGenerationTwoDDLTestImage(t, generationTwoConstraintImageForTest(t))
				if _, exists := paths.LoadOrStore(path, true); exists {
					t.Fatal("constraint fixtures share a database file")
				}
				var users int
				if err := database.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil || users != 0 {
					t.Fatalf("constraint fixture inherited users=%d, err=%v", users, err)
				}
				hostileInsertUser(t, database, fmt.Sprintf("private-copy-%d", index), 0, 0)
				if err := database.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil || users != 1 {
					t.Fatalf("constraint fixture leaked users=%d, err=%v", users, err)
				}
			})
		}
	})
	// The builder and all first consumers have run their cleanups. The image
	// must still be usable without retaining any of their temporary paths.
	t.Run("after-owner-cleanup", func(t *testing.T) {
		database := openGenerationTwoConstraintFixture(t)
		var users int
		if err := database.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil || users != 0 {
			t.Fatalf("constraint fixture after cleanup users=%d, err=%v", users, err)
		}
	})
}
