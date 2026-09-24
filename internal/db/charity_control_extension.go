package db

const charityControlSchema = `
ALTER TABLE donations ADD COLUMN discord_public_thanks INTEGER CHECK(discord_public_thanks IN (0,1));
CREATE TRIGGER donation_thanks_immutable BEFORE UPDATE OF discord_public_thanks ON donations
WHEN NEW.user_id IS NOT NULL AND OLD.discord_public_thanks IS NOT NEW.discord_public_thanks
BEGIN SELECT RAISE(ABORT,'donation public thanks choice is immutable'); END;
ALTER TABLE charity_models ADD COLUMN is_mainstream INTEGER NOT NULL DEFAULT 0 CHECK(is_mainstream IN (0,1));
ALTER TABLE charity_models ADD COLUMN excluded_request_fields TEXT NOT NULL DEFAULT '[]'
 CHECK(json_valid(excluded_request_fields) AND json_type(excluded_request_fields)='array' AND length(excluded_request_fields)<=2200);
`
