const reasonKeys: Record<string, string> = {
  window_limit: 'common.scanReason.windowLimit',
  candidate_limit: 'common.scanReason.candidateLimit',
  result_limit: 'common.scanReason.resultLimit',
  minute_limit: 'common.scanReason.minuteLimit',
  identity_or_ip_unavailable: 'common.scanReason.identityOrIP',
  prefix_retention: 'common.scanReason.prefixRetention',
  source_changed: 'common.scanReason.sourceChanged',
  permission_changed: 'common.scanReason.permissionChanged',
  scan_failed: 'common.scanReason.failed',
};
export function scanReasonKey(reason: string): string {
  return reasonKeys[reason] ?? 'common.scanReason.incomplete';
}
