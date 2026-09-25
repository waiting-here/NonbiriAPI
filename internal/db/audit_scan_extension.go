package db

import (
	"context"
	"database/sql"
	"errors"
)

const preAuditScanManifestHash = "fb66fca4e735b786a546f1ff0bd02ecb9f48adc4522dbadce2c5f8158498558b"

const auditScanSchema = `
CREATE TABLE image_discovery_dispatches (
 operation_id TEXT PRIMARY KEY REFERENCES image_model_refreshes(operation_id) ON DELETE CASCADE,
 method TEXT NOT NULL CHECK(method='GET'),
 url TEXT NOT NULL CHECK(length(CAST(url AS BLOB)) BETWEEN 1 AND 8192),
 request_body TEXT NOT NULL DEFAULT '' CHECK(request_body=''),
 request_content_type TEXT NOT NULL DEFAULT '' CHECK(request_content_type=''),
 dispatched_at INTEGER NOT NULL CHECK(dispatched_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE risk_client_scans (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='scn_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 admin INTEGER NOT NULL CHECK(admin IN (0,1)),
 request_token TEXT NOT NULL CHECK(length(request_token) BETWEEN 16 AND 64 AND request_token NOT GLOB '*[^A-Za-z0-9_-]*'),
 query_json TEXT NOT NULL CHECK(json_valid(query_json) AND length(CAST(query_json AS BLOB))<=4096),
 rules_json TEXT NOT NULL CHECK(json_valid(rules_json) AND json_type(rules_json)='array' AND length(CAST(rules_json AS BLOB))<=16777216),
 state TEXT NOT NULL CHECK(state IN ('queued','running','completed','cancelled','limited','failed')),
 reason TEXT NOT NULL DEFAULT '' CHECK(reason IN ('','result_limit','permission_changed','scan_failed')),
 from_at INTEGER NOT NULL CHECK(from_at BETWEEN 0 AND 253402300799),
 to_at INTEGER NOT NULL CHECK(to_at>from_at AND to_at-from_at<=2592000),
 call_kind TEXT NOT NULL CHECK(call_kind IN ('total','self','charity','unclassified')),
 model TEXT NOT NULL CHECK(length(CAST(model AS BLOB))<=512),
 upper_log_id INTEGER NOT NULL CHECK(upper_log_id>=0),
 after_at INTEGER NOT NULL CHECK(after_at>=from_at AND after_at<to_at),
 after_log_id INTEGER NOT NULL DEFAULT 0 CHECK(after_log_id BETWEEN 0 AND upper_log_id),
 candidates INTEGER NOT NULL CHECK(candidates>=0),
 scanned INTEGER NOT NULL DEFAULT 0 CHECK(scanned>=0),
 matched INTEGER NOT NULL DEFAULT 0 CHECK(matched BETWEEN 0 AND 100000),
 failures INTEGER NOT NULL DEFAULT 0 CHECK(failures BETWEEN 0 AND 3),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402292399),
 updated_at INTEGER NOT NULL CHECK(updated_at>=created_at),
 expires_at INTEGER NOT NULL CHECK(expires_at=created_at+86400),
 UNIQUE(user_id,request_token)
) STRICT;
CREATE UNIQUE INDEX idx_risk_scans_unfinished ON risk_client_scans(user_id) WHERE state IN ('queued','running');
CREATE INDEX idx_risk_scans_queue ON risk_client_scans(state,created_at,id);
CREATE INDEX idx_risk_scans_expiry ON risk_client_scans(expires_at,id);
CREATE INDEX idx_risk_scans_owner ON risk_client_scans(user_id,created_at DESC,id);
CREATE TABLE risk_client_scan_matches (
 scan_id TEXT NOT NULL REFERENCES risk_client_scans(id) ON DELETE CASCADE,
 request_log_id INTEGER NOT NULL REFERENCES request_source_facts(request_log_id) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 1 AND 100000),
 PRIMARY KEY(scan_id,request_log_id),
 UNIQUE(scan_id,ordinal)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_risk_scan_matches_log ON risk_client_scan_matches(request_log_id,scan_id);
CREATE INDEX idx_request_sources_scan ON request_source_facts(occurred_at,request_log_id) WHERE user_id IS NOT NULL AND kind IN ('self','charity','unclassified');
CREATE TRIGGER risk_scan_source_retired AFTER UPDATE OF user_id ON request_source_facts WHEN NEW.user_id IS NULL
BEGIN DELETE FROM risk_client_scan_matches WHERE request_log_id=NEW.request_log_id; END;
CREATE TRIGGER risk_scan_authority_changed AFTER UPDATE OF is_admin,level,auto_level,is_banned,banned_until ON users
BEGIN
 UPDATE risk_client_scans SET state='cancelled',reason='permission_changed'
 WHERE user_id=NEW.id AND state IN ('queued','running')
 AND ((admin=1 AND NEW.is_admin<>1) OR (admin=0 AND (NEW.is_admin<>0 OR COALESCE(NEW.level,NEW.auto_level)<>6)) OR NEW.is_banned=1);
END;
`

func applyAuditScanExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preAuditScanManifestHash {
		return errors.New("unrecognized audit scan source manifest")
	}
	_, err = tx.ExecContext(ctx, auditScanSchema)
	return err
}
