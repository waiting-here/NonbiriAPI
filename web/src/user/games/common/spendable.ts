import { creditsFromMilli, creditsToMilli } from './strict';
import type { GamesSnapshot } from './types';

export function spendableGameCredits(wallets: Pick<GamesSnapshot, 'balance' | 'gameBalance'>) {
  const general = creditsToMilli(wallets.balance, true);
  const game = creditsToMilli(wallets.gameBalance, true);
  const generalAvailable = general > 0n ? general : 0n;
  const gameAvailable = game > 0n ? game : 0n;
  return {
    general: creditsFromMilli(generalAvailable),
    game: creditsFromMilli(gameAvailable),
    total: creditsFromMilli(generalAvailable + gameAvailable),
  };
}
