import { Toggle } from '@shared/components/ui/Toggle';
import { useEffect, useRef } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useLocation } from 'react-router';
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
  const location = useLocation();
  const control = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (location.hash !== '#game-privacy' || !control.current) return;
    control.current.scrollIntoView({ block: 'center' });
    control.current.focus({ preventScroll: true });
  }, [location.key, location.hash, session.data?.user.id]);
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
    <div className="game-privacy-control" id="game-privacy" ref={control} tabIndex={-1}>
      <Toggle
        label={text('common.anonymous')}
        description={text('common.anonymousHelp')}
        checked={!session.data.user.game_profile_public}
        disabled={save.isPending}
        onChange={(checked) => save.mutate(!checked)}
      />
      {save.error ? <ErrorState error={save.error} /> : null}
    </div>
  );
}

export function GamePrivacyLink() {
  const { text } = useGameCopy();
  return (
    <p className="game-privacy-control">
      {text('common.sharedPrivacy')}{' '}
      <Link to="/games#game-privacy">{text('common.anonymousSettings')}</Link>
    </p>
  );
}
