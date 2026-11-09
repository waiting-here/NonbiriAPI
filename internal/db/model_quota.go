package db

// ModelQuotaSQL adds independent usage scopes without attributing legacy usage.
const ModelQuotaSQL = `
ALTER TABLE donation_quota_rules ADD COLUMN upstream_model_id TEXT NOT NULL DEFAULT '' CHECK(typeof(upstream_model_id)='text' AND length(upstream_model_id)<=512 AND instr(upstream_model_id,char(0))=0);
DROP INDEX idx_donation_quota_rules_key_order;
CREATE UNIQUE INDEX idx_donation_quota_rules_key_order ON donation_quota_rules(donation_key_id,upstream_model_id,display_order) WHERE current_epoch IS NOT NULL;
CREATE INDEX idx_donation_quota_rules_scope ON donation_quota_rules(donation_key_id,upstream_model_id);
ALTER TABLE donation_usage_reservations ADD COLUMN upstream_model_id TEXT NOT NULL DEFAULT '' CHECK(typeof(upstream_model_id)='text' AND length(upstream_model_id)<=512 AND instr(upstream_model_id,char(0))=0);
CREATE INDEX idx_donation_usage_model_reserved ON donation_usage_reservations(donation_key_id,upstream_model_id,claim_id) WHERE state='reserved';
CREATE TABLE donation_model_quotas (
 donation_key_id INTEGER NOT NULL REFERENCES donation_keys(id) ON DELETE CASCADE CHECK(typeof(donation_key_id)='integer' AND donation_key_id>0),
 upstream_model_id TEXT NOT NULL CHECK(typeof(upstream_model_id)='text' AND length(upstream_model_id) BETWEEN 1 AND 512 AND instr(upstream_model_id,char(0))=0),
 token_limit_mag BLOB CHECK(token_limit_mag IS NULL OR (typeof(token_limit_mag)='blob' AND length(token_limit_mag)=16)),
 input_token_limit_mag BLOB CHECK(input_token_limit_mag IS NULL OR (typeof(input_token_limit_mag)='blob' AND length(input_token_limit_mag)=16)),
 output_token_limit_mag BLOB CHECK(output_token_limit_mag IS NULL OR (typeof(output_token_limit_mag)='blob' AND length(output_token_limit_mag)=16)),
 tokens_used BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(tokens_used)='blob' AND length(tokens_used)=16),
 input_tokens_used BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(input_tokens_used)='blob' AND length(input_tokens_used)=16),
 output_tokens_used BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(output_tokens_used)='blob' AND length(output_tokens_used)=16),
 tokens_reserved BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(tokens_reserved)='blob' AND length(tokens_reserved)=16),
 input_tokens_reserved BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(input_tokens_reserved)='blob' AND length(input_tokens_reserved)=16),
 output_tokens_reserved BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(output_tokens_reserved)='blob' AND length(output_tokens_reserved)=16),
 unattributed_total_tokens BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(unattributed_total_tokens)='blob' AND length(unattributed_total_tokens)=16),
 created_at INTEGER NOT NULL CHECK(typeof(created_at)='integer' AND created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(donation_key_id,upstream_model_id)
) WITHOUT ROWID;
`

// LakeExchangeDailyIndexSQL bounds daily allowance reads to one user and direction.
const LakeExchangeDailyIndexSQL = `
CREATE INDEX idx_lake_exchange_daily_source ON lake_notes_exchange_receipts(user_id,direction,created_at,source_amount_mag);
`
