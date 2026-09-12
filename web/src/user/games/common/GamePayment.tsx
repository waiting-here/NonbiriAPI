import type { GamePayment as Payment } from './types';
import { GameMoney } from './GameMoney';
import { useGameCopy } from '../copy';

export function GamePayment({ payment }: { readonly payment: Payment }) {
  const { text } = useGameCopy();
  return (
    <span className="game-wallets">
      <span>{text('common.generalBalance')} <GameMoney value={payment.general} /></span>
      <span>{text('common.gameBalance')} <GameMoney value={payment.game} /></span>
    </span>
  );
}
