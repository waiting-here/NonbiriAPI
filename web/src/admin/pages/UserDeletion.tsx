import { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ErrorState } from '@shared/components/States';
import { elevateAdmin } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { ApiError } from '@shared/query/http';
import type { AdminUser } from '@shared/operations/managedUsers';
import { deleteAdminUser } from '../features/operations/core';

export function UserDeletion({
  user,
  refresh,
}: {
  user: AdminUser;
  refresh: () => Promise<unknown>;
}) {
  const { t } = useTranslation();
  const passwordHintId = useId();
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [elevating, setElevating] = useState(false);
  const [elevationError, setElevationError] = useState<unknown>(null);
  const token = useRef<string | null>(null);
  const revision = useRef(user.revision);
  const deletion = useRetainedOperation(async (input: { revision: string }, key) => {
    const value = token.current;
    token.current = null;
    if (!value)
      throw new ApiError('elevated_required', t('management.users.deletePasswordRequired'), 403);
    await deleteAdminUser(user.id, input.revision, key, value);
  }, refresh);
  useEffect(() => {
    revision.current = user.revision;
    token.current = null;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setOpen(false);
    setElevating(false);
    setPassword('');
    setConfirmation('');
    setElevationError(null);
    return () => {
      revision.current = '';
      token.current = null;
    };
  }, [user.revision]);
  const submit = async () => {
    if (!password || confirmation !== 'DELETE') return;
    const expected = user.revision;
    const submitted = password;
    setPassword('');
    setConfirmation('');
    setElevationError(null);
    setElevating(true);
    try {
      const elevation = await elevateAdmin(submitted);
      if (revision.current !== expected) return;
      token.current = elevation.token;
      setOpen(false);
      deletion.mutate({ revision: expected });
    } catch (error) {
      if (revision.current === expected) setElevationError(error);
    } finally {
      if (revision.current === expected) setElevating(false);
    }
  };
  return (
    <>
      <button className="btn btn-danger" type="button" onClick={() => setOpen(true)}>
        {t('management.users.delete')}
      </button>
      {open ? (
        <>
          <ConfirmDialog
            open
            title={t('management.users.deleteTitle')}
            description={t('management.users.deleteRefreshOnlyBody')}
            confirmLabel={t('management.users.deleteConfirm')}
            confirmDisabled={!password || confirmation !== 'DELETE'}
            danger
            busy={elevating || deletion.isPending}
            onCancel={() => {
              setOpen(false);
              setPassword('');
              setConfirmation('');
              setElevationError(null);
            }}
            onConfirm={() => void submit()}
          >
            <label className="ops-form-field">
              <span>{t('management.users.elevatedPassword')}</span>
              <input
                type="password"
                autoComplete="current-password"
                aria-describedby={passwordHintId}
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </label>
            <small id={passwordHintId}>{t('management.users.elevatedPasswordHint')}</small>
            <label className="ops-form-field">
              <span>{t('management.users.deleteTypeConfirmation', { token: 'DELETE' })}</span>
              <input
                autoComplete="off"
                value={confirmation}
                onChange={(event) => setConfirmation(event.target.value)}
              />
            </label>
          </ConfirmDialog>
        </>
      ) : null}
      {deletion.error || elevationError ? (
        <ErrorState error={deletion.error ?? elevationError} />
      ) : null}
    </>
  );
}
