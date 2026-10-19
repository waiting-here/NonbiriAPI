package v1

const headerDiagnosticsSchema = `INSERT INTO site_config(key,value,updated_at) VALUES('request_header_budget_mib','1024',0) ON CONFLICT(key) DO NOTHING;
ALTER TABLE observability_state ADD COLUMN header_bytes INTEGER NOT NULL DEFAULT 0 CHECK(header_bytes>=0);
CREATE TABLE request_header_facts (
 request_log_id INTEGER NOT NULL REFERENCES request_logs(id) ON DELETE CASCADE,
 attempt_seq INTEGER NOT NULL CHECK(attempt_seq BETWEEN 0 AND 100),
 captured_at INTEGER NOT NULL CHECK(captured_at BETWEEN 0 AND 253402300799),
 http_status INTEGER CHECK(http_status BETWEEN 100 AND 599),
 save_state TEXT NOT NULL CHECK(save_state IN ('saved','capacity_exhausted','too_large','unavailable')),
 encoded BLOB,
 bytes_saved INTEGER NOT NULL CHECK(bytes_saved>=0 AND bytes_saved<=8388608),
 PRIMARY KEY(request_log_id,attempt_seq),
 CHECK((save_state='saved' AND encoded IS NOT NULL AND bytes_saved=length(encoded)) OR (save_state<>'saved' AND encoded IS NULL AND bytes_saved=0)),
 CHECK((attempt_seq=0 AND http_status IS NULL) OR attempt_seq>0)
) STRICT;
CREATE TRIGGER request_header_insert_count AFTER INSERT ON request_header_facts
BEGIN UPDATE observability_state SET header_bytes=header_bytes+NEW.bytes_saved WHERE id=1; END;
CREATE TRIGGER request_header_delete_count AFTER DELETE ON request_header_facts
BEGIN UPDATE observability_state SET header_bytes=header_bytes-OLD.bytes_saved WHERE id=1; END;
`
