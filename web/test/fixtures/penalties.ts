export const penaltyID = `abc_${'A'.repeat(22)}`;
export const penaltyRequestID = `req_${'A'.repeat(22)}`;
export const penalty = {
  id: penaltyID,
  kind: 'ban',
  reason_code: 'charity_rpm',
  started_at: 1800000000,
  ends_at: 1800000060,
  ended_at: 1800000060,
  state: 'ended',
  result: 'expired',
};
export const penaltyAction = {
  id: '1',
  action: 'trigger',
  occurred_at: 1800000000,
  request_id: penaltyRequestID,
  request_log_available: false,
  operation_id: null,
  actor_user_id: null,
  previous_ends_at: null,
  ends_at: 1800000060,
  reason_code: 'charity_rpm',
  evidence_count: 21,
};
export const penaltyRules = {
  version: 1,
  rpm_ban_threshold: 21,
  rpm_ban_window_seconds: 100,
  rpm_ban_duration_seconds: 60,
  charity_min_chars: 20,
  charity_violation_deduct_milli: 0,
  charity_violation_ban_seconds: 0,
  charity_violation_window_seconds: 100,
  charity_violation_ban_threshold: 0,
  charity_violation_window_ban_seconds: 0,
  charity_suspend_window_seconds: 100,
  charity_suspend_threshold: 0,
  charity_suspend_duration_seconds: 0,
};
export function penaltyPage<T>(data: T[], size = 20, total = data.length, page = '1') {
  return {
    data,
    pagination: {
      page,
      page_size: size,
      total_items: String(total),
      total_pages: String(Math.max(1, Math.ceil(total / size))),
    },
  };
}
export function evidencePage(page = '1', size = 20) {
  const offset = (Number(page) - 1) * size;
  const members = Array.from({ length: Math.min(size, Math.max(0, 21 - offset)) }, (_, i) => ({
    occurred_at: 1799999980 + offset + i,
    request_id: `req_${String(offset + i).padStart(21, '0')}A`,
    violation_kind: 'rpm',
    content_chars: null,
    request_log_available: false,
  }));
  return {
    rules: penaltyRules,
    statistics: {
      rule: 'rpm_window',
      window_start: 1799999900,
      window_end: 1800000000,
      threshold: 21,
      count: 21,
    },
    members: penaltyPage(members, size, 21, page),
  };
}
