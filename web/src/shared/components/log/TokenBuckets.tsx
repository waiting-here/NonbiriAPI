import { useTranslation } from 'react-i18next';

// Four-bucket token display shared by both log screens. The four buckets are
// authoritative and mutually exclusive (uncached input / cache write / cache
// read / output). When the server marks usage_unknown, an explicit unknown
// badge is shown instead of zeros — a missing report must never read as "0".
// Values remain canonical decimal strings. They are never coerced through
// Number(), so counters above the JavaScript safe-integer range stay exact.

export interface TokenBucketValues {
  uncached_input_tokens: string;
  cache_write_input_tokens: string;
  cache_read_input_tokens: string;
  output_tokens: string;
  usage_unknown: boolean;
}

export function TokenBuckets({ row }: { row: TokenBucketValues }) {
  const { t } = useTranslation();
  if (row.usage_unknown) {
    return (
      <span className="status-badge is-inactive">{t('logs.usageUnknown')}</span>
    );
  }
  const entries = [
    { label: t('logs.bucketUncachedInputShort'), value: row.uncached_input_tokens },
    { label: t('logs.bucketCacheWriteShort'), value: row.cache_write_input_tokens },
    { label: t('logs.bucketCacheReadShort'), value: row.cache_read_input_tokens },
    { label: t('logs.bucketOutputShort'), value: row.output_tokens },
  ];
  return (
    <ul className="token-buckets">
      {entries.map((entry) => (
        <li key={entry.label}>
          <span className="token-bucket-label">{entry.label}</span>{' '}
          <span className="mono">{entry.value}</span>
        </li>
      ))}
    </ul>
  );
}
