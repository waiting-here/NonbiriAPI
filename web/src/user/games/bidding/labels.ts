export const handSuit = (seat: number, t: (zh: string, en: string) => string) => ({
  symbol: seat === 0 ? '♥' : '♠',
  name: seat === 0 ? t('红桃', 'Hearts') : t('黑桃', 'Spades'),
});
export const cardLabel = (rank: number) =>
  rank === 1 ? 'A' : rank === 11 ? 'J' : rank === 12 ? 'Q' : rank === 13 ? 'K' : String(rank);
