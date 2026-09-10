package db

// Missing rows inherit the global reservation. Overrides are model settings,
// and disappear with the model rather than an individual request or creator.
const charityModelReserveSchema = `
CREATE TABLE charity_model_token_reserves (
    model_id INTEGER PRIMARY KEY REFERENCES charity_models(id) ON DELETE CASCADE,
    amount_milli INTEGER NOT NULL CHECK(typeof(amount_milli)='integer' AND amount_milli BETWEEN 1 AND 9000000000000000)
);
`
