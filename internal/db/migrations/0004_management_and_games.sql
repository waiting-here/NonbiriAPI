CREATE TABLE admin_endpoint_tags (
 base_url TEXT NOT NULL CHECK(length(base_url) BETWEEN 1 AND 4096),
 tag TEXT NOT NULL CHECK(tag IN ('abusive_third_party','community_charity')),
 PRIMARY KEY(base_url,tag)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_admin_endpoint_tags_tag ON admin_endpoint_tags(tag,base_url);

ALTER TABLE request_error_bodies ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '' CHECK(failure_reason IN ('','read_failure','storage_failure'));
CREATE INDEX idx_request_errors_missing ON request_error_bodies(id) WHERE save_state<>'saved';
