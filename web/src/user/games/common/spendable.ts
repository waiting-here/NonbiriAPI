import { creditsFromMilli, creditsToMilli } from './strict';
import type { GamesSnapshot } from './types';

export function spendableGameMilli(wallets: Pick<GamesSnapshot, 'balance' | 'gameBalance'>) {
  const general = creditsToMilli(wallets.balance, true);
  const game = creditsToMilli(wallets.gameBalance, true);
  const generalAvailable = general > 0n ? general : 0n;
  const gameAvailable = game > 0n ? game : 0n;
  return {
    general: generalAvailable,
    game: gameAvailable,
    total: generalAvailable + gameAvailable,
  };
}

export function spendableGameCredits(wallets: Pick<GamesSnapshot, 'balance' | 'gameBalance'>) {
  const available = spendableGameMilli(wallets);
  return {
    general: creditsFromMilli(available.general),
    game: creditsFromMilli(available.game),
    total: creditsFromMilli(available.total),
  };
}
