import { modalityNameKeys } from '@shared/operations/modalityCopy';
import { useTranslation } from 'react-i18next';
import type { LogUsage } from './data';
import { Fold } from '../ui';

// Four-bucket token display shared by both log screens. The four buckets are
// authoritative and mutually exclusive (uncached input / cache write / cache
// read / output). When the server marks usage_unknown, an explicit unknown
// badge is shown instead of zeros — a missing report must never read as "0".
// Values remain canonical decimal strings. They are never coerced through
// Number(), so counters above the JavaScript safe-integer range stay exact.

export interface TokenBucketValues extends Pick<
  LogUsage,
  'search_units' | 'modalities' | 'pricing'
> {
  uncached_input_tokens: string;
  cache_write_input_tokens: string;
  cache_read_input_tokens: string;
  output_tokens: string;
  usage_unknown: boolean;
}

const usageKeys = {
  input: 'common.usageDetails.input',
  output: 'common.usageDetails.output',
  per_token: 'common.usageDetails.per_token',
  per_request: 'common.usageDetails.per_request',
  per_search_unit: 'common.usageDetails.per_search_unit',
  aggregate: 'common.usageDetails.aggregate',
  modality: 'common.usageDetails.modality',
  uncached_input: 'common.usageDetails.uncached_input',
  cache_write_input: 'common.usageDetails.cache_write_input',
  cache_read_input: 'common.usageDetails.cache_read_input',
} as const;
const reasonKeys = {
  missing_details: 'common.usageDetails.reason.missing_details',
  invalid_details: 'common.usageDetails.reason.invalid_details',
  incomplete_details: 'common.usageDetails.reason.incomplete_details',
  unknown_modality: 'common.usageDetails.reason.unknown_modality',
  cache_details_missing: 'common.usageDetails.reason.cache_details_missing',
  total_mismatch: 'common.usageDetails.reason.total_mismatch',
  reasoning_details_missing: 'common.usageDetails.reason.reasoning_details_missing',
  missing_price: 'common.usageDetails.reason.missing_price',
} as const;

export function TokenBuckets({ row }: { row: TokenBucketValues }) {
  const { t } = useTranslation();
  const entries = [
    { label: t('logs.bucketUncachedInputShort'), value: row.uncached_input_tokens },
    { label: t('logs.bucketCacheWriteShort'), value: row.cache_write_input_tokens },
    { label: t('logs.bucketCacheReadShort'), value: row.cache_read_input_tokens },
    { label: t('logs.bucketOutputShort'), value: row.output_tokens },
  ];
  return (
    <div className="ops-stack">
      {row.search_units !== undefined && (
        <p>
          {t('common.usageDetails.searchUnits')}: <span className="mono">{row.search_units}</span>
        </p>
      )}
      {row.usage_unknown ? (
        <span className="status-badge is-inactive">{t('logs.usageUnknown')}</span>
      ) : (
        <ul className="token-buckets">
          {entries.map((entry) => (
            <li key={entry.label}>
              <span className="token-bucket-label">{entry.label}</span>{' '}
              <span className="mono">{entry.value}</span>
            </li>
          ))}
        </ul>
      )}
      {row.pricing && (
        <p>
          {t('common.usageDetails.pricing')}: {t(usageKeys[row.pricing.mode])}
          {(['input', 'output'] as const).map(
            (side) =>
              row.pricing?.[(side + '_mode') as 'input_mode' | 'output_mode'] && (
                <span key={side}>
                  {' '}
                  · {t(usageKeys[side])}:{' '}
                  {t(usageKeys[row.pricing[(side + '_mode') as 'input_mode' | 'output_mode']!])}
                </span>
              ),
          )}
        </p>
      )}
      {(['input', 'output'] as const).map((side) => {
        const reason =
          row.pricing?.[(side + '_fallback') as 'input_fallback' | 'output_fallback'] ??
          row.modalities?.[(side + '_fallback') as 'input_fallback' | 'output_fallback'];
        return reason ? (
          <p className="nb-sub" key={side}>
            {t(usageKeys[side])}: {t(reasonKeys[reason])}
          </p>
        ) : null;
      })}
      {(row.modalities?.input || row.modalities?.output) && (
        <Fold title={t('common.usageDetails.modalities')}>
          <dl className="nb-kv">
            {(['uncached_input', 'cache_write_input', 'cache_read_input', 'output'] as const).map(
              (bucket) => {
                const values =
                  bucket === 'output' ? row.modalities?.output : row.modalities?.input?.[bucket];
                return (
                  values && (
                    <div key={bucket}>
                      <dt>{t(usageKeys[bucket])}</dt>
                      <dd>
                        {(['text', 'image', 'audio', 'video'] as const).map((m) => (
                          <p key={m}>
                            {t(modalityNameKeys[m])}: <span className="mono">{values[m]}</span>
                          </p>
                        ))}
                      </dd>
                    </div>
                  )
                );
              },
            )}
          </dl>
        </Fold>
      )}
    </div>
  );
}
