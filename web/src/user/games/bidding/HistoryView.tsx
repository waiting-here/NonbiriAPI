import type { DuelRound, Seat } from '../common/duel/types';
import { useDuelText } from '../common/duel/copy';
import { cardLabel, handSuit } from './labels';
import type { BiddingRound, BiddingView } from './normalize';

export function BiddingRoundView({
  round,
  you,
}: {
  readonly round: DuelRound<BiddingView, BiddingRound, never>;
  readonly you: Seat;
}) {
  const t = useDuelText();
  const fact = round.facts;
  return (
    <div className="bid-round-facts">
      <p>
        {t('你／对手出牌', 'Your bid / opponent’s bid')}:{' '}
        <strong>
          {handSuit(you, t).name} {cardLabel(fact.bids[you])} / {handSuit(1 - you, t).name}{' '}
          {cardLabel(fact.bids[1 - you])}
        </strong>
      </p>
      <p>
        {t('本轮奖励', 'Fresh reward')}: {fact.fresh} · {t('上轮累计', 'Carried in')}:{' '}
        {fact.carryBefore}
      </p>
      {fact.joker !== null && (
        <p>
          {fact.joker === you
            ? t('你使用了 Joker', 'You used a joker')
            : t('对手使用了 Joker', 'Opponent used a joker')}
        </p>
      )}
      <p>
        {fact.awardedTo !== null
          ? `${fact.awardedTo === you ? t('你赢得', 'You won') : t('对手赢得', 'Opponent won')} ${fact.awarded} ${t('分', 'points')}`
          : fact.discarded > 0
            ? `${t('最后一轮平手，奖池丢弃', 'Final tie, pool discarded')}: ${fact.discarded}`
            : `${t('平手，累计至下一轮', 'Tie, carried forward')}: ${fact.carryAfter}`}
      </p>
      <p>
        {t('结算后比分', 'Scores after settlement')}: {fact.scores[you]} : {fact.scores[1 - you]}
      </p>
    </div>
  );
}
export function PlayedHistory({ view, you }: { readonly view: BiddingView; readonly you: Seat }) {
  const t = useDuelText();
  if (!view.played[0].length) return null;
  return (
    <section className="bid-played">
      <h2>{t('已揭示出牌', 'Revealed bids')}</h2>
      <div
        className="bid-table-scroll"
        tabIndex={0}
        role="region"
        aria-label={t('出牌记录表，可横向滚动', 'Bid history, scroll horizontally')}
      >
        <table>
          <thead>
            <tr>
              <th>{t('轮次', 'Round')}</th>
              <th>{t('你', 'You')}</th>
              <th>{t('对手', 'Opponent')}</th>
              <th>{t('奖励去向', 'Reward status')}</th>
            </tr>
          </thead>
          <tbody>
            {view.played[0].map((_, index) => {
              const rewards = view.rewards.filter((card) => card.round === index + 1);
              const reward = rewards[0];
              return (
                <tr key={index}>
                  <td>{index + 1}</td>
                  <td>
                    {handSuit(you, t).name} {cardLabel(view.played[you][index])}
                  </td>
                  <td>
                    {handSuit(1 - you, t).name} {cardLabel(view.played[1 - you][index])}
                  </td>
                  <td>
                    {reward?.status === 'pool'
                      ? t('奖池累计', 'In carried pool')
                      : reward?.status === 'discarded'
                        ? t('丢弃', 'Discarded')
                        : reward?.owner === you
                          ? t('归你所有', 'Awarded to you')
                          : t('归对手所有', 'Awarded to opponent')}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
