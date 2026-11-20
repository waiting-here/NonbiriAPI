import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Amount } from '@shared/components/Amount';
import { Link } from 'react-router';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { getQuote, type Profile, type Quote } from '../api';
import { useWhaleText } from '../copy';

export function ExchangePane({
  profile,
  busy,
  onExchange,
  onRefresh,
  onCollect,
}: {
  profile: Profile;
  busy: boolean;
  onExchange: (quote: Quote, onError: (reason: string) => void) => Promise<boolean>;
  onRefresh: () => void;
  onCollect: () => void;
}) {
  const t = useWhaleText();
  const [lots, setLots] = useState('1');
  const [quotedBalance, setQuotedBalance] = useState<string>();
  const [quote, setQuote] = useState<Quote | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const values = profile.settings.values;
  const wallet = profile.snapshot.wallet;
  const enabled = profile.settings.exchange_enabled;
  const pendingCoins = wallet.pendingSocialPearls ?? 0;
  const pendingShards = wallet.pendingConchShards ?? 0;
  const wageProgress = Math.max(0, Math.min(1, wallet.wageProgress ?? 0));
  const availability = useQuery({
    queryKey: [
      'user',
      'games',
      'whale-haven',
      'exchange-limit',
      profile.social.door,
      profile.settings_revision,
      profile.snapshot.revision,
      wallet.pearls,
      wallet.generalMilli,
      wallet.dailyPearlExchanged,
      pendingCoins,
      wallet.siteDayKey,
    ],
    queryFn: ({ signal }) => getQuote('0', signal),
    enabled,
    retry: false,
    gcTime: 0,
    staleTime: 0,
  });
  const limit = availability.data;
  const valid = /^[1-9]\d{0,15}$/.test(lots) && !!limit && BigInt(lots) <= BigInt(limit.max_lots);
  const unit = t('通用积分', 'general credits');
  const gross = BigInt(values.lot_gross_milli);
  const cuts = [values.platform_cut_bp, values.welfare_cut_bp, values.thursday_cut_bp].map(
    (bp) => (gross * BigInt(bp)) / 10000n,
  );
  const net = gross - cuts[0] - cuts[1] - cuts[2];
  const noLots = limit?.max_lots === '0';
  const dailyBlocked = limit && BigInt(limit.daily_source_remaining) < BigInt(values.lot_pearls);
  const coinsBlocked =
    limit &&
    (wallet.pearls + pendingCoins < values.lot_pearls ||
      (noLots && !dailyBlocked && !limit.wallet_cap_blocked));
  const stale =
    quote &&
    (String(wallet.generalMilli) !== quotedBalance ||
      !enabled ||
      !limit ||
      BigInt(profile.snapshot.revision) > BigInt(quote.profile_revision) ||
      BigInt(profile.settings_revision) > BigInt(quote.settings_revision) ||
      BigInt(limit.profile_revision) > BigInt(quote.profile_revision) ||
      BigInt(limit.settings_revision) > BigInt(quote.settings_revision) ||
      BigInt(quote.quantity) > BigInt(limit.max_lots));
  async function preview() {
    if (!valid || busy || loading) return;
    setLoading(true);
    setError('');
    try {
      const result = await getQuote(lots);
      setQuotedBalance(String(wallet.generalMilli));
      setQuote(result);
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : t(
              '未能取得报价，请核对余额与额度后重试。',
              'Could not get a quote. Check the updated balance and limit, then retry.',
            ),
      );
      onRefresh();
      void availability.refetch();
    } finally {
      setLoading(false);
    }
  }
  return (
    <section className="voyage-card">
      <h3 className="kit-section-title">
        {t('小鲸币兑换通用积分', 'Exchange whale coins for general credits')}
      </h3>
      <dl className="wh-metrics">
        <div>
          <dt>{t('持有小鲸币', 'Whale coin balance')}</dt>
          <dd>{wallet.pearls}</dd>
        </div>
        <div>
          <dt>{t('海螺碎片', 'Conch shards')}</dt>
          <dd>{wallet.conchShards ?? 0}</dd>
        </div>
        <div>
          <dt>{t('今日已兑', 'Exchanged today')}</dt>
          <dd>
            {wallet.dailyPearlExchanged} / {values.daily_exchange_pearls}
          </dd>
        </div>
      </dl>
      {(pendingCoins > 0 || pendingShards > 0 || wageProgress > 0) && (
        <div>
          <p>
            {t('待结算', 'Pending settlement')} · {t('小鲸币', 'Whale coins')} {pendingCoins} ·{' '}
            {t('海螺碎片', 'Conch shards')} {pendingShards}
            {wageProgress > 0 && (
              <>
                {' '}
                · {t('下一枚工资进度', 'Progress toward next wage coin')}{' '}
                {Math.floor(wageProgress * 100)}%
              </>
            )}
          </p>
          {(pendingCoins > 0 || pendingShards > 0) && (
            <button type="button" disabled={busy} className="nb-btn" onClick={onCollect}>
              {t('收取并入账', 'Collect earnings')}
            </button>
          )}
        </div>
      )}
      <p>
        {t('每手小鲸币', 'Whale coins per lot')} {values.lot_pearls} ·{' '}
        {t('每手毛额', 'Gross per lot')}{' '}
        <Amount value={String(values.lot_gross_milli)} unit={unit} />
      </p>
      <details>
        <summary>{t('每手分配与净额', 'Cuts and net per lot')}</summary>
        <dl className="wh-quote">
          {[
            [t('平台分配', 'Platform'), cuts[0]],
            [t('低保池分配', 'Welfare pool'), cuts[1]],
            [t('星期四池分配', 'Thursday pool'), cuts[2]],
            [t('每手实际到账', 'Net per lot'), net],
          ].map(([label, amount]) => (
            <div key={String(label)}>
              <dt>{label}</dt>
              <dd>
                <Amount value={String(amount)} unit={unit} />
              </dd>
            </div>
          ))}
        </dl>
      </details>
      {!enabled ? (
        <p role="status">{t('站点已暂停兑换。', 'Exchange is paused.')}</p>
      ) : (
        <>
          <div aria-live="polite" aria-atomic="true">
            {limit ? (
              <>
                <p>
                  {t('最多可兑', 'Available now')} <strong>{limit.max_lots}</strong>{' '}
                  {t('手', 'lots')} · {t('今日剩余额度', 'Remaining today')}{' '}
                  {limit.daily_source_remaining} {t('枚小鲸币', 'whale coins')}
                </p>
                {noLots && (
                  <ul>
                    {limit.wallet_cap_blocked && (
                      <li>
                        {t(
                          '兑一手后的通用积分余额会超出上限',
                          'One lot would take your general balance above the cap of',
                        )}{' '}
                        <Amount value={limit.general_wallet_cap_milli} unit={unit} />
                      </li>
                    )}
                    {dailyBlocked && (
                      <li>
                        {t(
                          '今日剩余额度不足一整手。',
                          'The daily allowance has less than one lot left.',
                        )}
                      </li>
                    )}
                    {coinsBlocked && (
                      <li>
                        {t(
                          '持有的小鲸币不足一整手。',
                          'You need more whale coins for one full lot.',
                        )}
                      </li>
                    )}
                  </ul>
                )}
              </>
            ) : availability.isPending ? (
              <p>{t('正在读取可兑手数…', 'Checking available lots…')}</p>
            ) : null}
          </div>
          {availability.isError && (
            <p role="alert">
              {t('暂时无法确认可兑手数。', 'Available lots could not be confirmed.')}{' '}
              <button className="nb-btn" onClick={() => void availability.refetch()}>
                {t('重试', 'Retry')}
              </button>
            </p>
          )}
          <form
            className="wh-buttons"
            onSubmit={(e) => {
              e.preventDefault();
              void preview();
            }}
          >
            <label className="wh-field">
              {t('兑换手数', 'Lots to exchange')}{' '}
              <input
                className="kit-input"
                inputMode="numeric"
                pattern="[1-9][0-9]*"
                maxLength={16}
                required
                value={lots}
                onChange={(e) => {
                  setLots(e.target.value);
                  setError('');
                }}
              />
            </label>
            <button
              type="submit"
              className="nb-btn nb-btn--primary"
              disabled={busy || loading || !valid || availability.isError}
            >
              {loading ? t('读取报价中…', 'Getting quote…') : t('查看报价', 'Review quote')}
            </button>
          </form>
          {limit && !noLots && !valid && (
            <p>
              {t(
                '请输入可兑范围内的正整数手数。',
                'Enter a whole number of lots within the available limit.',
              )}
            </p>
          )}
        </>
      )}
      {error && <p role="alert">{error}</p>}
      <Link to="/credits">{t('查看积分明细', 'View credit history')}</Link>
      <ConfirmDialog
        open={!!quote}
        title={t('确认兑换', 'Confirm exchange')}
        description={
          quote
            ? t(
                `消耗 ${quote.source_pearls} 枚小鲸币，兑换 ${quote.quantity} 手。`,
                `Spend ${quote.source_pearls} whale coins to exchange ${quote.quantity} ${quote.quantity === '1' ? 'lot' : 'lots'}.`,
              )
            : ''
        }
        busy={busy}
        confirmDisabled={!!stale || availability.isError}
        confirmLabel={t('确认兑换', 'Confirm exchange')}
        onCancel={() => setQuote(null)}
        onConfirm={async () => {
          if (!quote || stale) return;
          await onExchange(quote, setError);
          setQuote(null);
          onRefresh();
          void availability.refetch();
        }}
      >
        {quote && (
          <>
            <dl className="wh-quote">
              {[
                [t('毛额', 'Gross'), quote.gross_general_milli],
                [t('平台分配', 'Platform'), quote.platform_milli],
                [t('低保池分配', 'Welfare pool'), quote.welfare_milli],
                [t('星期四池分配', 'Thursday pool'), quote.thursday_milli],
                [t('实际到账', 'Net credit'), quote.net_general_milli],
              ].map(([label, value]) => (
                <div key={label}>
                  <dt>{label}</dt>
                  <dd>
                    <Amount value={value} unit={unit} />
                  </dd>
                </div>
              ))}
            </dl>
            <p>
              {t('到账后余额上限', 'Balance cap after credit')}{' '}
              <Amount value={quote.general_wallet_cap_milli} unit={unit} />
            </p>
            {stale && (
              <p role="alert">
                {t(
                  '余额、额度或规则已更新，请取消并重新报价。',
                  'The balance, limit or rules changed. Cancel to request a new quote.',
                )}
              </p>
            )}
          </>
        )}
      </ConfirmDialog>
    </section>
  );
}
