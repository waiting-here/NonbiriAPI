import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  captureStationSession,
  stationSessionMatches,
  stationSessionWrite,
} from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ErrorState, LoadingState } from '@shared/components/States';
import { Button, Field, Note, Panel, PanelBody, PanelHead } from '@shared/components/ui';
import { elevateAdmin } from '@shared/operations/api';
import { getManagedMfa, resetManagedMfa } from '@shared/operations/mfa';
import type { AdminUser } from '@shared/operations/managedUsers';

export function UserMfaReset({ user }: { user: AdminUser }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [done, setDone] = useState(false);
  const active = useRef(false);
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  const status = useQuery({
    queryKey: ['admin', 'user-mfa', user.id],
    queryFn: () => stationSessionWrite(client, 'admin', () => getManagedMfa(user.id)),
    retry: false,
  });
  const submit = async () => {
    if (!password || busy) return;
    const snapshot = captureStationSession(client, 'admin');
    const submitted = password;
    setPassword('');
    setBusy(true);
    setError(null);
    try {
      const elevated = await elevateAdmin(submitted);
      if (!active.current || !stationSessionMatches(client, 'admin', snapshot)) return;
      await stationSessionWrite(client, 'admin', () => resetManagedMfa(user.id, elevated.token));
      if (!active.current || !stationSessionMatches(client, 'admin', snapshot)) return;
      setOpen(false);
      setDone(true);
      await status.refetch();
    } catch (failure) {
      if (active.current && stationSessionMatches(client, 'admin', snapshot)) setError(failure);
    } finally {
      if (active.current && stationSessionMatches(client, 'admin', snapshot)) setBusy(false);
    }
  };
  return (
    <Panel>
      <PanelHead title={t('mfa.adminTitle')} description={t('mfa.adminDescription')} />
      <PanelBody>
        {status.isPending ? (
          <LoadingState />
        ) : status.error ? (
          <ErrorState error={status.error} onRetry={() => void status.refetch()} />
        ) : (
          <>
            <p>{t(status.data.enrolled ? 'mfa.enabled' : 'mfa.notEnabled')}</p>
            {status.data.enrolled ? (
              <Button
                type="button"
                variant="danger-outline"
                onClick={() => {
                  setOpen(true);
                  setError(null);
                  setDone(false);
                }}
              >
                {t('mfa.reset')}
              </Button>
            ) : null}
          </>
        )}
        {done ? <Note tone="ok">{t('mfa.resetDone')}</Note> : null}
      </PanelBody>
      {open ? (
        <ConfirmDialog
          open
          title={t('mfa.resetTitle')}
          description={t('mfa.resetDescription', { user: user.username })}
          danger
          busy={busy}
          confirmLabel={t('mfa.resetConfirm')}
          confirmDisabled={!password}
          onCancel={() => {
            setOpen(false);
            setPassword('');
            setError(null);
          }}
          onConfirm={() => void submit()}
        >
          <Field
            label={t('management.users.elevatedPassword')}
            help={t('management.users.elevatedPasswordHint')}
          >
            {(props) => (
              <input
                {...props}
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            )}
          </Field>
          {error ? <ErrorState error={error} /> : null}
        </ConfirmDialog>
      ) : null}
    </Panel>
  );
}
