import { useAIText } from '@shared/aiPlayers';
import { GameMoney } from '../GameMoney';
import { GamePayment } from '../GamePayment';
import type { DuelMode, DuelResult, Rates } from './types';
import { outcomeText, useDuelText } from './copy';
export function DuelRates({ rates }: { readonly rates: Rates }) {
  const text = useDuelText();
  return (
    <div className="duel-rates">
      <span>
        {text('blackjack.platform')} {rates.platform / 100}%
      </span>
      <span>
        {text('common.welfare')} {rates.welfare / 100}%
      </span>
      <span>
        {text('common.thursday')} {rates.thursday / 100}%
      </span>
    </div>
  );
}
export function DuelTerms({ mode }: { readonly mode: DuelMode }) {
  const text = useDuelText();
  return (
    <div className="duel-terms">
      <p>
        {text('common.entryTicket')}{' '}
        <strong>
          <GameMoney value={mode.ticket} />
        </strong>
      </p>
      <DuelRates rates={mode.rates} />
      <p>{text('common.gameCreditsAreUsedFirstThenGeneral')}</p>
    </div>
  );
}
export function DuelFinance<V, P>({ result }: { readonly result: DuelResult<V, P> }) {
  const text = useDuelText();
  const aiText = useAIText();
  const reason =
    result.reason === 'server_restart'
      ? text('common.serverRestarted')
      : result.reason === 'account_unavailable'
        ? text('common.accountUnavailable')
        : result.reason === 'surrender'
          ? text('common.aPlayerSurrendered')
          : result.reason === 'target'
            ? text('common.targetReached')
            : result.reason === 'double-overload'
              ? text('common.bothPlayersOverloaded')
              : text('common.gameCompleted');
  return (
    <section className="duel-finance">
      <h2>{outcomeText(result.outcome, text)}</h2>
      <p>
        {reason} · {result.scores[result.you]} : {result.scores[1 - result.you]}
      </p>
      {result.ai && (
        <p>
          AI · {result.ai.terms.bot_name}
          {result.ai.first_clear ? ` · ${aiText('首次通关', 'First clear')}` : ''}
        </p>
      )}
      <dl>
        <div>
          <dt>{text('common.yourEntry')}</dt>
          <dd>
            <GamePayment payment={result.payment} />
          </dd>
        </div>
        <div>
          <dt>{text('common.principalReturnedRefund')}</dt>
          <dd>
            <GamePayment payment={result.refund} />
          </dd>
        </div>
        <div>
          <dt>
            {result.ai
              ? aiText('首通奖励 · 游戏积分', 'First clear · game credits')
              : text('common.prizeGeneralCredits')}
          </dt>
          <dd>
            <GameMoney value={result.ai?.reward ?? result.prize} />
          </dd>
        </div>
        {!result.ai && (
          <div>
            <dt>{text('common.gameFeesPlatformWelfareThursday')}</dt>
            <dd>
              <GameMoney value={result.rake.platform} /> / <GameMoney value={result.rake.welfare} />{' '}
              / <GameMoney value={result.rake.thursday} />
            </dd>
          </div>
        )}
      </dl>
    </section>
  );
}
