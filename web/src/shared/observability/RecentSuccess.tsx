import { useTranslation } from 'react-i18next';
import type { RecentSuccess as Success } from './api';

export function RecentSuccess({ value }: { value: Success }) {
  const { i18n } = useTranslation();
  const zh = i18n.resolvedLanguage?.startsWith('zh');
  const rate = value.rate == null ? '—' : `${(value.rate * 100).toFixed(1)}%`;
  return (
    <div>
      <span>
        {zh ? '近 24 小时请求成功率' : 'Request success over 24 hours'}: {rate}
      </span>
      <small>
        {' '}
        · {zh ? '样本' : 'Samples'} {value.sample_count} · {zh ? '取消' : 'Cancelled'}{' '}
        {value.cancelled}
      </small>
      {value.insufficient_sample && <small> · {zh ? '样本不足' : 'Insufficient sample'}</small>}
      {value.capture_started_at > value.window_start && (
        <small>
          {' '}
          · {zh ? '统计尚未覆盖完整时段' : 'Coverage does not yet span the full window'}
        </small>
      )}
    </div>
  );
}
