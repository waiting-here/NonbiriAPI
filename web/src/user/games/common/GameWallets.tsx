import type { GamesSnapshot } from './types';
import { GameMoney } from './GameMoney';
import { useGameCopy } from '../copy';

export function GameWallets({
  wallets,
}: {
  readonly wallets: Pick<GamesSnapshot, 'balance' | 'gameBalance'>;
}) {
  const { text } = useGameCopy();
  return (
    <div className="game-wallets">
      <span>
        {text('common.generalBalance')}{' '}
        <strong>
          <GameMoney value={wallets.balance} />
        </strong>
      </span>
      <span>
        {text('common.gameBalance')}{' '}
        <strong>
          <GameMoney value={wallets.gameBalance} />
        </strong>
      </span>
    </div>
  );
}
