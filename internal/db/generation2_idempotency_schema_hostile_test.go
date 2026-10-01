package db

import (
	"testing"
)

func TestGenerationTwoHostileIdempotencyExpiryAcrossScopes(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
	scopes := []string{
		"credential_report",
		"control_mutation",
		"openai_chat_completions",
		"charity_chat_completions",
		"model_discovery",
		"maintenance",
		"announcement",
		"activity",
		"game_fishing",
		"game_linklink",
		"game_rps",
		"donation",
	}
	for i, scope := range scopes {
		t.Run(scope, func(t *testing.T) {
			actorHash := hostileBlob32(byte(i + 1))
			keyHash := hostileBlob32(byte(i + 33))
			requestHash := hostileBlob32(byte(i + 65))
			lookup := hostileBlob32(byte(i + 97))
			if scope == "credential_report" {
				hostileMustExec(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,state,http_status,
 response_body,created_at,expires_at
) VALUES(?,?,?,?,?,'accepted',0,?,0,86400)`, scope, actorHash, keyHash, requestHash, lookup, []byte{})
				hostileMustFail(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,state,http_status,
 response_body,created_at,expires_at
) VALUES(?,?,?,?,?,'accepted',0,?,0,86399)`, scope, hostileBlob32(byte(i+2)), hostileBlob32(byte(i+34)), hostileBlob32(byte(i+66)), lookup, []byte{})
				hostileMustFail(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,state,http_status,
 response_body,created_at,expires_at
) VALUES(?,?,?,?,?,'accepted',0,?,0,86401)`, scope, hostileBlob32(byte(i+3)), hostileBlob32(byte(i+35)), hostileBlob32(byte(i+67)), lookup, []byte{})
				return
			}
			hostileMustExec(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at
) VALUES(?,?,?,?,'accepted',0,?,0,86400)`, scope, actorHash, keyHash, requestHash, []byte{})
			hostileMustFail(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at
) VALUES(?,?,?,?,'accepted',0,?,0,86399)`, scope, hostileBlob32(byte(i+2)), hostileBlob32(byte(i+34)), hostileBlob32(byte(i+66)), []byte{})
			hostileMustFail(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at
) VALUES(?,?,?,?,'accepted',0,?,0,86401)`, scope, hostileBlob32(byte(i+3)), hostileBlob32(byte(i+35)), hostileBlob32(byte(i+67)), []byte{})
		})
	}
	hostileMustFail(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at
) VALUES('unknown',?,?,?,'accepted',0,?,0,86400)`, hostileBlob32(240), hostileBlob32(241), hostileBlob32(242), []byte{})
}

func TestGenerationTwoHostileIdempotencyIdentityAndTransitions(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
	actorHash := hostileBlob32(1)
	keyHash := hostileBlob32(2)
	requestHash := hostileBlob32(3)
	hostileMustExec(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at
) VALUES('openai_chat_completions',?,?,?,'accepted',0,?,0,86400)`, actorHash, keyHash, requestHash, []byte{})

	// The idempotency identity and expiry are acceptance facts, not mutable
	// bookkeeping.  Each update keeps the value otherwise representable so a
	// missing immutable guard cannot hide behind a type/check failure.
	hostileMustFail(t, db, `UPDATE idempotency_records SET scope='activity' WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET actor_scope_hash=? WHERE scope='openai_chat_completions' AND key_hash=?`, hostileBlob32(4), keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET key_hash=? WHERE scope='openai_chat_completions' AND actor_scope_hash=?`, hostileBlob32(5), actorHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET request_hash=? WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, hostileBlob32(6), actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET lookup_fingerprint=? WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, hostileBlob32(7), actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET created_at=1,expires_at=86401 WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET expires_at=86401 WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, actorHash, keyHash)

	// Exactly one accepted -> completed transition is legal; completion data
	// and the state are then immutable, with no completed -> accepted revival.
	hostileMustExec(t, db, `UPDATE idempotency_records SET state='completed',http_status=200,response_body=? WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, []byte("ok"), actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET state='accepted',http_status=0,response_body=? WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, []byte{}, actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET http_status=201 WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET response_body=? WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, []byte("tampered"), actorHash, keyHash)

	credentialActor := hostileBlob32(11)
	credentialKey := hostileBlob32(12)
	credentialRequest := hostileBlob32(13)
	credentialLookup := hostileBlob32(14)
	hostileMustExec(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,state,http_status,
 response_body,created_at,expires_at
) VALUES('credential_report',?,?,?,?, 'accepted',0,?,0,86400)`, credentialActor, credentialKey, credentialRequest, credentialLookup, []byte{})
	hostileMustFail(t, db, `UPDATE idempotency_records SET lookup_fingerprint=? WHERE scope='credential_report' AND actor_scope_hash=? AND key_hash=?`, hostileBlob32(15), credentialActor, credentialKey)
}
