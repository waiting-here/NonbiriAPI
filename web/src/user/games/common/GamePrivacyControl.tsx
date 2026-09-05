import { useQueryClient } from '@tanstack/react-query';
import { stationSessionWrite } from '@shared/charityManagement';
import { ErrorState } from '@shared/components/States';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useUserSession, userKeys } from '../../data';
import { patchGameProfile } from '../../features/core/api';
import { useGameCopy } from '../copy';

export function GamePrivacyControl() {
  const session = useUserSession(false);
  const client = useQueryClient();
  const { text } = useGameCopy();
  const save = useRetainedOperation(
    (isPublic: boolean, key) =>
      stationSessionWrite(client, 'steward', () =>
        patchGameProfile(isPublic, { idempotencyKey: key, actionId: key }),
      ),
    () =>
      Promise.all([
        client.invalidateQueries({ queryKey: userKeys.session }),
        client.invalidateQueries({ queryKey: userKeys.me }),
        client.invalidateQueries({ queryKey: ['user', 'games'] }),
      ]),
    ['user'],
  );
  if (!session.data?.user || session.error) return null;
  return (
    <div className="game-privacy-control">
      <label className="checkbox-label">
        <input
          type="checkbox"
          checked={!session.data.user.game_profile_public}
          disabled={save.isPending}
          onChange={(event) => save.mutate(!event.target.checked)}
        />
        <span>{text('common.anonymous')}</span>
      </label>
      <p className="table-note">{text('common.anonymousHelp')}</p>
      {save.error ? <ErrorState error={save.error} /> : null}
    </div>
  );
}
