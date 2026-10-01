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
  const text = useDuelText();
  const fact = round.facts;
  return (
    <div className="bid-round-facts">
      <p>
        {text('bidding.yourBidOpponentSBid')}:{' '}
        <strong>
          {handSuit(you, text).name} {cardLabel(fact.bids[you])} / {handSuit(1 - you, text).name}{' '}
          {cardLabel(fact.bids[1 - you])}
        </strong>
      </p>
      <p>
        {text('bidding.freshReward')}: {fact.fresh} · {text('bidding.carriedIn')}:{' '}
        {fact.carryBefore}
      </p>
      {fact.joker !== null && (
        <p>
          {fact.joker === you ? text('bidding.youUsedAJoker') : text('bidding.opponentUsedAJoker')}
        </p>
      )}
      <p>
        {fact.awardedTo !== null
          ? `${fact.awardedTo === you ? text('bidding.youWon') : text('bidding.opponentWon')} ${fact.awarded} ${text('bidding.points')}`
          : fact.discarded > 0
            ? `${text('bidding.finalTiePoolDiscarded')}: ${fact.discarded}`
            : `${text('bidding.tieCarriedForward')}: ${fact.carryAfter}`}
      </p>
      <p>
        {text('bidding.scoresAfterSettlement')}: {fact.scores[you]} : {fact.scores[1 - you]}
      </p>
    </div>
  );
}
export function PlayedHistory({ view, you }: { readonly view: BiddingView; readonly you: Seat }) {
  const text = useDuelText();
  if (!view.played[0].length) return null;
  return (
    <section className="bid-played">
      <h2>{text('bidding.revealedBids')}</h2>
      <div
        className="bid-table-scroll"
        tabIndex={0}
        role="region"
        aria-label={text('bidding.bidHistoryScrollHorizontally')}
      >
        <table>
          <thead>
            <tr>
              <th>{text('bidding.round2')}</th>
              <th>{text('bidding.you')}</th>
              <th>{text('bidding.opponent')}</th>
              <th>{text('bidding.rewardStatus')}</th>
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
                    {handSuit(you, text).name} {cardLabel(view.played[you][index])}
                  </td>
                  <td>
                    {handSuit(1 - you, text).name} {cardLabel(view.played[1 - you][index])}
                  </td>
                  <td>
                    {reward?.status === 'pool'
                      ? text('bidding.inCarriedPool')
                      : reward?.status === 'discarded'
                        ? text('bidding.discarded2')
                        : reward?.owner === you
                          ? text('bidding.awardedToYou')
                          : text('bidding.awardedToOpponent')}
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
