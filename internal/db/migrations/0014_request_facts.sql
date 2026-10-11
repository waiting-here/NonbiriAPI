ALTER TABLE request_logs ADD COLUMN requested_streaming INTEGER CHECK(requested_streaming IN (0,1));
ALTER TABLE request_logs ADD COLUMN automatic_actions INTEGER CHECK(automatic_actions IS NULL OR (typeof(automatic_actions)='integer' AND automatic_actions BETWEEN 0 AND 63));
