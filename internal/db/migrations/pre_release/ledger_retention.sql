CREATE TABLE credit_compaction (
 id INTEGER PRIMARY KEY CHECK(id=1),
 through_seq INTEGER NOT NULL DEFAULT 0 CHECK(through_seq>=0),
 details_before INTEGER NOT NULL DEFAULT 0 CHECK(details_before BETWEEN 0 AND 253402300799),
 sweep_at INTEGER NOT NULL DEFAULT 0 CHECK(sweep_at BETWEEN 0 AND 253402300799),
 sweep_after_seq INTEGER NOT NULL DEFAULT 0 CHECK(sweep_after_seq>=0)
) STRICT;
INSERT INTO credit_compaction(id) VALUES(1);
CREATE TABLE credit_opening_balances (
 account_id INTEGER PRIMARY KEY REFERENCES credit_accounts(id) ON DELETE CASCADE,
 balance_sign INTEGER NOT NULL CHECK(balance_sign IN (-1,0,1)),
 balance_mag BLOB NOT NULL CHECK(length(balance_mag)=16 AND hex(balance_mag)<'80000000000000000000000000000000'),
 CHECK((balance_sign=0 AND balance_mag=zeroblob(16)) OR (balance_sign<>0 AND balance_mag<>zeroblob(16)))
) STRICT;
ALTER TABLE credit_operations ADD COLUMN compacted INTEGER NOT NULL DEFAULT 0 CHECK(compacted IN (0,1));
DROP TRIGGER credit_operations_no_update;
CREATE TRIGGER credit_operations_no_update BEFORE UPDATE ON credit_operations
WHEN NOT (
 NEW.id IS OLD.id AND NEW.ledger_seq IS OLD.ledger_seq AND NEW.kind IS OLD.kind AND
 NEW.source_type IS OLD.source_type AND NEW.source_id IS OLD.source_id AND NEW.source_seq IS OLD.source_seq AND
 NEW.created_at IS OLD.created_at AND (
  (NEW.compacted IS OLD.compacted AND
   NEW.donation_credit_delta_sign IS OLD.donation_credit_delta_sign AND NEW.donation_credit_delta_mag IS OLD.donation_credit_delta_mag AND
   NEW.donation_credit_after IS OLD.donation_credit_after AND NEW.reason IS OLD.reason AND
   (NEW.actor_user_id IS OLD.actor_user_id OR (NEW.actor_user_id IS NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id))) AND
   (NEW.donation_credit_user_id IS OLD.donation_credit_user_id OR (NEW.donation_credit_user_id IS NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.donation_credit_user_id))))
  OR (OLD.compacted=0 AND NEW.compacted=1 AND OLD.ledger_seq<=(SELECT through_seq FROM credit_compaction WHERE id=1) AND
   NEW.actor_user_id IS NULL AND NEW.donation_credit_user_id IS NULL AND NEW.donation_credit_delta_sign=0 AND
   NEW.donation_credit_delta_mag=zeroblob(16) AND NEW.donation_credit_after IS NULL AND NEW.reason IS NULL)
 ))
BEGIN SELECT RAISE(ABORT,'credit_operations is immutable outside compaction'); END;
DROP TRIGGER credit_operations_no_delete;
CREATE TRIGGER credit_operations_no_delete BEFORE DELETE ON credit_operations
WHEN OLD.compacted<>1 OR OLD.ledger_seq>(SELECT through_seq FROM credit_compaction WHERE id=1)
BEGIN SELECT RAISE(ABORT,'credit operation is not compacted'); END;
DROP TRIGGER credit_entries_no_delete;
CREATE TRIGGER credit_entries_no_delete BEFORE DELETE ON credit_entries
WHEN NOT EXISTS(SELECT 1 FROM credit_operations WHERE id=OLD.operation_id AND compacted=1
 AND ledger_seq<=(SELECT through_seq FROM credit_compaction WHERE id=1))
BEGIN SELECT RAISE(ABORT,'credit entry is not compacted'); END;
CREATE INDEX idx_inactivity_run_operation ON inactivity_runs(ledger_operation_id) WHERE ledger_operation_id IS NOT NULL;
CREATE INDEX idx_abuse_action_operation ON abuse_actions(operation_id) WHERE operation_id IS NOT NULL;
CREATE INDEX idx_lake_entitlement_operation ON lake_notes_entitlements(ledger_operation_id) WHERE ledger_operation_id IS NOT NULL;
CREATE INDEX idx_lake_exchange_operation ON lake_notes_exchange_receipts(ledger_operation_id);
CREATE INDEX idx_linklink_operation ON game_linklink_sessions(operation_id);
CREATE INDEX idx_rps_queue_operation ON game_rps_queue(reservation_operation_id);
CREATE INDEX idx_rps_terminal_operation ON game_rps_sessions(terminal_operation_id) WHERE terminal_operation_id IS NOT NULL;
CREATE INDEX idx_image_reserve_operation ON image_activity_tasks(reserve_operation_id) WHERE reserve_operation_id IS NOT NULL;
CREATE INDEX idx_image_terminal_operation ON image_activity_tasks(terminal_operation_id) WHERE terminal_operation_id IS NOT NULL;
