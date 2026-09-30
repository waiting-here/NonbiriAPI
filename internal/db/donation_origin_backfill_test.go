package db

import (
	"context"
	"testing"
)

func TestStorageContractsBackfillsProvenApprovalOrigins(t *testing.T) {
	database := deployedStorageFixture(t)
	user := hostileInsertUser(t, database, "origin-donor", 0, 0)
	endpoint := hostileInsertEndpoint(t, database, user, "https://upstream.example/v1")
	key := hostileInsertEndpointKey(t, database, endpoint, hostileInsertSecret(t, database, "https://upstream.example/v1", 1))
	secondKey := hostileInsertEndpointKey(t, database, endpoint, hostileInsertSecret(t, database, "https://upstream.example/v1", 2))
	channels := []string{"mch_AAAAAAAAAAAAAAAAAAAAAA", "mch_BBBBBBBBBBBBBBBBBBBBBA"}
	for i, category := range []string{"subscription", "api_platform"} {
		hostileMustExec(t, database, `INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,'Channel',?,'openai-compatible','https://upstream.example/v1',1,'active',1,0,0)`, channels[i], category)
	}
	member := func(donation, physical int64, channel *string, category string) {
		t.Helper()
		var channelID, revision, name, snapshotCategory any
		if channel != nil {
			channelID, revision, name, snapshotCategory = *channel, 1, "Channel", category
		}
		hostileMustExec(t, database, `INSERT INTO donation_keys(
donation_id,endpoint_key_id,canonical_base_url,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
failure_streak,streak_generation,next_claim_seq,next_fold_seq,created_at,updated_at,source_endpoint_key_id,report_fingerprint,
mainstream_channel_id,mainstream_channel_revision,mainstream_channel_name,mainstream_channel_category)
VALUES(?,?,'https://upstream.example/v1',zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),
zeroblob(16),?, ?,zeroblob(16),0,0,?,zeroblob(32),?,?,?,?)`, donation, physical, hostileBlob16(1), hostileBlob16(1), physical, channelID, revision, name, snapshotCategory)
	}
	approve := func(donation, revision int64, role string, at int64) {
		hostileMustExec(t, database, `INSERT INTO donation_reviews(donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,NULL,?,'approve','',?)`, donation, revision, role, at)
	}
	wants := map[int64]string{}
	for _, name := range []string{"subscription", "api_platform", "deleted-reviewer", "custom", "duplicate", "late", "mixed", "empty", "no-review"} {
		donation := hostileInsertDonation(t, database, user)
		wants[donation] = "unknown"
		switch name {
		case "subscription", "api_platform":
			index := 0
			if name == "api_platform" {
				index = 1
			}
			member(donation, key, &channels[index], name)
			approve(donation, 1, "", 0)
			approve(donation, 3, "admin", 20)
			hostileMustExec(t, database, `INSERT INTO donation_reviews(donation_id,submission_revision,reviewer_role,action,note,created_at)
VALUES(?,4,'level6','limit_update','Updated limits',30)`, donation)
			wants[donation] = "auto"
		case "deleted-reviewer":
			member(donation, key, nil, "")
			approve(donation, 2, "level6", 10)
			wants[donation] = "manual"
		case "custom":
			member(donation, key, nil, "")
			approve(donation, 1, "", 0)
		case "duplicate":
			member(donation, key, &channels[0], "subscription")
			approve(donation, 1, "", 0)
			approve(donation, 1, "admin", 0)
		case "late":
			member(donation, key, &channels[0], "subscription")
			approve(donation, 1, "", 10)
		case "mixed":
			member(donation, key, &channels[0], "subscription")
			member(donation, secondKey, &channels[1], "api_platform")
			approve(donation, 1, "", 0)
		case "empty":
			approve(donation, 1, "", 0)
		case "no-review":
			member(donation, key, &channels[0], "subscription")
		}
	}
	for range 2 {
		if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
			t.Fatal(err)
		}
		for id, want := range wants {
			var got string
			if err := database.QueryRow("SELECT first_approval_origin FROM donations WHERE id=?", id).Scan(&got); err != nil || got != want {
				t.Fatalf("donation %d origin=%q want=%q: %v", id, got, want, err)
			}
		}
	}
}
