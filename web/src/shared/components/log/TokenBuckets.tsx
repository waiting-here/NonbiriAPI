import { useTranslation } from 'react-i18next';
import { CompactNumber } from '@shared/components/CompactNumber';
import { formatCompact, type FormattedNumber } from '@shared/utils/formatNumber';

// Four-bucket token display shared by both log screens. The four buckets are
// authoritative and mutually exclusive (uncached input / cache write / cache
// read / output). When the server marks usage_unknown, an explicit unknown
// badge is shown instead of zeros — a missing report must never read as "0".
// Values arrive as JSON numbers from the log rows; formatting goes through
// the shared BigInt-safe formatter, which degrades to '—' for anything that
// is not a safe integer. Economic string values never pass through here.

export interface TokenBucketValues {
  uncached_input_tokens: number;
  cache_write_input_tokens: number;
  cache_read_input_tokens: number;
  output_tokens: number;
  usage_unknown: boolean;
}

function bucket(value: number): FormattedNumber {
  return Number.isSafeInteger(value) && value >= 0
    ? formatCompact(value)
    : { display: '—', exact: '—', abbreviated: false };
}

export function TokenBuckets({ row }: { row: TokenBucketValues }) {
  const { t } = useTranslation();
  if (row.usage_unknown) {
    return (
      <span className="status-badge is-inactive">{t('logs.usageUnknown')}</span>
    );
  }
  const entries = [
    { label: t('logs.bucketUncachedInputShort'), value: bucket(row.uncached_input_tokens) },
    { label: t('logs.bucketCacheWriteShort'), value: bucket(row.cache_write_input_tokens) },
    { label: t('logs.bucketCacheReadShort'), value: bucket(row.cache_read_input_tokens) },
    { label: t('logs.bucketOutputShort'), value: bucket(row.output_tokens) },
  ];
  return (
    <ul className="token-buckets">
      {entries.map((entry) => (
        <li key={entry.label}>
          <span className="token-bucket-label">{entry.label}</span>{' '}
          <CompactNumber value={entry.value} />
        </li>
      ))}
    </ul>
  );
}
