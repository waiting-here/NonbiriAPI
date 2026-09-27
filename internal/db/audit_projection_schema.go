package db

const auditProjectionSchema = `
ALTER TABLE worker_checkpoints ADD COLUMN last_success_at INTEGER CHECK(last_success_at IS NULL OR (typeof(last_success_at)='integer' AND last_success_at BETWEEN 0 AND 253402300799 AND last_success_at<=updated_at));
ALTER TABLE admin_alerts ADD COLUMN context_version INTEGER NOT NULL DEFAULT 0 CHECK(context_version IN (0,1));
ALTER TABLE admin_alerts ADD COLUMN context_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(context_json) AND json_type(context_json)='object' AND length(CAST(context_json AS BLOB))<=16384);
ALTER TABLE admin_alerts ADD COLUMN resolution_kind TEXT NOT NULL DEFAULT '' CHECK(resolution_kind IN ('','manual','automatic_blacklist','worker_recovered','legacy'));
ALTER TABLE risk_client_scans ADD COLUMN kind TEXT NOT NULL DEFAULT 'client_hits' CHECK(kind IN ('client_hits','users','shared_ips'));
ALTER TABLE risk_client_scans ADD COLUMN filter_revision INTEGER NOT NULL DEFAULT 1 CHECK(filter_revision>0);
ALTER TABLE risk_client_scans ADD COLUMN checkpoint_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(checkpoint_json) AND json_type(checkpoint_json)='object' AND length(CAST(checkpoint_json AS BLOB))<=16384);
ALTER TABLE risk_client_scans ADD COLUMN coverage_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(coverage_json) AND json_type(coverage_json)='object' AND length(CAST(coverage_json AS BLOB))<=4096);
ALTER TABLE risk_client_scans ADD COLUMN changed INTEGER NOT NULL DEFAULT 0 CHECK(changed IN (0,1));
DROP INDEX idx_risk_scans_unfinished;
CREATE INDEX idx_risk_scans_unfinished ON risk_client_scans(user_id,state) WHERE state IN ('queued','running');
CREATE TRIGGER risk_scan_capacity BEFORE INSERT ON risk_client_scans
WHEN (SELECT count(*) FROM risk_client_scans)>=200
 OR (NEW.state IN ('queued','running') AND ((SELECT count(*) FROM risk_client_scans WHERE state IN ('queued','running'))>=20
 OR (SELECT count(*) FROM risk_client_scans WHERE user_id=NEW.user_id AND state IN ('queued','running'))>=2))
BEGIN SELECT RAISE(ABORT,'audit scan capacity exhausted'); END;
CREATE TRIGGER risk_scan_capacity_update BEFORE UPDATE OF state,user_id ON risk_client_scans
WHEN NEW.state IN ('queued','running') AND (OLD.state NOT IN ('queued','running') OR NEW.user_id<>OLD.user_id)
 AND ((SELECT count(*) FROM risk_client_scans WHERE state IN ('queued','running') AND id<>OLD.id)>=20
 OR (SELECT count(*) FROM risk_client_scans WHERE user_id=NEW.user_id AND state IN ('queued','running') AND id<>OLD.id)>=2)
BEGIN SELECT RAISE(ABORT,'audit scan capacity exhausted'); END;
CREATE TABLE risk_scan_results (
 scan_id TEXT NOT NULL REFERENCES risk_client_scans(id) ON DELETE CASCADE,
 row_no INTEGER NOT NULL CHECK(row_no BETWEEN 1 AND 100000),
 user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
 request_log_id INTEGER REFERENCES request_source_facts(request_log_id) ON DELETE CASCADE,
 published INTEGER NOT NULL DEFAULT 0 CHECK(published IN (0,1)),
 result_json TEXT NOT NULL CHECK(json_valid(result_json) AND json_type(result_json)='object' AND length(CAST(result_json AS BLOB))<=16384),
 PRIMARY KEY(scan_id,row_no),
 CHECK(user_id IS NOT NULL OR request_log_id IS NOT NULL)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_risk_scan_results_user ON risk_scan_results(user_id,scan_id,row_no);
CREATE INDEX idx_risk_scan_results_source ON risk_scan_results(request_log_id,scan_id,row_no);
CREATE TABLE risk_scan_result_users (
 scan_id TEXT NOT NULL,
 row_no INTEGER NOT NULL,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 PRIMARY KEY(scan_id,row_no,user_id),
 FOREIGN KEY(scan_id,row_no) REFERENCES risk_scan_results(scan_id,row_no) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_risk_scan_result_users_source ON risk_scan_result_users(user_id,scan_id,row_no);
CREATE TABLE risk_scan_result_sources (
 scan_id TEXT NOT NULL,
 row_no INTEGER NOT NULL,
 request_log_id INTEGER NOT NULL REFERENCES request_source_facts(request_log_id) ON DELETE CASCADE,
 PRIMARY KEY(scan_id,row_no,request_log_id),
 FOREIGN KEY(scan_id,row_no) REFERENCES risk_scan_results(scan_id,row_no) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_risk_scan_result_sources_source ON risk_scan_result_sources(request_log_id,scan_id,row_no);
INSERT INTO risk_scan_results(scan_id,row_no,request_log_id,published,result_json)
 SELECT scan_id,ordinal,request_log_id,1,'{}' FROM risk_client_scan_matches;
INSERT INTO risk_scan_result_sources(scan_id,row_no,request_log_id)
 SELECT scan_id,ordinal,request_log_id FROM risk_client_scan_matches;
CREATE TRIGGER risk_scan_user_retired BEFORE DELETE ON users
BEGIN
 UPDATE risk_client_scans SET changed=1 WHERE id IN (SELECT scan_id FROM risk_scan_results WHERE user_id=OLD.id
 UNION SELECT scan_id FROM risk_scan_result_users WHERE user_id=OLD.id);
 DELETE FROM risk_scan_results WHERE user_id=OLD.id OR (scan_id,row_no) IN (SELECT scan_id,row_no FROM risk_scan_result_users WHERE user_id=OLD.id);
END;
CREATE TRIGGER risk_scan_result_source_retired AFTER UPDATE OF user_id ON request_source_facts WHEN NEW.user_id IS NULL
BEGIN
 UPDATE risk_client_scans SET changed=1 WHERE id IN (SELECT scan_id FROM risk_scan_results WHERE request_log_id=NEW.request_log_id
 UNION SELECT scan_id FROM risk_scan_result_sources WHERE request_log_id=NEW.request_log_id);
 DELETE FROM risk_scan_results WHERE request_log_id=NEW.request_log_id
 OR (scan_id,row_no) IN (SELECT scan_id,row_no FROM risk_scan_result_sources WHERE request_log_id=NEW.request_log_id);
END;
CREATE TRIGGER risk_scan_result_source_deleted BEFORE DELETE ON request_source_facts
BEGIN
 UPDATE risk_client_scans SET changed=1 WHERE id IN (SELECT scan_id FROM risk_scan_results WHERE request_log_id=OLD.request_log_id
 UNION SELECT scan_id FROM risk_scan_result_sources WHERE request_log_id=OLD.request_log_id);
 DELETE FROM risk_scan_results WHERE request_log_id=OLD.request_log_id
 OR (scan_id,row_no) IN (SELECT scan_id,row_no FROM risk_scan_result_sources WHERE request_log_id=OLD.request_log_id);
END;
ALTER TABLE game_rank_expiry_work ADD COLUMN net_bidding_delta_sign INTEGER NOT NULL DEFAULT 0 CHECK(net_bidding_delta_sign IN(-1,0,1));
ALTER TABLE game_rank_expiry_work ADD COLUMN net_bidding_delta_mag BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000' CHECK(length(net_bidding_delta_mag)=32 AND (net_bidding_delta_sign=0)=(net_bidding_delta_mag=zeroblob(32)));
ALTER TABLE game_rank_expiry_work ADD COLUMN net_bidding_last_seq BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(length(net_bidding_last_seq)=16);
CREATE TABLE game_bidding_net_rebuild (
 id INTEGER PRIMARY KEY CHECK(id=1),
 state TEXT NOT NULL CHECK(state IN ('pending','scanning','publishing','completed')),
 watermark BLOB CHECK(watermark IS NULL OR length(watermark)=16),
 last_seq BLOB NOT NULL CHECK(length(last_seq)=16),
 history_coverage_start INTEGER CHECK(history_coverage_start BETWEEN 0 AND 253402300799),
 missing_events INTEGER NOT NULL CHECK(missing_events>=0),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE game_bidding_net_rebuild_events (
 source_seq BLOB NOT NULL PRIMARY KEY REFERENCES game_rank_events(seq) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 loss_sign INTEGER NOT NULL CHECK(loss_sign IN (-1,0,1)),
 loss_mag BLOB NOT NULL CHECK(length(loss_mag)=16 AND (loss_sign=0)=(loss_mag=zeroblob(16))),
 settled_at INTEGER NOT NULL CHECK(settled_at BETWEEN 0 AND 253401695999)
) STRICT;
CREATE INDEX idx_bidding_rebuild_events_user ON game_bidding_net_rebuild_events(user_id,source_seq);
CREATE TABLE game_bidding_net_rebuild_totals (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 amount_sign INTEGER NOT NULL CHECK(amount_sign IN (-1,0,1)),
 amount_mag BLOB NOT NULL CHECK(length(amount_mag)=32 AND (amount_sign=0)=(amount_mag=zeroblob(32))),
 achieved_at INTEGER NOT NULL CHECK(achieved_at BETWEEN 0 AND 253402300799),
 achieved_phase INTEGER NOT NULL CHECK(achieved_phase IN (0,1)),
 achieved_seq BLOB NOT NULL CHECK(length(achieved_seq)=16)
) STRICT;
`
