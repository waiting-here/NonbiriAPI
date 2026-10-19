import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Button, Note } from '@shared/components/ui';
import { ErrorState } from '@shared/components/States';
import {
  captureStationSession,
  clearStationSession,
  stationSessionMatches,
  StationSessionChangedError,
} from '@shared/charityManagement';
import { isUnauthorized } from '@shared/query/http';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { economyKeys } from './queries';
import {
  changeDonationHistory,
  changeDonationHistoryBatch,
  DonationHistoryRun,
  type HistoryProgress,
  type HistoryScope,
} from './donationHistory';

const progressLabel = {
  running: 'user.charity.ownerHistory.running',
  paused: 'user.charity.ownerHistory.paused',
  complete: 'user.charity.ownerHistory.complete',
} as const;

export function DonationHistoryToggle({
  id,
  hidden,
  disabled,
}: {
  id: string;
  hidden: boolean;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const operation = useRetainedOperation(
    (input: { hidden: boolean }, key, context) =>
      changeDonationHistory(id, input.hidden, key, context.signal),
    () => client.invalidateQueries({ queryKey: economyKeys.donations }, { throwOnError: true }),
    economyKeys.donations,
  );
  return (
    <div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        disabled={
          disabled ||
          operation.isPending ||
          operation.outcome === 'unknown' ||
          operation.outcome === 'refresh-failed'
        }
        onClick={() => operation.mutate({ hidden: !hidden })}
      >
        {t(hidden ? 'user.charity.ownerHistory.restoreOne' : 'user.charity.ownerHistory.hideOne')}
      </Button>
      {operation.error ? (
        <ErrorState
          error={operation.error}
          onRetry={() => {
            if (operation.variables) operation.mutate(operation.variables);
          }}
        />
      ) : null}
      {operation.outcome === 'refresh-failed' ? (
        <ErrorState error={operation.refreshError} onRetry={() => void operation.refresh()} />
      ) : null}
    </div>
  );
}

export function DonationHistoryBatch({
  accountID,
  scope,
  disabled,
}: {
  accountID: string;
  scope: HistoryScope;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const active = useRef<DonationHistoryRun | null>(null);
  const mounted = useRef(true);
  const [progress, setProgress] = useState<HistoryProgress | null>(null);
  const [refreshError, setRefreshError] = useState<unknown>(null);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      active.current?.stop();
    };
  }, []);
  const refresh = async () => {
    try {
      await client.invalidateQueries({ queryKey: economyKeys.donations }, { throwOnError: true });
      if (mounted.current) setRefreshError(null);
    } catch (error) {
      if (mounted.current) setRefreshError(error);
    }
  };
  const begin = () => {
    let session: ReturnType<typeof captureStationSession>;
    try {
      session = captureStationSession(client, 'steward');
      if (JSON.parse(session.subject)[1] !== accountID) throw new StationSessionChangedError();
    } catch (error) {
      setProgress({ ...scope, state: 'paused', changed: '0', scanned: '0', error });
      return;
    }
    const guard = () => {
      if (!mounted.current || !stationSessionMatches(client, 'steward', session))
        throw new StationSessionChangedError();
    };
    const run = new DonationHistoryRun(
      scope,
      async (input, key) => {
        guard();
        try {
          const result = await changeDonationHistoryBatch(input, key);
          guard();
          return result;
        } catch (error) {
          guard();
          if (isUnauthorized(error)) clearStationSession(client, 'steward');
          throw error;
        }
      },
      (next) => {
        if (!mounted.current || !stationSessionMatches(client, 'steward', session)) return;
        setProgress(next);
        if (next.state !== 'running') void refresh();
      },
    );
    active.current = run;
    void run.resume();
  };
  const running = progress?.state === 'running';
  const paused = progress?.state === 'paused';
  const unknown = responseOutcomeUnknown(progress?.error);
  return (
    <div className="economy-status-stack">
      <Note>{t('user.charity.ownerHistory.help')}</Note>
      <div className="form-actions">
        <Button
          type="button"
          disabled={disabled || running || (paused && (!progress.error || unknown))}
          onClick={begin}
        >
          {t(
            scope.action === 'hide'
              ? 'user.charity.ownerHistory.hideAll'
              : 'user.charity.ownerHistory.restoreAll',
          )}
        </Button>
        {running ? (
          <Button type="button" variant="ghost" onClick={() => active.current?.stop()}>
            {t('user.charity.ownerHistory.pause')}
          </Button>
        ) : null}
        {paused && (!progress.error || unknown) ? (
          <Button type="button" onClick={() => void active.current?.resume()}>
            {t('user.charity.ownerHistory.resume')}
          </Button>
        ) : null}
      </div>
      {progress ? (
        <p role="status">
          {t('user.charity.ownerHistory.progress', {
            action: t(
              progress.action === 'hide'
                ? 'user.charity.ownerHistory.hideOne'
                : 'user.charity.ownerHistory.restoreOne',
            ),
            changed: progress.changed,
            scanned: progress.scanned,
            state: t(progressLabel[progress.state]),
          })}
          {' · '}
          {progress.q || t('common.all')}
          {progress.status ? ' · ' + t(`user.charity.status.${progress.status}`) : ''}
        </p>
      ) : null}
      {progress?.error ? <ErrorState error={progress.error} /> : null}
      {refreshError ? <ErrorState error={refreshError} onRetry={() => void refresh()} /> : null}
    </div>
  );
}
