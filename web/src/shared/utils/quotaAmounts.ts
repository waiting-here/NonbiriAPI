import { quotaMetricValue, type RecurringLimitMetric } from '@shared/operations/recurringLimits';
import { formatCount } from './formatNumber';

export function quotaInput(value: string, metric: RecurringLimitMetric) {
  if (metric === 'credits' && /^(0|[1-9][0-9]*)\.[0-9]{1,3}$/.test(value))
    return value.replace(/0+$/, '').replace(/\.$/, '');
  return value;
}

export function validQuotaInput(value: string, metric: RecurringLimitMetric) {
  try {
    quotaMetricValue(quotaInput(value, metric), metric, metric);
    return true;
  } catch {
    return false;
  }
}

export function displayQuotaAmount(
  value: string | null | undefined,
  metric: RecurringLimitMetric,
): string {
  if (value == null) return '—';
  if (metric !== 'credits') return formatCount(value).display;
  const [whole, fraction] = value.split('.');
  return formatCount(whole ?? '').display + (fraction ? '.' + fraction : '');
}
