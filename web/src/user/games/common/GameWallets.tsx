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
    <dl className="game-wallets">
      <div>
        <dt>{text('common.generalBalance')}</dt>
        <dd>
          <GameMoney value={wallets.balance} />
        </dd>
      </div>
      <div>
        <dt>{text('common.gameBalance')}</dt>
        <dd>
          <GameMoney value={wallets.gameBalance} />
        </dd>
      </div>
    </dl>
  );
}
