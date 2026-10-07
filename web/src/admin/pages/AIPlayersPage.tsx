import { useState } from 'react';
import { GwentAIAdmin } from '../features/games/GwentAI';
import { Link } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { useAIText } from '@shared/aiPlayers';
import { AIPlayersAdmin } from '../features/games/AIPlayers';

export function AIPlayersPage() {
  const t = useAIText();
  const [game, setGame] = useState<'bidding' | 'gwent'>('bidding');
  return (
    <div className="page ops-page">
      <PageHeader
        title={t('AI 玩家与挑战', 'AI players and challenges')}
        actions={
          <Link className="nb-btn nb-btn--secondary" to="/games">
            {t('返回游戏管理', 'Back to game configuration')}
          </Link>
        }
      />
      <div className="nb-actions" role="group" aria-label={t('选择游戏', 'Select game')}>
        <button
          className="nb-btn nb-btn--secondary"
          type="button"
          aria-pressed={game === 'bidding'}
          onClick={() => setGame('bidding')}
        >
          {t('竞标对决', 'Bidding Duel')}
        </button>
        <button
          className="nb-btn nb-btn--secondary"
          type="button"
          aria-pressed={game === 'gwent'}
          onClick={() => setGame('gwent')}
        >
          {t('AI 昆特牌', 'AI Gwent')}
        </button>
      </div>
      {game === 'gwent' ? <GwentAIAdmin /> : <AIPlayersAdmin />}
    </div>
  );
}
