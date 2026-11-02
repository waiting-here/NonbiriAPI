package db

// ReadPerformanceIndexesSQL avoids table lookups for frequently refreshed
// public statistics. Existing indexes retain their broader lifecycle uses.
const ReadPerformanceIndexesSQL = `CREATE INDEX idx_charity_outcomes_success ON charity_request_outcomes(model_id,completed_at,result) WHERE dispatched=1;
CREATE INDEX idx_fishing_rank_active_user ON game_fishing_rank_facts(user_id,expires_at,batch_id_text) WHERE aggregate_applied=1;
`

const LakeNotesLiveLeaseIndexSQL = `CREATE INDEX idx_lake_notes_live_lease ON lake_notes_casts(lease_until_ns,id) WHERE phase IN ('waiting','playing') AND paused=0;
`
