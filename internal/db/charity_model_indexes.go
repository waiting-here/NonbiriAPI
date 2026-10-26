package db

// CharityModelDeletionIndexesSQL bounds foreign-key lookups to the deleted
// model's rows while preserving history nullification and cache cascades.
const CharityModelDeletionIndexesSQL = `CREATE INDEX idx_charity_reservations_model ON charity_reservations(charity_model_id);
CREATE INDEX idx_charity_dispatch_receipts_model ON charity_dispatch_receipts(model_id);
CREATE INDEX idx_charity_affinities_model ON charity_key_affinities(model_id);
CREATE INDEX idx_charity_header_sessions_model ON charity_header_sessions(model_id);
CREATE INDEX idx_charity_header_uses_model ON charity_header_uses(model_id);
`
