package db

import (
	"context"
	"testing"
)

func TestDiscordGatePolicyPreservesAccountAndConfiguration(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
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
	userID := hostileInsertUser(t, database, "gate-account", 0, 0)
	hostileMustExec(t, database, "UPDATE users SET username='gate-account' WHERE id=?", userID)
	hostileMustExec(t, database, "UPDATE site_config SET value='preserved-guild',updated_at=42 WHERE key='discord_guild_id'")
	for range 2 {
		var policy, name, exempt, guild string
		var updated int
		if err := database.QueryRow(`SELECT username,discord_gate_policy FROM users WHERE id=?`, userID).Scan(&name, &policy); err != nil || policy != "inherit" || name != "gate-account" {
			t.Fatal("account changed during migration", name, policy, err)
		}
		if err := database.QueryRow(`SELECT value FROM site_config WHERE key='discord_registered_user_gate_exempt'`).Scan(&exempt); err != nil || exempt != "0" {
			t.Fatal("upgrade default is not strict", exempt, err)
		}
		if err := database.QueryRow(`SELECT value,updated_at FROM site_config WHERE key='discord_guild_id'`).Scan(&guild, &updated); err != nil || guild != "preserved-guild" || updated != 42 {
			t.Fatal("existing gate configuration changed", guild, updated, err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	}
	for _, policy := range []string{"require", "exempt", "inherit"} {
		hostileMustExec(t, database, "UPDATE users SET discord_gate_policy=? WHERE id=?", policy, userID)
	}
	for _, invalid := range []any{"", "allow", true, nil, []byte("exempt")} {
		hostileMustFail(t, database, "UPDATE users SET discord_gate_policy=? WHERE id=?", invalid, userID)
	}
}

func TestDiscordGateFreshDefault(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := seedGenerationTwo(context.Background(), tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := validateGenerationTwoFreshConfigSeed(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	userID := hostileInsertUser(t, database, "gate-default", 0, 0)
	var policy, global string
	if err := database.QueryRow(`SELECT discord_gate_policy,(SELECT value FROM site_config WHERE key='discord_registered_user_gate_exempt') FROM users WHERE id=?`, userID).Scan(&policy, &global); err != nil || policy != "inherit" || global != "0" {
		t.Fatal(policy, global, err)
	}
}
