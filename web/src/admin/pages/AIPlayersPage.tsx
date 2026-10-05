import { Link } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { useAIText } from '@shared/aiPlayers';
import { AIPlayersAdmin } from '../features/games/AIPlayers';

export function AIPlayersPage() {
  const t = useAIText();
  return (
    <div className="page ops-page">
      <PageHeader
        title={t('竞标对决 · AI 玩家与策略', 'Bidding Duel · AI players and strategies')}
        actions={
          <Link className="nb-btn nb-btn--secondary" to="/games">
            {t('返回游戏管理', 'Back to game configuration')}
          </Link>
        }
      />
      <AIPlayersAdmin />
    </div>
  );
}
