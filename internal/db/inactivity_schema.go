package db

import (
	"context"
	"database/sql"
)

const inactivitySchema = `
ALTER TABLE users ADD COLUMN ban_kind TEXT NOT NULL DEFAULT '' CHECK(ban_kind='' OR (ban_kind='protective_inactivity' AND is_admin=0 AND is_banned=1 AND banned_until IS NULL));
CREATE TABLE inactivity_policy (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL CHECK(revision>=1),
 config_json TEXT NOT NULL CHECK(json_valid(config_json) AND length(CAST(config_json AS BLOB))<=4096),
 decay_grace_until INTEGER NOT NULL CHECK(decay_grace_until BETWEEN 0 AND 253402300799),
 protection_grace_until INTEGER NOT NULL CHECK(protection_grace_until BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL
) STRICT;
CREATE TABLE user_activity_state (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 observation_started_at INTEGER NOT NULL CHECK(observation_started_at BETWEEN 0 AND 253402300799),
 last_active_at INTEGER CHECK(last_active_at IS NULL OR last_active_at BETWEEN 0 AND 253402300799),
 activity_seq INTEGER NOT NULL DEFAULT 0 CHECK(activity_seq>=0),
 activity_epoch INTEGER NOT NULL DEFAULT 0 CHECK(activity_epoch>=0),
 schedule_revision INTEGER NOT NULL DEFAULT 0 CHECK(schedule_revision>=0),
 next_due_at INTEGER CHECK(next_due_at IS NULL OR next_due_at BETWEEN 0 AND 253402300799),
 last_decay_at INTEGER CHECK(last_decay_at IS NULL OR last_decay_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_activity_schedule ON user_activity_state(schedule_revision,user_id);
CREATE INDEX idx_activity_due ON user_activity_state(next_due_at,user_id) WHERE next_due_at IS NOT NULL;
CREATE TABLE inactivity_runs (
 id TEXT PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 policy_revision INTEGER NOT NULL CHECK(policy_revision>=1),
 activity_epoch INTEGER NOT NULL CHECK(activity_epoch>=0),
 due_slot INTEGER NOT NULL CHECK(due_slot BETWEEN 0 AND 253402300799),
 action TEXT NOT NULL CHECK(action IN ('decay','protection')),
 general_milli TEXT NOT NULL CHECK(length(general_milli) BETWEEN 1 AND 39 AND general_milli NOT GLOB '*[^0-9]*' AND (general_milli='0' OR substr(general_milli,1,1)<>'0') AND (length(general_milli)<39 OR general_milli<='170141183460469231731687303715884105727')),
 game_milli TEXT NOT NULL CHECK(length(game_milli) BETWEEN 1 AND 39 AND game_milli NOT GLOB '*[^0-9]*' AND (game_milli='0' OR substr(game_milli,1,1)<>'0') AND (length(game_milli)<39 OR game_milli<='170141183460469231731687303715884105727')),
 ledger_operation_id TEXT REFERENCES credit_operations(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 deidentify_at INTEGER NOT NULL CHECK(deidentify_at BETWEEN 0 AND 253402300799 AND deidentify_at=created_at+7776000),
 retain_until INTEGER NOT NULL CHECK(retain_until BETWEEN 0 AND 253402300799 AND retain_until=created_at+34560000),
 UNIQUE(user_id,policy_revision,activity_epoch,due_slot)
) STRICT;
CREATE INDEX idx_inactivity_runs_user ON inactivity_runs(user_id,created_at,id);
CREATE INDEX idx_inactivity_runs_retention ON inactivity_runs(retain_until,id);
CREATE INDEX idx_inactivity_runs_identity ON inactivity_runs(deidentify_at,id) WHERE user_id IS NOT NULL;
CREATE TRIGGER inactivity_runs_immutable BEFORE UPDATE ON inactivity_runs
WHEN NEW.id<>OLD.id OR NEW.policy_revision<>OLD.policy_revision OR NEW.activity_epoch<>OLD.activity_epoch
 OR NEW.due_slot<>OLD.due_slot OR NEW.action<>OLD.action OR NEW.general_milli<>OLD.general_milli OR NEW.game_milli<>OLD.game_milli
 OR NEW.created_at<>OLD.created_at OR NEW.deidentify_at<>OLD.deidentify_at OR NEW.retain_until<>OLD.retain_until
 OR (NEW.user_id IS NOT OLD.user_id AND NOT (OLD.user_id IS NOT NULL AND NEW.user_id IS NULL))
 OR (NEW.ledger_operation_id IS NOT OLD.ledger_operation_id AND NOT (OLD.ledger_operation_id IS NOT NULL AND NEW.ledger_operation_id IS NULL))
BEGIN SELECT RAISE(ABORT,'inactivity execution receipts are immutable'); END;
CREATE TABLE inactivity_audits (
 id TEXT PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 action TEXT NOT NULL CHECK(action IN ('configure','preview')),
 policy_revision INTEGER NOT NULL CHECK(policy_revision>=1),
 details_json TEXT NOT NULL CHECK(json_valid(details_json) AND length(CAST(details_json AS BLOB))<=4096),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 deidentify_at INTEGER NOT NULL CHECK(deidentify_at BETWEEN 0 AND 253402300799 AND deidentify_at=created_at+7776000),
 retain_until INTEGER NOT NULL CHECK(retain_until BETWEEN 0 AND 253402300799 AND retain_until=created_at+34560000)
) STRICT;
CREATE INDEX idx_inactivity_audits_retention ON inactivity_audits(retain_until,id);
CREATE INDEX idx_inactivity_audits_identity ON inactivity_audits(deidentify_at,id) WHERE actor_user_id IS NOT NULL;
`

func seedInactivity(ctx context.Context, tx *sql.Tx, at int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO inactivity_policy VALUES(1,1,?,0,0,?,NULL)`,
		`{"enabled":false,"decay":{"enabled":false,"inactive_days":null,"interval_days":null,"assets":{"general":null,"game":null}},"protection":{"enabled":false,"inactive_days":null}}`, at)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_activity_state(user_id,observation_started_at) SELECT id,? FROM users`, at)
	return err
}
