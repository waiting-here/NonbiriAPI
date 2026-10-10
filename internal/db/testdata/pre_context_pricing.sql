ALTER TABLE charity_models DROP COLUMN context_pricing;
ALTER TABLE charity_reservations DROP COLUMN context_pricing;
UPDATE schema_state SET version=10 WHERE id=1;
