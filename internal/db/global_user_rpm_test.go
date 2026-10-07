package db

import (
	"context"
	"testing"
)

func TestGlobalUserRPMFreshDefault(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	epoch, err := GenerateOpaqueID("b1e_")
	if err != nil {
		t.Fatal(err)
	}
	if err := insertGenerationTwoConfig(context.Background(), tx, epoch); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"global_rpm_per_user", "default_rpm_per_user"} {
		var value string
		if err := tx.QueryRow(`SELECT value FROM site_config WHERE key=?`, key).Scan(&value); err != nil || value != "60" {
			t.Fatalf("default %s=%q: %v", key, value, err)
		}
	}
	if err := validateGenerationTwoFreshConfigSeed(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
}
