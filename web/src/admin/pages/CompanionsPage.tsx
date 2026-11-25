import { useState } from 'react';
import { useCompanionCopy } from '@shared/companionCopy';
import { Link } from 'react-router';
import { Tabs } from '@shared/components/ui/Tabs';
import { PageHeader, LoadingState, ErrorState } from '@shared/components/States';
import { Panel, PanelBody } from '@shared/components/ui';
import type { CompanionGame, CompanionPrograms } from '@shared/companion';
import { useCompanionRead } from '../../user/games/companion/api';
import { ProgramLibrary } from '../../user/games/companion/ProgramLibrary';
import '../../user/games/companion/companion.css';

function OfficialProgram({ game }: { game: CompanionGame }) {
  const state = useCompanionRead<CompanionPrograms>(game, '', { admin: true });
  if (!state.data)
    return state.error ? (
      <ErrorState error={state.error} onRetry={() => void state.refetch()} />
    ) : (
      <LoadingState />
    );
  return (
    <Panel>
      <PanelBody>
        <ProgramLibrary game={game} programs={state.data} refresh={state.refetch} admin />
      </PanelBody>
    </Panel>
  );
}
export function CompanionsPage() {
  const text = useCompanionCopy();
  const [game, setGame] = useState<CompanionGame>('bidding');
  return (
    <div className="page ops-page">
      <PageHeader title={text('adminTitle')} back={<Link to="/games">{text('backAdmin')}</Link>} />
      <Tabs
        label={text('selectGame')}
        value={game}
        onChange={setGame}
        tabs={(['bidding', 'likes', 'gwent'] as const).map((value) => ({
          value,
          label: text(value),
        }))}
      >
        <OfficialProgram key={game} game={game} />
      </Tabs>
    </div>
  );
}
