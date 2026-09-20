import type {
  BlackjackCard,
  BlackjackFact,
  BlackjackHand,
  BlackjackState,
} from '@shared/games/blackjack';

export interface AudioFact {
  readonly key: string;
  readonly cue: string;
  readonly at: number;
}

interface CardEntry {
  readonly location: string;
  readonly card: BlackjackCard;
}

function atSeconds(value: number) {
  return Math.round(value * 1000);
}

function cardEntries(cards: NonNullable<BlackjackFact['cards']>): CardEntry[] {
  const entries: CardEntry[] = [];
  for (const seat of cards.seats)
    for (const [handIndex, hand] of seat.hands.entries())
      for (const [cardIndex, card] of hand.cards.entries())
        entries.push({
          location: `seat:${seat.number}:hand:${handIndex}:card:${cardIndex}`,
          card,
        });
  const visibleDealerCards = cards.hole_hidden ? cards.dealer.slice(0, 1) : cards.dealer;
  for (const [cardIndex, card] of visibleDealerCards.entries())
    entries.push({ location: `dealer:card:${cardIndex}`, card });
  return entries;
}

function cardID(entry: CardEntry) {
  return `${entry.location}:${entry.card.rank}:${entry.card.suit}`;
}

function ownHands(
  cards: NonNullable<BlackjackFact['cards']>,
  seat: number,
): readonly BlackjackHand[] {
  return cards.seats.find((item) => item.number === seat)?.hands ?? [];
}

function ownSeat(state: BlackjackState) {
  return state.your_seat ?? state.you?.seat ?? null;
}

function tableResultFacts(current: BlackjackState, previous: BlackjackState, facts: AudioFact[]) {
  const table = current.table;
  const oldTable = previous.table;
  if (!table || !oldTable || table.phase !== 'result' || oldTable.phase === 'result') return;
  const seat = ownSeat(current);
  if (seat === null) return;
  const settlement = table.fact.settlements.find((item) => item.seat === seat);
  if (!settlement) return;
  const currentHands = table.fact.cards ? ownHands(table.fact.cards, seat) : [];
  const previousHands = oldTable.fact.cards ? ownHands(oldTable.fact.cards, seat) : [];
  const natural =
    settlement.hands.some((hand) => hand.outcome === 'natural') ||
    currentHands.some((hand) => hand.natural);
  const hadNatural = previousHands.some((hand) => hand.natural);
  const at = atSeconds(table.terminal_at ?? current.server_now);
  const naturalAlreadyEmitted = facts.some(
    (fact) =>
      fact.cue === 'blackjack_natural' && fact.key.startsWith(`blackjack:${table.id}:1:natural:`),
  );
  if (natural && !hadNatural && !naturalAlreadyEmitted)
    facts.push({ key: `blackjack:${table.id}:1:natural:result`, cue: 'blackjack_natural', at });
  if (natural) return;

  const delta = settlement.hands.reduce(
    (sum, hand) => sum + BigInt(hand.net_milli) - BigInt(hand.stake_milli),
    0n,
  );
  const cue =
    delta === 0n || settlement.hands.every((hand) => hand.outcome === 'push')
      ? 'common_draw'
      : delta > 0n
        ? 'common_win'
        : 'common_loss';
  facts.push({ key: `blackjack:${table.id}:1:result`, cue, at });
}

function initialTableFacts(current: BlackjackState, facts: AudioFact[]) {
  const table = current.table;
  const cards = table?.fact.cards;
  if (!table || !cards) return;
  const at = atSeconds(current.server_now);
  for (const entry of cardEntries(cards))
    facts.push({
      key: `blackjack:${table.id}:1:deal:${entry.location}:${entry.card.rank}:${entry.card.suit}`,
      cue: 'blackjack_deal',
      at,
    });
  const seat = ownSeat(current);
  if (seat === null) return;
  for (const [index, hand] of ownHands(cards, seat).entries()) {
    if (hand.natural)
      facts.push({
        key: `blackjack:${table.id}:1:natural:${index}:${hand.revision}`,
        cue: 'blackjack_natural',
        at,
      });
    if (hand.total.value > 21)
      facts.push({
        key: `blackjack:${table.id}:1:bust:${index}:${hand.revision}`,
        cue: 'blackjack_bust',
        at,
      });
  }
}

function tableTransitionFacts(
  current: BlackjackState,
  previous: BlackjackState,
  facts: AudioFact[],
) {
  const table = current.table;
  const oldTable = previous.table;
  if (!table || !oldTable) return;
  if (table.id !== oldTable.id) {
    if (oldTable.phase === 'seating' && table.fact.cards) {
      facts.push({
        key: `blackjack:${table.id}:1:match`,
        cue: 'common_match',
        at: atSeconds(current.server_now),
      });
      initialTableFacts(current, facts);
    }
    return;
  }
  const cards = table.fact.cards;
  const oldCards = oldTable.fact.cards;
  if (cards && !oldCards) {
    initialTableFacts(current, facts);
    return;
  }
  if (cards) {
    const oldIDs = new Set(oldCards ? cardEntries(oldCards).map(cardID) : []);
    const flipped = oldCards?.hole_hidden === true && !cards.hole_hidden;
    if (flipped)
      facts.push({
        key: `blackjack:${table.id}:1:flip:${table.revision}`,
        cue: 'blackjack_flip',
        at: atSeconds(current.server_now),
      });
    for (const entry of cardEntries(cards)) {
      if (oldIDs.has(cardID(entry))) continue;
      if (flipped && entry.location === 'dealer:card:1') continue;
      facts.push({
        key: `blackjack:${table.id}:1:deal:${entry.location}:${entry.card.rank}:${entry.card.suit}`,
        cue: 'blackjack_deal',
        at: atSeconds(current.server_now),
      });
    }

    const seat = ownSeat(current);
    const oldSeat = ownSeat(previous);
    if (seat !== null && seat === oldSeat && oldCards) {
      const hands = ownHands(cards, seat);
      const oldHands = ownHands(oldCards, seat);
      for (const [index, hand] of hands.entries()) {
        const oldHand = oldHands[index];
        if (!oldHand) continue;
        if (hand.units > oldHand.units)
          facts.push({
            key: `blackjack:${table.id}:1:double:${index}:${hand.revision}`,
            cue: 'blackjack_double',
            at: atSeconds(current.server_now),
          });
        if (hand.split && !oldHand.split)
          facts.push({
            key: `blackjack:${table.id}:1:split:${index}:${hand.revision}`,
            cue: 'blackjack_split',
            at: atSeconds(current.server_now),
          });
        if (!oldHand.natural && hand.natural)
          facts.push({
            key: `blackjack:${table.id}:1:natural:${index}:${hand.revision}`,
            cue: 'blackjack_natural',
            at: atSeconds(current.server_now),
          });
        if (
          !oldHand.stood &&
          hand.stood &&
          hand.units === oldHand.units &&
          hand.cards.length === oldHand.cards.length &&
          hand.split === oldHand.split &&
          hands.length === oldHands.length &&
          !(oldHand.total.value <= 21 && hand.total.value > 21) &&
          !(!oldHand.natural && hand.natural)
        )
          facts.push({
            key: `blackjack:${table.id}:1:stand:${index}:${hand.revision}`,
            cue: 'common_confirm',
            at: atSeconds(current.server_now),
          });
        if (oldHand.total.value <= 21 && hand.total.value > 21)
          facts.push({
            key: `blackjack:${table.id}:1:bust:${index}:${hand.revision}`,
            cue: 'blackjack_bust',
            at: atSeconds(current.server_now),
          });
      }
      if (
        hands.length > oldHands.length &&
        !hands.some(
          (hand, index) => index < oldHands.length && hand.split && !oldHands[index].split,
        )
      ) {
        const hand = hands[hands.length - 1];
        facts.push({
          key: `blackjack:${table.id}:1:split:${hands.length - 1}:${hand?.revision ?? table.revision}`,
          cue: 'blackjack_split',
          at: atSeconds(current.server_now),
        });
      }
    }
  }
}

export function blackjackAudioFacts(home: BlackjackState, previous?: BlackjackState): AudioFact[] {
  if (!previous || !home.table || !previous.table) return [];
  if (home.table.id !== previous.table.id && previous.table.phase !== 'seating') return [];
  const facts: AudioFact[] = [];
  tableTransitionFacts(home, previous, facts);
  tableResultFacts(home, previous, facts);
  const emphasized = new Set(
    facts
      .filter((fact) =>
        ['blackjack_natural', 'blackjack_bust', 'blackjack_double', 'blackjack_split'].includes(
          fact.cue,
        ),
      )
      .map((fact) => fact.at),
  );
  const seen = new Set<string>();
  return facts.filter((fact) => {
    if (fact.cue === 'blackjack_deal' && emphasized.has(fact.at)) return false;
    const key = `${fact.at}:${fact.cue}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}
