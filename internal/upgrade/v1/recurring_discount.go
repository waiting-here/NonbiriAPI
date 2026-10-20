package v1

const recurringDiscountSchema = `
ALTER TABLE charity_models ADD COLUMN recurring_discount TEXT NOT NULL DEFAULT '{"enabled":false,"time_zone":"UTC","default_percent":100,"periods":[]}' CHECK(json_valid(recurring_discount) AND json_type(recurring_discount)='object' AND length(CAST(recurring_discount AS BLOB))<=4096);
ALTER TABLE charity_price_templates ADD COLUMN recurring_discount TEXT NOT NULL DEFAULT '{"enabled":false,"time_zone":"UTC","default_percent":100,"periods":[]}' CHECK(json_valid(recurring_discount) AND json_type(recurring_discount)='object' AND length(CAST(recurring_discount AS BLOB))<=4096);
`
