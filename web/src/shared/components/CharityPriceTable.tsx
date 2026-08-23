import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatCreditsFromMilli } from '@shared/utils/formatNumber';

export interface CharityPriceRow {
  label: string;
  userMilli: string;
  rewardMilli: string;
  currentUserMilli?: string;
}

export interface CharityPriceTableProps {
  mode: 'per_request' | 'per_token';
  rows: CharityPriceRow[];
  discount?: { percent: number; enabled: boolean; startAt?: number; endAt?: number };
}

function discountedValue(value: string, percent: number): string | undefined {
  try {
    const original = BigInt(value);
    if (original < 0n || !Number.isInteger(percent) || percent < 0 || percent > 100) return undefined;
    if (percent === 0) return '0';
    const product = original * BigInt(percent);
    return ((product + 99n) / 100n).toString();
  } catch {
    return undefined;
  }
}

function isDiscountActive(discount: CharityPriceTableProps['discount'], now: number): boolean {
  if (!discount?.enabled || discount.percent === 100) return false;
  if (discount.startAt !== undefined && now < discount.startAt) return false;
  if (discount.endAt !== undefined && now >= discount.endAt) return false;
  return true;
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
  const formatted = formatCreditsFromMilli(value);
  return (
    <span className="charity-amount" title={formatted.exact} aria-label={`${label}: ${formatted.exact}`}>
      {formatted.display}
    </span>
  );
}

/**
 * Shared, display-only charity price table. It never participates in routing
 * or accounting. The API may provide active prices; the fallback is only a
 * visual projection using exact BigInt arithmetic and the server's discount
 * percentage. Donor rewards are intentionally never discounted.
 */
export function CharityPriceTable({ mode, rows, discount }: CharityPriceTableProps) {
  const { t } = useTranslation();
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  useEffect(() => {
    if (!discount?.endAt && !discount?.startAt) return undefined;
    const timer = window.setInterval(() => setNow(Math.floor(Date.now() / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, [discount?.endAt, discount?.startAt]);

  const active = isDiscountActive(discount, now);
  const countdown = useMemo(() => {
    if (!discount || !active) return null;
    const target = discount.endAt;
    if (target === undefined) return null;
    return formatCountdown(target - now);
  }, [active, discount, now]);

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
      ) : null}
      <div className="table-scroll">
        <table className="charity-price-table">
          <caption className="sr-only">{t('user.charity.priceCaption')}</caption>
          <thead>
            <tr>
              <th scope="col">{t('user.charity.priceType')}</th>
              <th scope="col">{t('user.charity.userPrice')}</th>
              <th scope="col">{t('user.charity.donorReward')}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const derived = active ? discountedValue(row.userMilli, discount?.percent ?? 100) : undefined;
              const current = active ? row.currentUserMilli ?? derived : undefined;
              const user = formatCreditsFromMilli(row.userMilli);
              const currentFormatted = current ? formatCreditsFromMilli(current) : null;
              return (
                <tr key={row.label}>
                  <th scope="row">{row.label}</th>
                  <td>
                    {active && currentFormatted && current !== row.userMilli ? (
                      <>
                        <s className="charity-original"><ExactAmount value={row.userMilli} label={t('user.charity.originalPrice')} /></s>{' '}
                        <strong><ExactAmount value={current ?? row.userMilli} label={t('user.charity.currentPrice')} /></strong>
                      </>
                    ) : (
                      <ExactAmount value={row.userMilli} label={t('user.charity.userPrice')} />
                    )}
                    {user.abbreviated ? <span className="sr-only"> ({user.exact})</span> : null}
                  </td>
                  <td><ExactAmount value={row.rewardMilli} label={t('user.charity.donorReward')} /></td>
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
