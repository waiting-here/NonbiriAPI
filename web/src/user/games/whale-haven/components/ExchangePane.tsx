import { useState } from 'react';
import { Amount } from '@shared/components/Amount';
import { Link } from 'react-router';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { getQuote, type Profile, type Quote } from '../api';

export function ExchangePane({
  profile,
  busy,
  onExchange,
}: {
  profile: Profile;
  busy: boolean;
  onExchange: (quote: Quote) => Promise<boolean>;
}) {
  const [lots, setLots] = useState('1');
  const [quote, setQuote] = useState<Quote | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  async function preview() {
    setLoading(true);
    setError('');
    try {
      setQuote(await getQuote(lots));
    } catch (err) {
      setError(err instanceof Error ? err.message : '无法获取报价，请重试。');
    } finally {
      setLoading(false);
    }
  }
  const values = profile.settings.values;
  const stale =
    quote &&
    (BigInt(profile.snapshot.revision) > BigInt(quote.profile_revision) ||
      BigInt(profile.settings_revision) > BigInt(quote.settings_revision));
  const tooMany = quote && BigInt(quote.quantity) > BigInt(quote.max_lots);
  return (
    <section className="voyage-card">
      <h3 className="kit-section-title">小鲸币兑换通用积分</h3>
      <p>
        现有 {profile.snapshot.wallet.pearls} 小鲸币 · 已兑换{' '}
        {profile.snapshot.wallet.dailyPearlExchanged} / {values.daily_exchange_pearls} 枚
      </p>
      <p>
        每手 {values.lot_pearls} 枚小鲸币，毛额{' '}
        <Amount value={String(values.lot_gross_milli)} unit="通用积分" />
        。实际到账及分配以报价为准。
      </p>
      {!profile.settings.exchange_enabled ? (
        <p>站点已暂停兑换。</p>
      ) : (
        <form
          className="wh-buttons"
          onSubmit={(e) => {
            e.preventDefault();
            void preview();
          }}
        >
          <label>
            兑换手数{' '}
            <input
              className="kit-input"
              inputMode="numeric"
              pattern="[1-9][0-9]*"
              required
              value={lots}
              onChange={(e) => setLots(e.target.value)}
            />
          </label>
          <button type="submit" disabled={busy || loading} className="nb-btn nb-btn--primary">
            {loading ? '读取报价中…' : '查看报价'}
          </button>
        </form>
      )}
      {error && <p role="alert">{error}</p>}
      <Link to="/credits">查看积分明细</Link>
      <ConfirmDialog
        open={!!quote}
        title="确认兑换"
        description={
          quote ? '消耗 ' + quote.source_pearls + ' 枚小鲸币，兑换 ' + quote.quantity + ' 手。' : ''
        }
        busy={busy}
        confirmDisabled={!!(stale || tooMany || quote?.wallet_cap_blocked)}
        confirmLabel="确认兑换"
        onCancel={() => setQuote(null)}
        onConfirm={async () => {
          if (quote) {
            await onExchange(quote);
            setQuote(null);
          }
        }}
      >
        {quote && (
          <>
            <dl className="wh-quote">
              {(
                [
                  ['毛额', quote.gross_general_milli],
                  ['平台分配', quote.platform_milli],
                  ['低保池分配', quote.welfare_milli],
                  ['星期四池分配', quote.thursday_milli],
                  ['实际到账', quote.net_general_milli],
                ] as const
              ).map(([label, value]) => (
                <div key={label}>
                  <dt>{label}</dt>
                  <dd>
                    <Amount value={value} unit="通用积分" />
                  </dd>
                </div>
              ))}
            </dl>
            <p>
              当前最多可兑 {quote.max_lots} 手，今日剩余额度 {quote.daily_source_remaining} 枚。
            </p>
            {quote.wallet_cap_blocked ? (
              <p role="alert">通用积分已达水位，暂时无法兑换。</p>
            ) : stale ? (
              <p role="alert">存档或配置已更新。请取消并重新获取报价。</p>
            ) : tooMany ? (
              <p role="alert">可兑手数不足，请取消并修改数量。</p>
            ) : null}
          </>
        )}
      </ConfirmDialog>
    </section>
  );
}
