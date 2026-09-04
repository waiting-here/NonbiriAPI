import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Amount, formatAmount } from '@shared/components/Amount';
import { formatDateTime } from '@shared/utils/datetime';

export interface CharityPriceRow {
  label: string;
  userMilli: string;
  discountedUserMilli: string;
  rewardMilli?: string;
}

export interface CharityPriceTableProps {
  mode: 'per_request' | 'per_token';
  rows: CharityPriceRow[];
  serverNow: number;
  discount?: { percent: number; enabled: boolean; startAt?: number; endAt?: number };
}

function discountPhase(
  discount: CharityPriceTableProps['discount'],
  now: number,
): 'none' | 'scheduled' | 'active' {
  if (!discount?.enabled || discount.percent === 100) return 'none';
  if (discount.endAt !== undefined && now >= discount.endAt) return 'none';
  if (discount.startAt !== undefined && now < discount.startAt) return 'scheduled';
  return 'active';
}

function formatCountdown(seconds: number): string {
  const safe = Math.max(0, Math.floor(seconds));
  const days = Math.floor(safe / 86400);
  const hours = Math.floor((safe % 86400) / 3600);
  const minutes = Math.floor((safe % 3600) / 60);
  const remaining = safe % 60;
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m ${remaining}s`;
}

function ExactAmount({ value, label }: { value: string; label: string }) {
  const formatted = formatAmount(value);
  return (
    <Amount
      className="charity-amount"
      value={value}
      title={formatted}
      aria-label={`${label}: ${formatted}`}
    />
  );
}

/**
 * Shared, display-only charity price table. It never participates in routing
 * or accounting. Base and discounted prices are server projections; the
 * server clock keeps offer boundaries independent of the device clock.
 * Donor rewards, when a management view supplies them, are never discounted.
 */
export function CharityPriceTable({ mode, rows, serverNow, discount }: CharityPriceTableProps) {
  const { t } = useTranslation();
  const [now, setNow] = useState(serverNow);
  useEffect(() => {
    const observedAt = window.performance.now();
    const update = () =>
      setNow(serverNow + Math.floor((window.performance.now() - observedAt) / 1000));
    update();
    if (discount?.endAt === undefined && discount?.startAt === undefined) return undefined;
    const timer = window.setInterval(update, 1000);
    return () => window.clearInterval(timer);
  }, [discount?.endAt, discount?.startAt, serverNow]);

  const phase = discountPhase(discount, now);
  const active = phase === 'active';
  const countdown = useMemo(() => {
    if (!discount || !active) return null;
    const target = discount.endAt;
    if (target === undefined) return null;
    return formatCountdown(target - now);
  }, [active, discount, now]);
  const showRewards = rows.length > 0 && rows.every((row) => row.rewardMilli !== undefined);

  return (
    <div className="charity-price-wrap">
      {active ? (
        <p className="charity-discount" role="status">
          <strong>
            {discount?.percent === 0
              ? t('user.charity.discountFree')
              : t('user.charity.discountPercent', { percent: 100 - (discount?.percent ?? 100) })}
          </strong>
          {countdown ? <span>{t('user.charity.discountRemaining', { time: countdown })}</span> : null}
        </p>
      ) : phase === 'scheduled' && discount?.startAt !== undefined ? (
        <p className="charity-discount" role="status">
          <strong>{t('user.charity.discountUpcoming')}</strong>
          <span>
            {discount.percent === 0
              ? t('user.charity.discountFree')
              : t('user.charity.discountPercent', { percent: 100 - discount.percent })}
            {' · '}
            {t('user.charity.discountStartsAt', { time: formatDateTime(discount.startAt) })}
          </span>
        </p>
      ) : null}
      <div className="table-scroll">
        <table
          className={`charity-price-table${showRewards ? '' : ' charity-price-table--compact'}`}
        >
          <caption className="sr-only">{t('user.charity.priceCaption')}</caption>
          <thead>
            <tr>
              <th scope="col">{t('user.charity.priceType')}</th>
              <th scope="col">{t('user.charity.userPrice')}</th>
              {showRewards ? <th scope="col">{t('user.charity.donorReward')}</th> : null}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const current = active ? row.discountedUserMilli : undefined;
              return (
                <tr key={row.label}>
                  <th scope="row">{row.label}</th>
                  <td>
                    {active && current !== undefined && current !== row.userMilli ? (
                      <>
                        <s className="charity-original"><ExactAmount value={row.userMilli} label={t('user.charity.originalPrice')} /></s>{' '}
                        <strong><ExactAmount value={current ?? row.userMilli} label={t('user.charity.currentPrice')} /></strong>
                      </>
                    ) : (
                      <ExactAmount value={row.userMilli} label={t('user.charity.userPrice')} />
                    )}
                  </td>
                  {showRewards && row.rewardMilli !== undefined ? (
                    <td>
                      <ExactAmount value={row.rewardMilli} label={t('user.charity.donorReward')} />
                    </td>
                  ) : null}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <p className="muted charity-price-note">{t(mode === 'per_token' ? 'user.charity.tokenUnit' : 'user.charity.requestUnit')}</p>
    </div>
  );
}
