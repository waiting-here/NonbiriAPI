import { useState } from 'react';
import { useCompanionCopy } from '@shared/companionCopy';
import { Link } from 'react-router';
import { Tabs } from '@shared/components/ui/Tabs';
import { PageHeader, LoadingState, ErrorState } from '@shared/components/States';
import { Panel, PanelBody, Toggle } from '@shared/components/ui';
import {
  companionUploadsAvailable,
  uploadsEnabled,
  gameUploadsEnabled,
  type CompanionGame,
  type CompanionPrograms,
} from '@shared/companion';
import { useCompanionRead, useCompanionWrite } from '../../user/games/companion/api';
import { ProgramLibrary } from '../../user/games/companion/ProgramLibrary';
import '../../user/games/companion/companion.css';

function OfficialProgram({ game }: { game: CompanionGame }) {
  const state = useCompanionRead<CompanionPrograms>(game, '', { admin: true });
  const text = useCompanionCopy();
  const setting = useCompanionWrite<CompanionPrograms>(game, state.refetch, true);
  if (!state.data)
    return state.error ? (
      <ErrorState error={state.error} onRetry={() => void state.refetch()} />
    ) : (
      <LoadingState />
    );
  const uploadAllowed = companionUploadsAvailable(game, state.data.upload_available);
  return (
    <Panel>
      <PanelBody>
        <div className="nb-stack">
          <Toggle
            label={text('open')}
            checked={state.data.enabled}
            disabled={setting.isPending || setting.outcome === 'unknown'}
            onChange={(enabled) => setting.mutate({ path: '/settings', body: { enabled } })}
          />
          {setting.error && (
            <ErrorState
              error={setting.error}
              onRetry={setting.variables ? () => setting.mutate(setting.variables!) : undefined}
            />
          )}
          <Toggle
            label={text('globalUploads')}
            checked={uploadsEnabled && state.data.upload_global_enabled === true}
            disabled
            onChange={() => undefined}
          />
          <Toggle
            label={text('gameUploads')}
            checked={gameUploadsEnabled[game] && state.data.upload_game_enabled === true}
            disabled
            onChange={() => undefined}
          />
          {!uploadAllowed && <p id="companion-upload-notice">{text('uploadNotice')}</p>}
          <fieldset
            className="companion-program-settings"
            disabled={!uploadAllowed}
            aria-describedby={!uploadAllowed ? 'companion-upload-notice' : undefined}
          >
            <legend>{text('officialProgram')}</legend>
            <ProgramLibrary game={game} programs={state.data} refresh={state.refetch} admin />
          </fieldset>
        </div>
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
