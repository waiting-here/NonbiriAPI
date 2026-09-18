import { GameMoney } from '../GameMoney';
import { GamePayment } from '../GamePayment';
import type { DuelMode, DuelResult, Rates } from './types';
import { outcomeText, useDuelText } from './copy';

export function DuelRates({ rates }: { readonly rates: Rates }) {
  const t = useDuelText();
  return (
    <div className="duel-rates">
      <span>
        {t('平台', 'Platform')} {rates.platform / 100}%
      </span>
      <span>
        {t('低保池', 'Welfare')} {rates.welfare / 100}%
      </span>
      <span>
        {t('周四池', 'Thursday')} {rates.thursday / 100}%
      </span>
    </div>
  );
}
export function DuelTerms({ mode }: { readonly mode: DuelMode }) {
  const t = useDuelText();
  return (
    <div className="duel-terms">
      <p>
        {t('入场票价', 'Entry ticket')}{' '}
        <strong>
          <GameMoney value={mode.ticket} />
        </strong>
      </p>
      <DuelRates rates={mode.rates} />
      <p>
        {t(
          '优先使用游戏积分，不足部分使用通用积分。胜者按原币种返还本金，奖金发为通用积分；平局或系统取消原额退款。三项费用仅从输家投入分别计算。',
          'Game credits are used first, then general credits. The winner receives their principal in its original currencies and a general-credit prize. Draws and system cancellations refund both entrants. Each fee is calculated separately from the losing entry.',
        )}
      </p>
    </div>
  );
}
export function DuelFinance<V, P>({ result }: { readonly result: DuelResult<V, P> }) {
  const t = useDuelText();
  const reason =
    result.reason === 'server_restart'
      ? t('服务器重启', 'Server restarted')
      : result.reason === 'account_unavailable'
        ? t('账号不可用', 'Account unavailable')
        : result.reason === 'surrender'
          ? t('一方认输', 'A player surrendered')
          : result.reason === 'target'
            ? t('达到目标', 'Target reached')
            : result.reason === 'double-overload'
              ? t('双方过载', 'Both players overloaded')
              : t('对局结束', 'Game completed');
  return (
    <section className="duel-finance">
      <h2>{outcomeText(result.outcome, t)}</h2>
      <p>
        {reason} · {result.scores[result.you]} : {result.scores[1 - result.you]}
      </p>
      <dl>
        <div>
          <dt>{t('本人投入', 'Your entry')}</dt>
          <dd>
            <GamePayment payment={result.payment} />
          </dd>
        </div>
        <div>
          <dt>{t('本金返还／退款', 'Principal returned / refund')}</dt>
          <dd>
            <GamePayment payment={result.refund} />
          </dd>
        </div>
        <div>
          <dt>{t('奖金（通用积分）', 'Prize (general credits)')}</dt>
          <dd>
            <GameMoney value={result.prize} />
          </dd>
        </div>
        <div>
          <dt>{t('整局费用：平台／低保池／周四池', 'Game fees: platform / welfare / Thursday')}</dt>
          <dd>
            <GameMoney value={result.rake.platform} /> / <GameMoney value={result.rake.welfare} /> /{' '}
            <GameMoney value={result.rake.thursday} />
          </dd>
        </div>
      </dl>
    </section>
  );
}
