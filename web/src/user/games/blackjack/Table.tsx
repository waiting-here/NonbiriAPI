import type { DuelText } from '../common/duel/copy';
import type { CSSProperties, ReactNode } from 'react';
import type {
  BlackjackCard,
  BlackjackFact,
  BlackjackHand,
  BlackjackTable,
} from '@shared/games/blackjack';
import { useDuelText } from '../common/duel/copy';
import { creditsFromMilli, formatCredits } from '../common/strict';
import { PublicGameIdentity } from '../common/PublicGameIdentity';
const ranks = ['', 'A', '2', '3', '4', '5', '6', '7', '8', '9', '10', 'J', 'Q', 'K'];
const suits = ['♠', '♥', '♣', '♦'];
export function PlayingCard({
  card,
  identity,
  small = false,
}: {
  readonly card: BlackjackCard | null;
  readonly identity: string;
  readonly small?: boolean;
}) {
  const text = useDuelText();
  const name = card
    ? [
        text('blackjack.spades'),
        text('blackjack.hearts'),
        text('bidding.clubs'),
        text('bidding.diamonds'),
      ][card.suit]
    : text('blackjack.dealerHoleCard');
  return (
    <span
      className={`bid-reward bj-card ${small ? 'bj-card--small' : ''} ${card ? (card.suit % 2 ? 'bid-reward--0' : '') : 'bj-card--back'}`}
      data-card-key={identity}
      aria-label={card ? `${ranks[card.rank]} ${name}` : name}
      role="img"
    >
      {card ? (
        <>
          <span className="bid-reward__corner" aria-hidden="true">
            {ranks[card.rank]}
            <small>{suits[card.suit]}</small>
          </span>
          <span className="bid-reward__suit" aria-hidden="true">
            {suits[card.suit]}
          </span>
          <strong aria-hidden="true">{ranks[card.rank]}</strong>
        </>
      ) : (
        <span aria-hidden="true">✦</span>
      )}
    </span>
  );
}
export function HandCards({
  hand,
  identity,
  small = false,
}: {
  readonly hand: BlackjackHand;
  readonly identity: string;
  readonly small?: boolean;
}) {
  const text = useDuelText();
  const label = hand.natural
    ? text('blackjack.blackjack2')
    : hand.total.value > 21
      ? text('blackjack.bust')
      : hand.stood
        ? text('blackjack.stood')
        : text('blackjack.deciding');
  return (
    <div
      className={`bj-hand ${hand.total.value > 21 ? 'is-bust' : ''} ${hand.natural ? 'is-natural' : ''}`}
    >
      <div
        className="bj-cards"
        style={{ '--card-count': Math.max(2, hand.cards.length) } as CSSProperties}
      >
        {hand.cards.map((card, i) => (
          <PlayingCard
            key={`${identity}:${i}`}
            card={card}
            identity={`${identity}:${i}:${card.rank}:${card.suit}`}
            small={small}
          />
        ))}
      </div>
      <p className="bj-hand-score">
        <strong data-score-key={`${identity}:${hand.total.value}:${hand.stood}`}>
          {hand.total.value}
          {hand.total.soft && <small>{text('blackjack.soft')}</small>}
        </strong>
        <span>{label}</span>
        {hand.units === 2 && <b>×2</b>}
        {hand.split && <span>{text('blackjack.split')}</span>}
      </p>
    </div>
  );
}
export function BlackjackBoard({
  table,
  ownSeat,
  controls,
}: {
  readonly table: BlackjackTable;
  readonly ownSeat: number | null;
  readonly controls?: ReactNode;
}) {
  const text = useDuelText();
  const cards = table.fact.cards;
  const mine = cards?.seats.find((seat) => seat.number === ownSeat);
  const myTerms = table.fact.seats.find((seat) => seat.seat === ownSeat);
  const hasOwnArea = ownSeat !== null && table.phase !== 'seating';
  const identityFor = (seat: number) => {
    const value = table.realtime_identities?.find((identity) => identity.seat === seat);
    return value
      ? { kind: 'public' as const, displayName: value.display_name, avatarURL: value.avatar_url }
      : { kind: 'anonymous' as const };
  };
  return (
    <section
      className={`bj-board bj-board--viewer-${ownSeat === null ? 'spectator' : 'participant'} bj-board--phase-${table.phase}`}
      aria-label={text('blackjack.blackjackTable')}
    >
      <div className="bj-table-watermark" aria-hidden="true">
        BLACKJACK <span>3 : 2</span>
      </div>
      <section className="bj-dealer" aria-label={text('blackjack.dealer')}>
        <h2>
          {text('blackjack.dealer')} <small>{text('blackjack.standsOnSoft17')}</small>
        </h2>
        <div className="bj-cards">
          {cards ? (
            <>
              {cards.dealer.map((card, i) => (
                <PlayingCard
                  key={`${table.id}:dealer:${i}`}
                  identity={`${table.id}:dealer:${i}:${card.rank}:${card.suit}`}
                  card={card}
                />
              ))}
              {cards.hole_hidden && <PlayingCard identity={`${table.id}:hole`} card={null} />}
            </>
          ) : (
            <>
              <PlayingCard identity="waiting-dealer-0" card={null} />
              <PlayingCard identity="waiting-dealer-1" card={null} />
            </>
          )}
        </div>
        <p>
          {cards?.hole_hidden ? (
            text('blackjack.holeCardOpensWhenEveryoneFinishes')
          ) : cards ? (
            <strong data-score-key={`dealer:${cards.dealer_total.value}`}>
              {cards.dealer_total.value} {text('blackjack.points')}
            </strong>
          ) : (
            text('blackjack.waitingToDeal')
          )}
        </p>
      </section>
      {hasOwnArea && (
        <section className="bj-mine" aria-label={text('blackjack.yourHands')}>
          <h2>
            {text('blackjack.yourHands')} <small>#{ownSeat + 1}</small>
          </h2>
          <PublicGameIdentity
            identity={identityFor(ownSeat)}
            anonymousLabel={text('blackjack.anonymousCardPlayer')}
            isMe
            meLabel={text('bidding.you')}
          />
          {myTerms?.emote && (
            <span className="bj-emote" role="status">
              {emoteText(myTerms.emote, text)}
            </span>
          )}
          <div className="bj-hands">
            {mine?.hands.map((hand, i) => (
              <section key={i}>
                <h3>
                  {mine.hands.length > 1
                    ? `${text('blackjack.hand')} ${i + 1}`
                    : text('blackjack.yourHand')}
                </h3>
                <HandCards hand={hand} identity={`${table.id}:seat:${mine.number}:hand:${i}`} />
              </section>
            ))}
          </div>
        </section>
      )}
      {controls}
      <div
        className={`bj-seats ${hasOwnArea ? 'bj-seats--eight' : 'bj-seats--nine'}`}
        aria-label={text('blackjack.otherSeats')}
      >
        {Array.from({ length: 9 }, (_, number) => {
          if (number === ownSeat && hasOwnArea) return null;
          const terms = table.fact.seats.find((s) => s.seat === number),
            seat = cards?.seats.find((s) => s.number === number);
          return (
            <section
              key={number}
              className={`bj-seat ${terms ? 'is-occupied' : ''} ${number === ownSeat ? 'is-own' : ''}`}
              aria-label={`${text('blackjack.seat')} ${number + 1}`}
            >
              <header>
                <strong>
                  #{number + 1}
                  {number === ownSeat && ` · ${text('bidding.you')}`}
                </strong>
                <span>{terms ? formatCredits(terms.stake) : text('blackjack.open')}</span>
              </header>
              {terms && (
                <PublicGameIdentity
                  identity={identityFor(number)}
                  anonymousLabel={text('blackjack.anonymousCardPlayer')}
                  isMe={number === ownSeat}
                  meLabel={text('bidding.you')}
                  size={24}
                />
              )}
              {seat ? (
                seat.hands.map((hand, i) => (
                  <HandCards
                    key={i}
                    hand={hand}
                    identity={`${table.id}:seat:${number}:hand:${i}`}
                    small
                  />
                ))
              ) : terms ? (
                <p>{text('blackjack.seated')}</p>
              ) : (
                <span className="bj-empty-seat" aria-hidden="true">
                  ♧
                </span>
              )}
              {terms?.stopped && <small>{text('blackjack.autoStand')}</small>}
              {terms?.emote && (
                <span className="bj-emote" role="status">
                  {emoteText(terms.emote, text)}
                </span>
              )}
            </section>
          );
        })}
      </div>
    </section>
  );
}
export function emoteText(emote: string, text: DuelText) {
  return (
    (
      {
        hello: text('blackjack.hello'),
        nice: text('blackjack.nice'),
        wow: text('blackjack.wow'),
        good_luck: text('blackjack.goodLuck'),
        thanks: text('blackjack.thanks'),
        gg: text('blackjack.gG'),
      } as Record<string, string>
    )[emote] ?? ''
  );
}
export function outcomeText(outcome: string, text: DuelText) {
  return (
    (
      {
        win: text('blackjack.win'),
        loss: text('blackjack.loss'),
        push: text('blackjack.push'),
        natural: text('blackjack.blackjack2'),
      } as Record<string, string>
    )[outcome] ?? ''
  );
}
const milli = (value: string) => formatCredits(creditsFromMilli(BigInt(value)));
export function BlackjackSettlement({
  fact,
  seat,
}: {
  readonly fact: BlackjackFact;
  readonly seat: number | null;
}) {
  const text = useDuelText();
  const settlements =
    seat === null ? fact.settlements : fact.settlements.filter((s) => s.seat === seat);
  return (
    <section
      className="bj-settlement"
      aria-label={text('blackjack.settlementDetails')}
      data-settlement
    >
      <h2>{seat === null ? text('blackjack.tableResults') : text('blackjack.yourResults')}</h2>
      {settlements.map((s) => (
        <div key={s.seat} className="bj-settled-seat">
          {seat === null && (
            <h3>
              {text('blackjack.seat')} #{s.seat + 1}
            </h3>
          )}
          {s.hands.map((h, i) => (
            <article key={i} className={`bj-payout bj-payout--${h.outcome}`}>
              <header>
                <span>
                  {text('blackjack.hand')} {i + 1}
                </span>
                <strong>{outcomeText(h.outcome, text)}</strong>
              </header>
              <dl>
                <div>
                  <dt>{text('blackjack.stake')}</dt>
                  <dd>{milli(h.stake_milli)}</dd>
                </div>
                <div>
                  <dt>{text('blackjack.grossReturn')}</dt>
                  <dd>{milli(h.gross_milli)}</dd>
                </div>
                <div>
                  <dt>{text('blackjack.platformFee')}</dt>
                  <dd>−{milli(h.platform_milli)}</dd>
                </div>
                <div>
                  <dt>{text('blackjack.welfarePool')}</dt>
                  <dd>−{milli(h.welfare_milli)}</dd>
                </div>
                <div>
                  <dt>{text('blackjack.thursdayPool')}</dt>
                  <dd>−{milli(h.thursday_milli)}</dd>
                </div>
                <div className="bj-net">
                  <dt>{text('blackjack.generalCreditsPaid')}</dt>
                  <dd data-payout={h.net_milli}>{milli(h.net_milli)}</dd>
                </div>
              </dl>
            </article>
          ))}
        </div>
      ))}
      <p>{text('blackjack.allNormalReturnsAreGeneralCreditsIncluding')}</p>
    </section>
  );
}
