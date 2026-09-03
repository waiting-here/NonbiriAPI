package db

import (
	"database/sql"
	"strings"
	"testing"
)

// The tests in this file lock the durable DDL matrices for mainstream channel
// authority, endpoint provenance snapshots, per-key donation lifecycle with
// report-lineage facts, and the report cursor pair.
// Only the frozen database expression is under test here; HTTP/service/UI
// behavior belongs to the later implementation waves.

func hostileInsertChannelFixture(t *testing.T, db *sql.DB, id string, enabled int) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',?,'active',1,0,0)`,
		id, "channel-"+id[len(id)-3:], enabled)
}

func hostileDonationKeyColumns() string {
	return `
 id,donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,
 price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
 token_reserve,enabled,failure_disabled,failure_streak,streak_generation,next_claim_seq,next_fold_seq,
 safe_note,created_at,updated_at`
}

func hostileDonationKeyValues() string {
	return `
VALUES(?,?,?,'head','tail','https://upstream.example/v1','openai-compatible',?,?,?,?,?,?,
 0,1,0,?,?,?,?,'',0,0`
}

func hostileTerminalDonationKeyValues() string {
	return `
VALUES(?,?,NULL,'head','tail','https://upstream.example/v1','openai-compatible',?,?,?,?,?,?,
 0,0,0,?,?,?,?,'',0,0`
}

func hostileDonationKeyArgs(t *testing.T, db *sql.DB, donationID, endpointKeyID int64) []any {
	t.Helper()
	return append([]any{hostileNextPK64(t, db, "donation_keys"), donationID, endpointKeyID},
		hostileBlob16(0), hostileBlob16(0), hostileBlob16(0), hostileBlob16(0),
		hostileBlob16(0), hostileBlob16(0), hostileBlob16(0), hostileBlob16(1),
		hostileBlob16(1), hostileBlob16(0))
}

func hostileTerminalDonationKeyArgs(t *testing.T, db *sql.DB, donationID int64) []any {
	t.Helper()
	return append([]any{hostileNextPK64(t, db, "donation_keys"), donationID},
		hostileBlob16(0), hostileBlob16(0), hostileBlob16(0), hostileBlob16(0),
		hostileBlob16(0), hostileBlob16(0), hostileBlob16(0), hostileBlob16(1),
		hostileBlob16(1), hostileBlob16(0))
}

func TestGenerationTwoHostileMainstreamChannelMatrices(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)

	// Canonical insert is a positive control.
	valid := hostileOID("mch_")
	hostileInsertChannelFixture(t, db, valid, 1)
	var name string
	if err := db.QueryRow(`SELECT name FROM mainstream_channels WHERE id=?`, valid).Scan(&name); err != nil {
		t.Fatalf("read canonical channel: %v", err)
	}

	// OID boundaries and closed sets.
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'active',1,0,0)`,
		hostileBadOID("mch_"), "bad-oid")
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'active',1,0,0)`,
		hostileOID("rpc_"), "wrong-prefix")
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'other','openai-compatible','https://channel.example/v1',1,'active',1,0,0)`,
		valid, "bad-category")
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'subscription','unknown-connector','https://channel.example/v1',1,'active',1,0,0)`,
		valid, "bad-connector")
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'retiring',1,0,0)`,
		valid, "bad-state")
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'active',0,0,0)`,
		valid, "zero-revision")
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'active','1',0,0)`,
		valid, "text-revision")
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,'','subscription','openai-compatible','https://channel.example/v1',1,'active',1,0,0)`,
		valid, "empty-name")
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'active',1,0,0)`,
		valid, strings.Repeat("界", 129))
	hostileMustExec(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'active',1,0,0)`,
		hostileOIDVariant("mch_", 'B', 'Q'), strings.Repeat("界", 128))
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'active',1,'0','0')`,
		valid, "text-timestamps")

	// state/retired_at matrix: retired rows are disabled, carry retired_at,
	// and can never recover or be re-enabled.
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at,retired_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'retired',1,0,0,NULL)`,
		valid, "retired-null-time")
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at,retired_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'retired',1,0,0,0)`,
		valid, "retired-enabled")
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at,retired_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'active',1,0,0,0)`,
		valid, "active-with-retired-at")
	retiredID := hostileOIDVariant("mch_", 'C', 'Q')
	hostileMustExec(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at,retired_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',0,'retired',1,0,0,10)`,
		retiredID, "retired")
	hostileMustFail(t, db, `UPDATE mainstream_channels SET state='active',updated_at=11 WHERE id=?`, retiredID)
	hostileMustFail(t, db, `UPDATE mainstream_channels SET enabled=1,updated_at=11 WHERE id=?`, retiredID)

	// active+enabled is capped at 100 by a transactional invariant. Existing
	// active rows count toward the cap, so they are disabled first.
	hostileMustExec(t, db, `UPDATE mainstream_channels SET enabled=0,updated_at=1 WHERE id=?`, valid)
	hostileMustExec(t, db, `UPDATE mainstream_channels SET enabled=0,updated_at=1 WHERE id=?`,
		hostileOIDVariant("mch_", 'B', 'Q'))
	tails := []byte{'A', 'Q', 'g', 'w'}
	for i := 0; i < 100; i++ {
		hostileInsertChannelFixture(t, db, hostileOIDVariant("mch_", byte('a'+i%26), tails[i/26]), 1)
	}
	hostileMustFail(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'subscription','openai-compatible','https://channel.example/v1',1,'active',1,0,0)`,
		hostileOIDVariant("mch_", 'Z', 'Q'), "insert-limit-exceeded")
	// A disabled channel row never counts toward the cap and cannot be
	// enabled while the cap is already full.
	disabledID := hostileOIDVariant("mch_", '-', 'Q')
	hostileMustExec(t, db, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,?,'api_platform','anthropic-compatible','https://platform.example/v1',0,'active',1,0,0)`,
		disabledID, "disabled-below-cap")
	hostileMustFail(t, db, `UPDATE mainstream_channels SET enabled=1,updated_at=2 WHERE id=?`, disabledID)
	// Retiring one active channel frees exactly one slot again.
	hostileMustExec(t, db, `
UPDATE mainstream_channels SET state='retired',enabled=0,retired_at=20,updated_at=20 WHERE id=?`,
		hostileOIDVariant("mch_", 'a', 'A'))
	hostileMustExec(t, db, `UPDATE mainstream_channels SET enabled=1,updated_at=3 WHERE id=?`, disabledID)
}

func TestGenerationTwoHostileEndpointChannelSnapshotMatrix(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	userID := hostileInsertUser(t, db, "channel-snapshot", 0, 0)
	channelID := hostileOID("mch_")
	hostileInsertChannelFixture(t, db, channelID, 1)

	// Custom endpoints keep the whole snapshot null.
	customID := hostileInsertEndpoint(t, db, userID, "https://custom.example/v1")
	var channelRef sql.NullString
	if err := db.QueryRow(`SELECT mainstream_channel_id FROM endpoints WHERE id=?`, customID).Scan(&channelRef); err != nil {
		t.Fatal(err)
	}
	if channelRef.Valid {
		t.Fatal("custom endpoint must not carry channel provenance")
	}

	// All-or-none snapshot insertion and closed boundaries.
	hostileMustFail(t, db, `
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,
 mainstream_channel_id,mainstream_channel_revision,mainstream_channel_category,created_at,updated_at)
VALUES(?,'openai-compatible','https://main.example/v1','',1,1,?,1,'subscription',0,0)`,
		userID, channelID, "partial-no-name")
	hostileMustFail(t, db, `
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,
 mainstream_channel_id,mainstream_channel_revision,mainstream_channel_name,created_at,updated_at)
VALUES(?,'openai-compatible','https://main.example/v1','',1,1,?,1,'name',0,0)`,
		userID, channelID, "partial-no-category")
	hostileMustFail(t, db, `
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,
 mainstream_channel_id,mainstream_channel_revision,mainstream_channel_name,mainstream_channel_category,created_at,updated_at)
VALUES(?,'openai-compatible','https://main.example/v1','',1,1,?,1,'name','other',0,0)`,
		userID, channelID, "bad-category")
	hostileMustFail(t, db, `
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,
 mainstream_channel_id,mainstream_channel_revision,mainstream_channel_name,mainstream_channel_category,created_at,updated_at)
VALUES(?,'openai-compatible','https://main.example/v1','',1,1,?,0,'name','subscription',0,0)`,
		userID, channelID, "zero-revision")
	hostileMustFail(t, db, `
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,
 mainstream_channel_id,mainstream_channel_revision,mainstream_channel_name,mainstream_channel_category,created_at,updated_at)
VALUES(?,'openai-compatible','https://main.example/v1','',1,1,?,?,?,'subscription',0,0)`,
		userID, hostileBadOID("mch_"), 1, "bad-channel-oid")

	// Canonical mainstream snapshot.
	hostileMustExec(t, db, `
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,
 mainstream_channel_id,mainstream_channel_revision,mainstream_channel_name,mainstream_channel_category,created_at,updated_at)
VALUES(?,'openai-compatible','https://channel.example/v1','',1,1,?,1,'name','subscription',0,0)`,
		userID, channelID)
	endpointID := hostileNextPK64(t, db, "endpoints") - 1

	// The snapshot is immutable just like connector/URL identity.
	hostileMustFail(t, db, `
UPDATE endpoints SET mainstream_channel_id=? WHERE id=?`, hostileOIDVariant("mch_", 'Z', 'Q'), endpointID)
	hostileMustFail(t, db, `
UPDATE endpoints SET mainstream_channel_revision=2 WHERE id=?`, endpointID)
	hostileMustFail(t, db, `
UPDATE endpoints SET mainstream_channel_name='renamed' WHERE id=?`, endpointID)
	hostileMustFail(t, db, `
UPDATE endpoints SET mainstream_channel_category='api_platform' WHERE id=?`, endpointID)
	// A retired channel keeps satisfying the endpoint FK; the referenced
	// channel row itself can no longer be deleted (ON DELETE RESTRICT).
	hostileMustExec(t, db, `
UPDATE mainstream_channels SET state='retired',enabled=0,retired_at=5,updated_at=5 WHERE id=?`, channelID)
	hostileMustFail(t, db, `DELETE FROM mainstream_channels WHERE id=?`, channelID)
	// Ordinary endpoint updates that do not touch identity still succeed.
	hostileMustExec(t, db, `UPDATE endpoints SET note='updated',updated_at=9 WHERE id=?`, endpointID)
	// Deleting the user cascades endpoints without touching the channel.
	hostileMustExec(t, db, `DELETE FROM users WHERE id=?`, userID)
	var channels int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mainstream_channels`).Scan(&channels); err != nil {
		t.Fatal(err)
	}
	if channels == 0 {
		t.Fatal("channel authority must survive endpoint deletion")
	}
}

func TestGenerationTwoHostileDonationKeyExpiryAndTraceMatrix(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	userID := hostileInsertUser(t, db, "donation-expiry", 0, 0)
	donationID := hostileInsertDonation(t, db, userID)
	fixtureEndpointID := hostileInsertEndpoint(t, db, userID, "https://fixture.example/v1")
	fixtureSecretID := hostileInsertSecret(t, db, "https://fixture.example/v1", 3)
	fixtureEndpointKeyID := hostileInsertEndpointKey(t, db, fixtureEndpointID, fixtureSecretID)
	fingerprint := hostileBlob32(7)

	// Non-terminal keys require a fingerprint and no match deadline.
	args := hostileDonationKeyArgs(t, db, donationID, fixtureEndpointKeyID)
	hostileMustFail(t, db, `
INSERT INTO donation_keys(`+hostileDonationKeyColumns()+`,
 source_endpoint_key_id,report_fingerprint
)`+hostileDonationKeyValues()+`,?,NULL)`, append(args, fixtureEndpointKeyID)...)
	hostileMustFail(t, db, `
INSERT INTO donation_keys(`+hostileDonationKeyColumns()+`,
 source_endpoint_key_id,report_fingerprint,report_match_until
)`+hostileDonationKeyValues()+`,?,?,100)`, append(args, fixtureEndpointKeyID, fingerprint)...)

	// Donor upper bound requires an effective expiry within it.
	args = hostileDonationKeyArgs(t, db, donationID, fixtureEndpointKeyID)
	hostileMustFail(t, db, `
INSERT INTO donation_keys(`+hostileDonationKeyColumns()+`,
 authorized_expires_at,expires_at,source_endpoint_key_id,report_fingerprint
)`+hostileDonationKeyValues()+`,200,NULL,?,?)`, append(args, fixtureEndpointKeyID, fingerprint)...)
	hostileMustFail(t, db, `
INSERT INTO donation_keys(`+hostileDonationKeyColumns()+`,
 authorized_expires_at,expires_at,source_endpoint_key_id,report_fingerprint
)`+hostileDonationKeyValues()+`,200,201,?,?)`, append(args, fixtureEndpointKeyID, fingerprint)...)
	args = hostileDonationKeyArgs(t, db, donationID, fixtureEndpointKeyID)
	hostileMustFail(t, db, `
INSERT INTO donation_keys(`+hostileDonationKeyColumns()+`,
 authorized_expires_at,expires_at,source_endpoint_key_id,report_fingerprint
)`+hostileDonationKeyValues()+`,200,199,?,?)`, append(args, fixtureEndpointKeyID, fingerprint)...)

	// A finite donor bound equal to the effective expiry is canonical; the
	// effective value may move inside the bound but never beyond it.
	args = hostileDonationKeyArgs(t, db, donationID, fixtureEndpointKeyID)
	hostileMustExec(t, db, `
INSERT INTO donation_keys(`+hostileDonationKeyColumns()+`,
 authorized_expires_at,expires_at,source_endpoint_key_id,report_fingerprint
)`+hostileDonationKeyValues()+`,200,200,?,?)`, append(args, fixtureEndpointKeyID, fingerprint)...)
	finiteID := hostileNextPK64(t, db, "donation_keys") - 1

	// Effective expiry may be tightened, but never above the donor bound;
	// donor bound, provenance, and source identity are immutable.
	hostileMustExec(t, db, `UPDATE donation_keys SET expires_at=100,updated_at=1 WHERE id=?`, finiteID)
	hostileMustExec(t, db, `UPDATE donation_keys SET expires_at=200,updated_at=2 WHERE id=?`, finiteID)
	hostileMustFail(t, db, `UPDATE donation_keys SET expires_at=201,updated_at=3 WHERE id=?`, finiteID)
	hostileMustFail(t, db, `UPDATE donation_keys SET authorized_expires_at=300 WHERE id=?`, finiteID)
	otherDonationID := hostileInsertDonation(t, db, userID)
	hostileMustFail(t, db, `UPDATE donation_keys SET donation_id=? WHERE id=?`, otherDonationID, finiteID)
	hostileMustExec(t, db, `UPDATE donations SET status='rejected',terminal_at=10,updated_at=10 WHERE id=?`, otherDonationID)
	hostileMustFail(t, db, `UPDATE donations SET status='pending',terminal_at=NULL,updated_at=11 WHERE id=?`, otherDonationID)
	hostileMustFail(t, db, `UPDATE donations SET terminal_at=11,updated_at=11 WHERE id=?`, otherDonationID)
	hostileMustFail(t, db, `UPDATE donation_keys SET display_head='other' WHERE id=?`, finiteID)
	hostileMustFail(t, db, `UPDATE donation_keys SET display_tail='other' WHERE id=?`, finiteID)
	hostileMustFail(t, db, `UPDATE donation_keys SET canonical_base_url='https://other.example/v1' WHERE id=?`, finiteID)
	hostileMustFail(t, db, `UPDATE donation_keys SET connector_type='anthropic-compatible' WHERE id=?`, finiteID)
	hostileMustFail(t, db, `
UPDATE donation_keys SET mainstream_channel_id=?,mainstream_channel_revision=1,
 mainstream_channel_name='n',mainstream_channel_category='subscription' WHERE id=?`,
		hostileOID("mch_"), finiteID)
	hostileMustFail(t, db, `UPDATE donation_keys SET source_endpoint_key_id=424242 WHERE id=?`, finiteID)
	// Non-terminal keys can never drop their fingerprint.
	hostileMustFail(t, db, `UPDATE donation_keys SET report_fingerprint=NULL WHERE id=?`, finiteID)

	// Provenance snapshot is all-or-none with a valid channel reference.
	provenanceDonationID := hostileInsertDonation(t, db, userID)
	args = hostileDonationKeyArgs(t, db, provenanceDonationID, fixtureEndpointKeyID)
	hostileMustFail(t, db, `
INSERT INTO donation_keys(`+hostileDonationKeyColumns()+`,
 mainstream_channel_id,mainstream_channel_revision,mainstream_channel_category,
 source_endpoint_key_id,report_fingerprint
)`+hostileDonationKeyValues()+`,?,1,'subscription',?,?)`,
		append(args, hostileOID("mch_"), fixtureEndpointKeyID, fingerprint)...)
	channelID := hostileOID("mch_")
	hostileInsertChannelFixture(t, db, channelID, 1)
	hostileMustExec(t, db, `
INSERT INTO donation_keys(
 id,donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,
 price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
 token_reserve,enabled,failure_disabled,failure_streak,streak_generation,next_claim_seq,next_fold_seq,
 safe_note,created_at,updated_at,mainstream_channel_id,mainstream_channel_revision,
 mainstream_channel_name,mainstream_channel_category,source_endpoint_key_id,report_fingerprint
) VALUES(?,?,?,'head','tail','https://channel.example/v1','openai-compatible',?,?,?,?,?,?,
 0,1,0,?,?,?,?,'',0,0,?,1,'name','subscription',?,?)`,
		hostileNextPK64(t, db, "donation_keys"), provenanceDonationID, fixtureEndpointKeyID,
		hostileBlob16(0), hostileBlob16(0), hostileBlob16(0), hostileBlob16(0),
		hostileBlob16(0), hostileBlob16(0), hostileBlob16(0), hostileBlob16(1),
		hostileBlob16(1), hostileBlob16(0), channelID, fixtureEndpointKeyID, fingerprint)
	mainstreamKeyID := hostileNextPK64(t, db, "donation_keys") - 1

	// A live row cannot lose its physical key before becoming terminal.
	hostileMustFail(t, db, `UPDATE donation_keys SET endpoint_key_id=NULL WHERE id=?`, mainstreamKeyID)
	var sourceID int64
	if err := db.QueryRow(`SELECT source_endpoint_key_id FROM donation_keys WHERE id=?`, mainstreamKeyID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if sourceID != fixtureEndpointKeyID {
		t.Fatalf("source endpoint key identity changed: %d", sourceID)
	}
	// Deleting the referenced channel row is blocked while the snapshot exists.
	hostileMustFail(t, db, `DELETE FROM mainstream_channels WHERE id=?`, channelID)

	// A live physical key must preserve the exact same source identity. It can
	// only clear endpoint_key_id atomically with the live-to-terminal transition.
	endpointID := hostileInsertEndpoint(t, db, userID, "https://physical.example/v1")
	secretID := hostileInsertSecret(t, db, "https://physical.example/v1", 1)
	endpointKeyID := hostileInsertEndpointKey(t, db, endpointID, secretID)
	liveDonationKeyID := hostileInsertDonationKey(t, db, donationID, endpointKeyID)
	otherSecretID := hostileInsertSecret(t, db, "https://physical.example/v1", 2)
	otherEndpointKeyID := hostileInsertEndpointKey(t, db, endpointID, otherSecretID)
	hostileMustFail(t, db, `UPDATE donation_keys SET endpoint_key_id=? WHERE id=?`, otherEndpointKeyID, liveDonationKeyID)
	hostileMustFail(t, db, `UPDATE donation_keys SET endpoint_key_id=NULL WHERE id=?`, liveDonationKeyID)

	var dbNow int64
	if err := db.QueryRow(`SELECT unixepoch()`).Scan(&dbNow); err != nil {
		t.Fatalf("read database clock: %v", err)
	}
	transitionEndedAt := dbNow
	transitionMatchUntil := transitionEndedAt + 7776000
	// Even an otherwise legal effective expiry cannot be changed by the same
	// statement that makes the key terminal.
	hostileMustFail(t, db, `
UPDATE donation_keys
SET endpoint_key_id=NULL,enabled=0,ended_reason='expired',ended_at=?,report_match_until=?,
 expires_at=?,updated_at=?
WHERE id=?`, transitionEndedAt, transitionMatchUntil, dbNow+3600, dbNow, liveDonationKeyID)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin atomic donation-key terminalization: %v", err)
	}
	if _, err := tx.Exec(`
UPDATE donation_keys
SET endpoint_key_id=NULL,enabled=0,ended_reason='expired',ended_at=?,report_match_until=?,updated_at=?
WHERE id=?`, transitionEndedAt, transitionMatchUntil, dbNow, liveDonationKeyID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("atomically terminalize and clear donation key: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit atomic donation-key terminalization: %v", err)
	}
	hostileMustFail(t, db, `UPDATE donation_keys SET endpoint_key_id=? WHERE id=?`, endpointKeyID, liveDonationKeyID)

	// Terminal keys pin report_match_until to ended_at+90d and freeze every
	// terminal fact. Fingerprints survive until that exact deadline.
	futureEndedAt := dbNow - 7776000 + 60
	futureMatchUntil := dbNow + 60
	terminalArgs := hostileTerminalDonationKeyArgs(t, db, donationID)
	hostileMustFail(t, db, `
INSERT INTO donation_keys(`+hostileDonationKeyColumns()+`,
 ended_reason,ended_at,source_endpoint_key_id,report_fingerprint,report_match_until
)`+hostileTerminalDonationKeyValues()+`,'expired',?,?,?,NULL)`,
		append(terminalArgs, futureEndedAt, fixtureEndpointKeyID, fingerprint)...)
	terminalArgs = hostileTerminalDonationKeyArgs(t, db, donationID)
	hostileMustFail(t, db, `
INSERT INTO donation_keys(`+hostileDonationKeyColumns()+`,
 ended_reason,ended_at,source_endpoint_key_id,report_fingerprint,report_match_until
)`+hostileTerminalDonationKeyValues()+`,'expired',?,?,?,?)`,
		append(terminalArgs, futureEndedAt, fixtureEndpointKeyID, fingerprint, futureMatchUntil+1)...)
	terminalArgs = hostileTerminalDonationKeyArgs(t, db, donationID)
	hostileMustExec(t, db, `
INSERT INTO donation_keys(`+hostileDonationKeyColumns()+`,
 ended_reason,ended_at,source_endpoint_key_id,report_fingerprint,report_match_until
)`+hostileTerminalDonationKeyValues()+`,'expired',?,?,?,?)`,
		append(terminalArgs, futureEndedAt, fixtureEndpointKeyID, fingerprint, futureMatchUntil)...)
	terminalID := hostileNextPK64(t, db, "donation_keys") - 1
	hostileMustFail(t, db, `UPDATE donation_keys SET report_fingerprint=? WHERE id=?`, hostileBlob32(8), terminalID)
	hostileMustFail(t, db, `UPDATE donation_keys SET report_fingerprint=NULL WHERE id=?`, terminalID)
	hostileMustFail(t, db, `UPDATE donation_keys SET report_fingerprint=NULL,updated_at=? WHERE id=?`, futureMatchUntil-1, terminalID)
	// The lifecycle coordinator's frozen decision time authorizes cleanup at
	// the exact deadline. The one-way transition can never be reversed.
	hostileMustExec(t, db, `UPDATE donation_keys SET report_fingerprint=NULL,updated_at=? WHERE id=?`, futureMatchUntil, terminalID)
	hostileMustFail(t, db, `UPDATE donation_keys SET report_fingerprint=? WHERE id=?`, hostileBlob32(8), terminalID)
	hostileMustFail(t, db, `UPDATE donation_keys SET report_match_until=999999 WHERE id=?`, terminalID)
	hostileMustFail(t, db, `UPDATE donation_keys SET ended_at=ended_at+1,report_match_until=report_match_until+1 WHERE id=?`, terminalID)
	hostileMustFail(t, db, `UPDATE donation_keys SET ended_reason='withdrawn' WHERE id=?`, terminalID)
	hostileMustFail(t, db, `UPDATE donation_keys SET expires_at=1 WHERE id=?`, terminalID)
	hostileMustFail(t, db, `UPDATE donation_keys SET ended_at=NULL,ended_reason=NULL,report_match_until=NULL,enabled=1 WHERE id=?`, terminalID)

	// Once the deadline has elapsed the fingerprint clears exactly once and
	// can never be restored or replaced.
	pastEndedAt := dbNow - 7776001
	pastMatchUntil := dbNow - 1
	pastArgs := hostileTerminalDonationKeyArgs(t, db, donationID)
	hostileMustExec(t, db, `
INSERT INTO donation_keys(`+hostileDonationKeyColumns()+`,
 ended_reason,ended_at,source_endpoint_key_id,report_fingerprint,report_match_until
)`+hostileTerminalDonationKeyValues()+`,'expired',?,?,?,?)`,
		append(pastArgs, pastEndedAt, fixtureEndpointKeyID, fingerprint, pastMatchUntil)...)
	pastID := hostileNextPK64(t, db, "donation_keys") - 1
	hostileMustExec(t, db, `UPDATE donation_keys SET report_fingerprint=NULL,updated_at=? WHERE id=?`, pastMatchUntil, pastID)
	hostileMustFail(t, db, `UPDATE donation_keys SET report_fingerprint=? WHERE id=?`, hostileBlob32(9), pastID)
}

func TestGenerationTwoHostileReportCursorAndTargetSourceMatrix(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)

	// All-or-none cursor pair, closed source set, and endpoint cursor>0.
	hostileMustFail(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_source,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 retry_attempt_count,created_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1','approved','complete',
 1,1,1000,'endpoint',0,0,0,0,0,0,0,0,1)`, hostileOID("rpc_"), hostileBlob32(3))
	hostileMustFail(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_id,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 retry_attempt_count,created_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1','approved','complete',
 1,1,1000,5,0,0,0,0,0,0,0,0,1)`, hostileOID("rpc_"), hostileBlob32(3))
	hostileMustFail(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_source,cursor_id,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 retry_attempt_count,created_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1','approved','complete',
 1,1,1000,'model',5,0,0,0,0,0,0,0,0,1)`, hostileOID("rpc_"), hostileBlob32(3))
	hostileMustFail(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_source,cursor_id,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 retry_attempt_count,created_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1','approved','complete',
 1,1,1000,'endpoint',0,0,0,0,0,0,0,0,1)`, hostileOID("rpc_"), hostileBlob32(3))

	// Valid shapes: initial/complete null, donation phase zero, positive ids.
	for i, shape := range []struct {
		source, id any
	}{
		{nil, nil}, {"donation", int64(0)}, {"endpoint", int64(5)}, {"donation", int64(9)},
	} {
		hostileMustExec(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_source,cursor_id,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 retry_attempt_count,created_at,decision_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1','approved','complete',
 1,1,1000,?,?,0,0,0,0,0,0,0,0,0,1)`,
			hostileOIDVariant("rpc_", byte('A'+i), 'Q'), hostileBlob32(byte(20+i)), shape.source, shape.id)
	}

	// Target source identity is mandatory and integer-typed.
	reportID := hostileOIDVariant("rpc_", 'Z', 'Q')
	hostileInsertReportCase(t, db, reportID, hostileBlob32(9), "pending_review", "complete", 0)
	hostileMustFail(t, db, `
INSERT INTO report_targets(
 id,case_id,target_seq,endpoint_key_id,key_ref,connector_type,canonical_base_url,
 key_display_head,key_display_tail,state,discovered_version,created_at,updated_at
) VALUES(?,?,0,NULL,?,'openai-compatible','https://upstream.example/v1','head','tail','protected',1,0,0)`,
		hostileOID("rpt_"), reportID, hostileBlob32(3))
	hostileMustFail(t, db, `
INSERT INTO report_targets(
 id,case_id,target_seq,endpoint_key_id,source_endpoint_key_id,key_ref,connector_type,canonical_base_url,
 key_display_head,key_display_tail,state,discovered_version,created_at,updated_at
) VALUES(?,?,0,NULL,'x',?,'openai-compatible','https://upstream.example/v1','head','tail','protected',1,0,0)`,
		hostileOID("rpt_"), reportID, hostileBlob32(3))
	hostileMustExec(t, db, `
INSERT INTO report_targets(
 id,case_id,target_seq,endpoint_key_id,source_endpoint_key_id,key_ref,connector_type,canonical_base_url,
 key_display_head,key_display_tail,state,discovered_version,created_at,updated_at
) VALUES(?,?,0,NULL,7,?,'openai-compatible','https://upstream.example/v1','head','tail','protected',1,0,0)`,
		hostileOID("rpt_"), reportID, hostileBlob32(3))
	hostileMustFail(t, db, `UPDATE report_targets SET source_endpoint_key_id=8 WHERE case_id=?`, reportID)
}
