package db

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func progressionSourceFixture(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(generationTwoWithoutProgressionSchema); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, preProgressionManifestHash)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := seedGenerationTwo(context.Background(), tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestProgressionFreshAndPublishedSourceUpgrade(t *testing.T) {
	ctx := context.Background()
	fresh := openGenerationTwoDDLForTest(t)
	tx, err := fresh.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := seedGenerationTwo(ctx, tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	seed, err := readGenerationTwoFreshConfigSeedRows(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := generationTwoFreshConfigSeedDigest(seed)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fresh config seed sha256=%s", hash)
	want, err := readGenerationManifest(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	prior := progressionSourceFixture(t)
	if err := extendKnownGenerationTwoSchema(ctx, prior); err != nil {
		t.Fatal(err)
	}
	got, err := readGenerationManifest(ctx, prior)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh/upgrade differ: %s / %s: %v", generationManifestDigest(got), generationManifestDigest(want), err)
	}
	var epoch int64
	if err := prior.QueryRow(`SELECT started_at FROM game_statistics_epoch`).Scan(&epoch); err != nil || epoch <= 0 {
		t.Fatal(epoch, err)
	}
	if err := extendKnownGenerationTwoSchema(ctx, prior); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := prior.QueryRow(`SELECT started_at FROM game_statistics_epoch`).Scan(&after); err != nil || after != epoch {
		t.Fatal("epoch changed", after, err)
	}
	if _, err := prior.Exec(`UPDATE game_statistics_epoch SET started_at=0`); err == nil {
		t.Fatal("epoch mutation accepted")
	}
}

func TestProgressionQuickStakeMigrationUsesSourceLimits(t *testing.T) {
	for _, tc := range []struct{ name, min, max, step, def, want string }{
		{"default", "1000000", "50000000", "1000000", "5000000", `["1000","5000","10000","50000"]`},
		{"filtered", "5000000", "15000000", "5000000", "10000000", `["5000","10000"]`},
		{"single", "10000000", "10000000", "1000000", "10000000", `["10000"]`},
		{"fallback", "3000000", "9000000", "3000000", "6000000", `["6000"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := progressionSourceFixture(t)
			for key, value := range map[string]string{"min_stake": tc.min, "max_stake": tc.max, "stake_step": tc.step, "default_stake": tc.def} {
				if _, err := database.Exec(`UPDATE site_config SET value=? WHERE key=?`, value, "game_blackjack_"+key+"_milli"); err != nil {
					t.Fatal(err)
				}
			}
			if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := database.QueryRow(`SELECT value FROM site_config WHERE key='game_blackjack_quick_stakes'`).Scan(&got); err != nil || got != tc.want {
				t.Fatal(got, tc.want, err)
			}
			if _, err := database.Exec(`UPDATE site_config SET value='[]' WHERE key='game_blackjack_quick_stakes'`); err != nil {
				t.Fatal(err)
			}
			if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
				t.Fatal(err)
			}
			if err := database.QueryRow(`SELECT value FROM site_config WHERE key='game_blackjack_quick_stakes'`).Scan(&got); err != nil || got != "[]" {
				t.Fatal("disabled buttons lost", got, err)
			}
		})
	}
}

func TestProgressionMissingDonationEvidenceRollsBack(t *testing.T) {
	database := progressionSourceFixture(t)
	user := hostileInsertUser(t, database, "donation-source", 0, 0)
	if _, err := database.Exec(`UPDATE users SET donation_credit_mag=X'00000000000000000000000000000001' WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	err := extendKnownGenerationTwoSchema(context.Background(), database)
	if err == nil || !strings.Contains(err.Error(), "no ledger achievement") {
		t.Fatal("invalid donation evidence accepted", err)
	}
	assertRetainedManifest(t, database, preProgressionManifestHash)
	var got int
	if err := database.QueryRow(`SELECT count(*) FROM site_config WHERE key LIKE 'activity_loan_%'`).Scan(&got); err != nil || got != 0 {
		t.Fatal("partial configuration committed", got, err)
	}
}

func TestLoanTermsExactArithmeticAndBounds(t *testing.T) {
	got, err := CalculateLoanTerms("10000", "900", "1300")
	if err != nil || got.Nominal != 10000000 || got.Disbursed != 9000000 || got.Fee != 1000000 || got.Repayment != 13000000 || got.Interest != 3000000 {
		t.Fatal(got, err)
	}
	for _, values := range [][3]string{{"0", "900", "1300"}, {"1e4", "900", "1300"}, {"10000", "1000", "1300"}, {"10000", "900", "1000"}, {"9000000000000", "900", "1300"}, {"10000", "900", "99999999999999999999999999999999"}} {
		if _, err := CalculateLoanTerms(values[0], values[1], values[2]); err == nil {
			t.Fatal("invalid terms accepted", values)
		}
	}
}
