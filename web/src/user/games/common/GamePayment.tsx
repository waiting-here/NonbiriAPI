import type { GamePayment as Payment } from './types';
import { GameWallets } from './GameWallets';

export function GamePayment({ payment }: { readonly payment: Payment }) {
  return <GameWallets wallets={{ balance: payment.general, gameBalance: payment.game }} />;
}
