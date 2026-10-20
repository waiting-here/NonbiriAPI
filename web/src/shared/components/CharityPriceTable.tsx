import type { EffectiveDiscount, RecurringDiscount } from '@shared/operations/recurringDiscount';
import { formatCount } from '@shared/utils/formatNumber';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Amount, formatAmount } from '@shared/components/Amount';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import './CharityPriceTable.css';

export interface CharityPriceRow {
  label: string;
  compactLabel?: string;
  userMilli: string;
  currentUserMilli: string;
  rewardMilli?: string;
}

export interface CharityPriceTableProps {
  compact?: boolean;
  longContext?: { inputThreshold: string; rows: CharityPriceRow[] };
  contextPenalty?: { inputThreshold: string; fineMilli: string };
  mode: 'per_request' | 'per_token';
  rows: CharityPriceRow[];
  serverNow: number;
  effectiveDiscount: EffectiveDiscount;
  recurringDiscount: RecurringDiscount;
  discount?: { percent: number; enabled: boolean; startAt?: number; endAt?: number };
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

function timestampISO(value: number): string | undefined {
  const date = new Date(value * 1000);
  return Number.isFinite(date.getTime()) ? date.toISOString() : undefined;
}

/**
 * Shared, display-only charity price table. It never participates in routing
 * or accounting. Base and current prices are server projections; the
 * server clock keeps offer boundaries independent of the device clock.
 * Donor rewards, when a management view supplies them, are never discounted.
 */
export function CharityPriceTable({
  mode,
  rows,
  serverNow,
  discount,
  effectiveDiscount,
  recurringDiscount,
  compact = false,
  longContext,
  contextPenalty,
}: CharityPriceTableProps) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const [now, setNow] = useState(serverNow);
  useEffect(() => {
    const observedAt = window.performance.now();
    const update = () =>
      setNow(serverNow + Math.floor((window.performance.now() - observedAt) / 1000));
    update();
    if (effectiveDiscount.next_change_at === null) return undefined;
    const timer = window.setInterval(update, 1000);
    return () => window.clearInterval(timer);
  }, [effectiveDiscount.next_change_at, serverNow]);

  const active = effectiveDiscount.percent < 100;
  const nextChange = effectiveDiscount.next_change_at;
  const countdown = nextChange === null ? null : formatCountdown(nextChange - now);
  const showRewards = rows.length > 0 && rows.every((row) => row.rewardMilli !== undefined);
  const deadline =
    nextChange !== null ? (
      <time dateTime={timestampISO(nextChange)}>
        {t('common.recurringDiscount.nextChange', { time: formatDateTime(nextChange) })}
      </time>
    ) : null;
  const scheduleNote = recurringDiscount.enabled ? (
    <p className="nb-sub">
      {t('common.recurringDiscount.zoneSummary', { zone: recurringDiscount.time_zone })}
      {discount?.enabled ? <> · {t('common.recurringDiscount.paused')}</> : null}
    </p>
  ) : null;
  const penaltyNote = contextPenalty && (
    <p className="nb-sub charity-context-note">
      <strong>
        {t('common.operations.charity.context.penaltyNotice', {
          threshold: formatCount(contextPenalty.inputThreshold).display,
        })}
      </strong>
      <span>
        {t('common.operations.charity.context.fineNote', {
          amount: formatAmount(contextPenalty.fineMilli),
        })}
      </span>
    </p>
  );

  if (compact)
    return (
      <div className="charity-prices-compact">
        <dl>
          {rows.map((row) => (
            <div key={row.label}>
              <dt>{row.compactLabel ?? row.label}</dt>
              <dd>
                <ExactAmount value={row.currentUserMilli} label={t('user.charity.currentPrice')} />
                {row.currentUserMilli !== row.userMilli ? (
                  <s>
                    <ExactAmount value={row.userMilli} label={t('user.charity.originalPrice')} />
                  </s>
                ) : null}
              </dd>
            </div>
          ))}
        </dl>
        {longContext && (
          <p className="nb-sub">{t('common.operations.charity.context.longAvailable')}</p>
        )}
        {scheduleNote}
        {deadline ? <p className="nb-sub">{deadline}</p> : null}
        {penaltyNote}
      </div>
    );

  return (
    <div className="charity-price-wrap">
      {effectiveDiscount.source !== 'none' ? (
        <p className="charity-discount" role="status">
          <strong>
            {active
              ? effectiveDiscount.percent === 0
                ? t('user.charity.discountFree')
                : t('user.charity.discountPercent', { percent: 100 - effectiveDiscount.percent })
              : t('common.recurringDiscount.currentPercent', {
                  percent: effectiveDiscount.percent,
                })}
          </strong>
          {effectiveDiscount.source === 'fixed' &&
          effectiveDiscount.percent === 100 &&
          nextChange !== null ? (
            <span>
              {t('user.charity.discountUpcoming')} ·{' '}
              {t('user.charity.discountPercent', { percent: 100 - (discount?.percent ?? 100) })}
            </span>
          ) : null}
          {deadline}
          {countdown ? (
            <span>{t('common.recurringDiscount.nextRemaining', { time: countdown })}</span>
          ) : null}
        </p>
      ) : null}
      {scheduleNote}
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
          {[
            { rows, threshold: undefined },
            ...(longContext
              ? [{ rows: longContext.rows, threshold: longContext.inputThreshold }]
              : []),
          ].map((group, index) => (
            <tbody key={index}>
              {group.threshold && (
                <tr className="charity-context-heading">
                  <th scope="rowgroup" colSpan={showRewards ? 3 : 2}>
                    {t('common.operations.charity.context.longNotice', {
                      threshold: formatCount(group.threshold).display,
                    })}
                  </th>
                </tr>
              )}
              {group.rows.map((row) => {
                const current = row.currentUserMilli;
                return (
                  <tr key={row.label}>
                    <th scope="row">{row.label}</th>
                    <td>
                      {current !== row.userMilli ? (
                        <span className="charity-price-pair">
                          <s className="charity-original">
                            <ExactAmount
                              value={row.userMilli}
                              label={t('user.charity.originalPrice')}
                            />
                          </s>
                          <strong className="charity-current">
                            <ExactAmount value={current} label={t('user.charity.currentPrice')} />
                          </strong>
                        </span>
                      ) : (
                        <ExactAmount value={current} label={t('user.charity.currentPrice')} />
                      )}
                    </td>
                    {showRewards && row.rewardMilli !== undefined ? (
                      <td>
                        <ExactAmount
                          value={row.rewardMilli}
                          label={t('user.charity.donorReward')}
                        />
                      </td>
                    ) : null}
                  </tr>
                );
              })}
            </tbody>
          ))}
        </table>
      </div>
      {penaltyNote}
      {(longContext || contextPenalty) && (
        <p className="nb-sub">{t('common.operations.charity.context.usageHelp')}</p>
      )}
      <p className="muted charity-price-note">
        {t(mode === 'per_token' ? 'user.charity.tokenUnit' : 'user.charity.requestUnit')}{' '}
        {t('user.charity.billingHelp')}
      </p>
    </div>
  );
}
