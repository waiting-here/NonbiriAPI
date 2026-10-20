package v1

// Runtime episodes and dispatch facts survive retirement of per-key settings.
const automaticBackoffSchema = `ALTER TABLE donation_key_backoffs DROP COLUMN profile;
ALTER TABLE donation_key_backoffs DROP COLUMN rules_json;
ALTER TABLE dispatch_smart_backoff_facts DROP COLUMN profile;
`
