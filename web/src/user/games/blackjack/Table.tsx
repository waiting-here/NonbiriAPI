import type { CSSProperties, ReactNode } from 'react';
import type {
  BlackjackCard,
  BlackjackFact,
  BlackjackHand,
  BlackjackTable,
} from '@shared/games/blackjack';
import { useDuelText } from '../common/duel/copy';
import { creditsFromMilli, formatCredits } from '../common/strict';

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
  const t = useDuelText();
  const name = card
    ? [t('黑桃', 'Spades'), t('红心', 'Hearts'), t('梅花', 'Clubs'), t('方块', 'Diamonds')][
        card.suit
      ]
    : t('庄家暗牌', 'Dealer hole card');
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
  const t = useDuelText();
  const label = hand.natural
    ? t('自然二十一点', 'Blackjack')
    : hand.total.value > 21
      ? t('爆牌', 'Bust')
      : hand.stood
        ? t('已停牌', 'Stood')
        : t('决策中', 'Deciding');
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
          {hand.total.soft && <small>{t('软', 'soft')}</small>}
        </strong>
        <span>{label}</span>
        {hand.units === 2 && <b>×2</b>}
        {hand.split && <span>{t('分牌', 'Split')}</span>}
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
  const t = useDuelText();
  const cards = table.fact.cards;
  const mine = cards?.seats.find((seat) => seat.number === ownSeat);
  const myTerms = table.fact.seats.find((seat) => seat.seat === ownSeat);
  return (
    <section className="bj-board" aria-label={t('二十一点牌桌', 'Blackjack table')}>
      <div className="bj-table-watermark" aria-hidden="true">
        BLACKJACK <span>3 : 2</span>
      </div>
      <section className="bj-dealer" aria-label={t('庄家', 'Dealer')}>
        <h2>
          {t('庄家', 'Dealer')} <small>{t('软 17 停牌', 'Stands on soft 17')}</small>
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
            t('全桌结束决策后翻开底牌', 'Hole card opens when everyone finishes')
          ) : cards ? (
            <strong data-score-key={`dealer:${cards.dealer_total.value}`}>
              {cards.dealer_total.value} {t('点', 'points')}
            </strong>
          ) : (
            t('等待发牌', 'Waiting to deal')
          )}
        </p>
      </section>
      {mine && (
        <section className="bj-mine" aria-label={t('你的手牌', 'Your hands')}>
          <h2>
            {t('你的手牌', 'Your hands')} <small>#{mine.number + 1}</small>
          </h2>
          {myTerms?.emote && (
            <span className="bj-emote" role="status">
              {emoteText(myTerms.emote, t)}
            </span>
          )}
          <div className="bj-hands">
            {mine.hands.map((hand, i) => (
              <section key={i}>
                <h3>
                  {mine.hands.length > 1 ? `${t('手牌', 'Hand')} ${i + 1}` : t('本手', 'Your hand')}
                </h3>
                <HandCards hand={hand} identity={`${table.id}:seat:${mine.number}:hand:${i}`} />
              </section>
            ))}
          </div>
        </section>
      )}
      {controls}
      <div className="bj-seats" aria-label={t('同桌玩家', 'Other seats')}>
        {Array.from({ length: 9 }, (_, number) => {
          if (number === ownSeat && mine) return null;
          const terms = table.fact.seats.find((s) => s.seat === number),
            seat = cards?.seats.find((s) => s.number === number);
          return (
            <section
              key={number}
              className={`bj-seat ${terms ? 'is-occupied' : ''} ${number === ownSeat ? 'is-own' : ''}`}
              aria-label={`${t('座位', 'Seat')} ${number + 1}`}
            >
              <header>
                <strong>
                  #{number + 1}
                  {number === ownSeat && ` · ${t('你', 'You')}`}
                </strong>
                <span>{terms ? formatCredits(terms.stake) : t('空位', 'Open')}</span>
              </header>
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
                <p>{t('已落座', 'Seated')}</p>
              ) : (
                <span className="bj-empty-seat" aria-hidden="true">
                  ♧
                </span>
              )}
              {terms?.stopped && <small>{t('自动停牌', 'Auto stand')}</small>}
              {terms?.emote && (
                <span className="bj-emote" role="status">
                  {emoteText(terms.emote, t)}
                </span>
              )}
            </section>
          );
        })}
      </div>
    </section>
  );
}
export function emoteText(emote: string, t: (zh: string, en: string) => string) {
  return (
    (
      {
        hello: t('👋 你好', '👋 Hello'),
        nice: t('👏 好牌', '👏 Nice'),
        wow: t('😮 哇', '😮 Wow'),
        good_luck: t('🍀 好运', '🍀 Good luck'),
        thanks: t('🙏 谢谢', '🙏 Thanks'),
        gg: t('🤝 好局', '🤝 GG'),
      } as Record<string, string>
    )[emote] ?? ''
  );
}
export function outcomeText(outcome: string, t: (zh: string, en: string) => string) {
  return (
    (
      {
        win: t('获胜', 'Win'),
        loss: t('失败', 'Loss'),
        push: t('平局', 'Push'),
        natural: t('自然二十一点', 'Blackjack'),
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
  const t = useDuelText();
  const settlements =
    seat === null ? fact.settlements : fact.settlements.filter((s) => s.seat === seat);
  return (
    <section
      className="bj-settlement"
      aria-label={t('结算明细', 'Settlement details')}
      data-settlement
    >
      <h2>{seat === null ? t('本桌结算', 'Table results') : t('你的结算', 'Your results')}</h2>
      {settlements.map((s) => (
        <div key={s.seat} className="bj-settled-seat">
          {seat === null && (
            <h3>
              {t('座位', 'Seat')} #{s.seat + 1}
            </h3>
          )}
          {s.hands.map((h, i) => (
            <article key={i} className={`bj-payout bj-payout--${h.outcome}`}>
              <header>
                <span>
                  {t('手牌', 'Hand')} {i + 1}
                </span>
                <strong>{outcomeText(h.outcome, t)}</strong>
              </header>
              <dl>
                <div>
                  <dt>{t('投入', 'Stake')}</dt>
                  <dd>{milli(h.stake_milli)}</dd>
                </div>
                <div>
                  <dt>{t('应返总额', 'Gross return')}</dt>
                  <dd>{milli(h.gross_milli)}</dd>
                </div>
                <div>
                  <dt>{t('平台费用', 'Platform fee')}</dt>
                  <dd>−{milli(h.platform_milli)}</dd>
                </div>
                <div>
                  <dt>{t('低保池', 'Welfare pool')}</dt>
                  <dd>−{milli(h.welfare_milli)}</dd>
                </div>
                <div>
                  <dt>{t('周四池', 'Thursday pool')}</dt>
                  <dd>−{milli(h.thursday_milli)}</dd>
                </div>
                <div className="bj-net">
                  <dt>{t('通用积分到账', 'General credits paid')}</dt>
                  <dd data-payout={h.net_milli}>{milli(h.net_milli)}</dd>
                </div>
              </dl>
            </article>
          ))}
        </div>
      ))}
      <p>
        {t(
          '正常返还全部发为通用积分，已包含本金及逐手费用。',
          'All normal returns are general credits, including principal after per-hand fees.',
        )}
      </p>
    </section>
  );
}
