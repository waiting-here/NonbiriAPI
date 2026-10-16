import { useEffect, useState, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  captureStationSession,
  stationSessionMatches,
  type StationSessionSnapshot,
} from '@shared/charityManagement';
import { Button, Note } from '@shared/components/ui';
import { isMfaRequired } from '@shared/query/http';
import { observeMfaRequired } from '@shared/query/mfaRequired';
import { useUserSession } from '../../data';
import { MfaChallenge } from './MfaChallenge';

export function MfaRecovery({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const { t } = useTranslation();
  // Disabled observer tracks account changes without an additional bootstrap request.
  const session = useUserSession(false);
  const [pending, setPending] = useState<StationSessionSnapshot | null>(null);
  const [open, setOpen] = useState(false);
  const [recovered, setRecovered] = useState<StationSessionSnapshot | null>(null);
  useEffect(
    () =>
      observeMfaRequired(() => {
        let snapshot: StationSessionSnapshot;
        try {
          snapshot = captureStationSession(client, 'steward');
        } catch {
          return;
        }
        return () => {
          if (!stationSessionMatches(client, 'steward', snapshot)) return;
          setRecovered(null);
          setPending(snapshot);
          setOpen(true);
        };
      }),
    [client],
  );
  const current = Boolean(
    session.data && pending && stationSessionMatches(client, 'steward', pending),
  );
  const recover = async () => {
    const snapshot = pending;
    setPending(null);
    setOpen(false);
    await client.refetchQueries({ queryKey: ['user', 'session'], exact: true });
    if (!snapshot || !stationSessionMatches(client, 'steward', snapshot)) return;
    // Only reads blocked by MFA resume. Mutation observers retain their original failure.
    await client.refetchQueries({
      predicate: (query) => query.isActive() && isMfaRequired(query.state.error),
    });
    if (stationSessionMatches(client, 'steward', snapshot)) setRecovered(snapshot);
  };
  return (
    <>
      {current && !open ? (
        <div className="mfa-recovery">
          <Note
            tone="warn"
            action={
              <Button type="button" variant="primary" onClick={() => setOpen(true)}>
                {t('mfa.verify')}
              </Button>
            }
          >
            {t('mfa.recoveryDescription')}
          </Note>
        </div>
      ) : null}
      {recovered && session.data && stationSessionMatches(client, 'steward', recovered) ? (
        <div className="mfa-recovery">
          <Note tone="ok">{t('mfa.recovered')}</Note>
        </div>
      ) : null}
      {children}
      {current && open ? (
        <MfaChallenge
          key={`${pending?.subject}:${pending?.generation}`}
          dialog
          onCancel={() => setOpen(false)}
          onSuccess={recover}
        />
      ) : null}
    </>
  );
}
