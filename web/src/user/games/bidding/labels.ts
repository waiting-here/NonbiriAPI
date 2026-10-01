import type { DuelText } from '../common/duel/copy';
export const handSuit = (seat: number, text: DuelText) => ({
  symbol: seat === 0 ? '♥' : '♠',
  name: seat === 0 ? text('blackjack.hearts') : text('blackjack.spades'),
});
export const cardLabel = (rank: number) =>
  rank === 1 ? 'A' : rank === 11 ? 'J' : rank === 12 ? 'Q' : rank === 13 ? 'K' : String(rank);
