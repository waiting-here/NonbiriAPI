CREATE INDEX idx_charity_reservations_retention ON charity_reservations(finalized_at,logical_request_id) WHERE state IN ('committed','released');
CREATE INDEX idx_donation_usage_retention ON donation_usage_reservations(finalized_at,claim_id) WHERE state IN ('committed','released');
