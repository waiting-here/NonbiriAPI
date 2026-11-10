package db

// KeyQueueSQL keeps existing keys on immediate rejection until queueing is chosen.
const KeyQueueSQL = `ALTER TABLE endpoint_key_limits ADD COLUMN concurrency_overflow TEXT NOT NULL DEFAULT 'reject' CHECK(concurrency_overflow IN ('reject','queue'));
ALTER TABLE endpoint_key_limits ADD COLUMN rpm_overflow TEXT NOT NULL DEFAULT 'reject' CHECK(rpm_overflow IN ('reject','queue'));
ALTER TABLE endpoint_key_limits ADD COLUMN concurrency_queue_limit INTEGER NOT NULL DEFAULT 0 CHECK(typeof(concurrency_queue_limit)='integer' AND concurrency_queue_limit BETWEEN 0 AND 2147483647);
ALTER TABLE endpoint_key_limits ADD COLUMN rpm_queue_limit INTEGER NOT NULL DEFAULT 0 CHECK(typeof(rpm_queue_limit)='integer' AND rpm_queue_limit BETWEEN 0 AND 2147483647);
`
