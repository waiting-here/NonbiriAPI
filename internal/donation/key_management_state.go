package donation

// Disabled approved uses remain maintainable. Execution authorization and
// admission limits are evaluated separately when a request is dispatched.
const manageableKeySQL = `d.status IN ('pending','approved') AND d.terminal_at IS NULL
AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?)
AND EXISTS(SELECT 1 FROM donation_uses u WHERE u.donation_key_id=dk.id AND u.current_approved_revision_id IS NOT NULL)
AND EXISTS(SELECT 1 FROM donation_key_memberships m JOIN endpoint_keys k ON k.id=m.endpoint_key_id
 JOIN endpoints e ON e.id=k.endpoint_id WHERE m.donation_key_id=dk.id AND m.donation_id=d.id
 AND m.endpoint_key_id=dk.endpoint_key_id AND e.user_id=d.user_id)`

const manageableDonationSQL = `EXISTS(SELECT 1 FROM donation_keys dk WHERE dk.donation_id=d.id AND ` + manageableKeySQL + `)`
