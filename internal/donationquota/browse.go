package donationquota

import "fmt"

// AvailabilityPredicate is the set-based counterpart of Available for browse
// COUNT queries. Arguments are trusted SQL expressions supplied by repository
// code, never request text. All tables remain in the caller's authorized read
// snapshot. Reservation and dispatch continue to use the transactional engine.
func AvailabilityPredicate(keyID, decisionNow, credits, tokens string) string {
	return fmt.Sprintf(`EXISTS (
WITH token_budget AS MATERIALIZED (
 SELECT tk.*,COALESCE(tk.input_token_reserve+tk.output_token_reserve,%[4]s) AS effective_reserve
 FROM donation_keys tk WHERE tk.id=%[1]s
), live_quota AS MATERIALIZED (
 SELECT e.*,MAX(%[2]s,e.effective_at,COALESCE(e.last_observed_at,0)) AS observed_now
 FROM donation_quota_rules r JOIN donation_quota_epochs e
   ON e.rule_id=r.id AND e.epoch=r.current_epoch
 WHERE r.donation_key_id=%[1]s
), quota_window AS MATERIALIZED (
 SELECT q.*,
 CASE WHEN q.mode='sliding' THEN nbi_calendar_subtract(q.observed_now,q.interval,q.time_zone) END AS window_start,
 CASE WHEN q.mode='reset' THEN
   CASE WHEN p.start_at<=q.observed_now AND q.observed_now<p.end_at THEN p.start_at
        WHEN q.alignment='calendar' THEN nbi_calendar_start(q.observed_now,q.interval,q.time_zone,COALESCE(q.week_starts_on,0)) END
 END AS period_start
 FROM live_quota q LEFT JOIN donation_quota_periods p
   ON p.rule_id=q.rule_id AND p.epoch=q.epoch AND p.start_at=q.current_period_start
), quota_remaining AS MATERIALIZED (
 SELECT q.metric,q.retired_at,nbi_u128_remaining(q.limit_mag,
 COALESCE(CASE WHEN q.mode='sliding' THEN
   (SELECT nbi_u128_sum(b.used_mag) FROM donation_quota_buckets b
    WHERE b.rule_id=q.rule_id AND b.epoch=q.epoch AND b.success_at>q.window_start AND b.success_at<=q.observed_now)
 ELSE (SELECT p.used_mag FROM donation_quota_periods p
       WHERE p.rule_id=q.rule_id AND p.epoch=q.epoch AND p.start_at=q.period_start) END,nbi_u128(0)),
 COALESCE(CASE WHEN q.mode='sliding' THEN
   (SELECT nbi_u128_sum(b.reserved_mag) FROM donation_quota_buckets b
    WHERE b.rule_id=q.rule_id AND b.epoch=q.epoch AND b.success_at>q.window_start AND b.success_at<=q.observed_now)
 ELSE (SELECT p.reserved_mag FROM donation_quota_periods p
       WHERE p.rule_id=q.rule_id AND p.epoch=q.epoch AND p.start_at=q.period_start) END,nbi_u128(0)),
 q.pending_reserved) AS remaining
 FROM quota_window q
)
SELECT 1 FROM token_budget b WHERE
 (b.input_token_limit_mag IS NULL OR (b.input_token_reserve IS NOT NULL
   AND nbi_u128_remaining(b.input_token_limit_mag,b.input_tokens_used,b.input_tokens_reserved,nbi_u128(0))>nbi_u128(0)
   AND nbi_u128_remaining(b.input_token_limit_mag,b.input_tokens_used,b.input_tokens_reserved,nbi_u128(0))>=nbi_u128(b.input_token_reserve)))
 AND (b.output_token_limit_mag IS NULL OR (b.output_token_reserve IS NOT NULL
   AND nbi_u128_remaining(b.output_token_limit_mag,b.output_tokens_used,b.output_tokens_reserved,nbi_u128(0))>nbi_u128(0)
   AND nbi_u128_remaining(b.output_token_limit_mag,b.output_tokens_used,b.output_tokens_reserved,nbi_u128(0))>=nbi_u128(b.output_token_reserve)))
 AND NOT EXISTS (SELECT 1 FROM quota_remaining q WHERE q.retired_at IS NOT NULL OR q.remaining=nbi_u128(0)
 OR (q.metric IN ('input_tokens','output_tokens') AND b.input_token_reserve IS NULL)
 OR q.remaining<nbi_u128(CASE q.metric WHEN 'calls' THEN 1 WHEN 'tokens' THEN b.effective_reserve
 WHEN 'input_tokens' THEN b.input_token_reserve WHEN 'output_tokens' THEN b.output_token_reserve WHEN 'credits' THEN %[3]s ELSE -1 END))
)`, keyID, decisionNow, credits, tokens)
}

// EffectiveTokenReserveSQL supplies the same pair-or-scalar amount to existing
// cumulative total-limit predicates. Inputs are trusted repository expressions.
func EffectiveTokenReserveSQL(keyID, legacy string) string {
	return fmt.Sprintf(`(SELECT COALESCE(tk.input_token_reserve+tk.output_token_reserve,%s) FROM donation_keys tk WHERE tk.id=%s)`, legacy, keyID)
}
