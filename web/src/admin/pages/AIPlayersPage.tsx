import { Tabs } from '@shared/components/ui/Tabs';
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
        back={<Link to="/games">{t('返回游戏配置', 'Back to game settings')}</Link>}
      />
      <Tabs
        label={t('选择游戏', 'Select game')}
        value={game}
        onChange={setGame}
        tabs={[
          { value: 'bidding', label: t('竞标对决', 'Bidding Duel') },
          { value: 'gwent', label: t('AI 昆特牌', 'AI Gwent') },
        ]}
      />
      {game === 'gwent' ? <GwentAIAdmin /> : <AIPlayersAdmin />}
    </div>
  );
}
