import { useGameCopy } from '../copy';
import { formatCredits } from './strict';

export function GameMoney({ value }: { readonly value: string }) {
  const { text } = useGameCopy();
  return (
    <span className="game-money">
      {formatCredits(value)} <span className="game-money__unit">{text('common.credits')}</span>
    </span>
  );
}
