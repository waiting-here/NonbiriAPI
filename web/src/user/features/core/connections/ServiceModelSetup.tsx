import { useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Field, Note } from '@shared/components/ui';
import type { Endpoint, EndpointKey } from '../types';
import type { DiscoveryModel, DiscoverySource } from './types';
import { SourceModelMapping } from './SourceModelMapping';
import { DirectoryBatch } from './DirectoryBatch';
import { useConnectionOperation } from './useConnectionOperation';
import { ConnectionOperationNotice } from './OperationNotice';
import { connectionAPI } from './api';
import { connectionKeys } from './ResourceList';
import { CoreErrorPanel, DiscoveryFailureNotice } from '../components';

export function ServiceModelSetup({
  accountId,
  entry,
  entryKey,
  source,
}: {
  accountId: string;
  entry: Endpoint;
  entryKey: EndpointKey;
  source?: DiscoverySource;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const pollingStarted = useRef(0);
  const sourceKey = [...connectionKeys.root(accountId), 'setup-source', source?.id] as const;
  const current = useQuery({
    queryKey: sourceKey,
    initialData: source,
    staleTime: Infinity,
    enabled: Boolean(source),
    queryFn: async ({ signal }) => {
      const value = await connectionAPI.source(source!.id, signal);
      if (value.state !== 'checking')
        await client.invalidateQueries({
          queryKey: [...connectionKeys.root(accountId), value.id, 'source-models'],
        });
      return value;
    },
    refetchInterval: (query) => {
      if (query.state.data?.state !== 'checking') return false;
      if (!pollingStarted.current) pollingStarted.current = Date.now();
      return Date.now() - pollingStarted.current < 30_000 ? 1500 : false;
    },
  });
  const currentSource = current.data;
  const [manual, setManual] = useState('');
  const [rows, setRows] = useState<DiscoveryModel[]>([]);
  const refresh = useConnectionOperation(accountId, (result) => {
    if (result.kind === 'source_refresh') {
      pollingStarted.current = Date.now();
      client.setQueryData(sourceKey, result.source);
    }
  });
  const target = { entry, key: entryKey };
  return (
    <div className="nb-stack">
      {currentSource ? (
        <>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={refresh.isPending || refresh.outcome === 'unknown' || current.isFetching}
            onClick={() =>
              currentSource.state === 'checking'
                ? void current.refetch()
                : void refresh
                    .run({
                      kind: 'source_refresh',
                      sourceId: currentSource.id,
                      expectedRevision: currentSource.revision,
                    })
                    .catch(() => undefined)
            }
          >
            {t(
              currentSource.state === 'checking'
                ? 'common.connections.checkResult'
                : 'common.connections.refreshSource',
            )}
          </button>
          <ConnectionOperationNotice operation={refresh} />
          {currentSource.state === 'failed' ? (
            <>
              <DiscoveryFailureNotice failure={currentSource.safe_class} />
              {currentSource.safe_diag ? <Note tone="warn">{currentSource.safe_diag}</Note> : null}
            </>
          ) : currentSource.state === 'checking' ? (
            <Note>{t(`common.connections.sourceStates.${currentSource.state}`)}</Note>
          ) : null}
          {current.error ? (
            <CoreErrorPanel error={current.error} onRetry={() => void current.refetch()} />
          ) : null}
          <SourceModelMapping accountId={accountId} source={currentSource} initialTarget={target} />
        </>
      ) : (
        <>
          <Note>{t('user.workflows.manualModelHelp')}</Note>
          <form
            className="nb-form"
            onSubmit={(event) => {
              event.preventDefault();
              const name = manual.trim();
              if (name && !rows.some((row) => row.upstream_model_id === name))
                setRows((previous) => [
                  ...previous,
                  {
                    id: 'manual:' + name,
                    upstream_model_id: name,
                    provider: '',
                    native_services: [],
                    service_evidence: 'unknown',
                    needs_service_selection: true,
                  },
                ]);
              setManual('');
            }}
          >
            <Field label={t('common.connections.upstreamModel')}>
              {(props) => (
                <input
                  {...props}
                  value={manual}
                  required
                  maxLength={512}
                  onChange={(event) => setManual(event.target.value)}
                />
              )}
            </Field>
            <button type="submit" className="nb-btn nb-btn--secondary" disabled={!manual.trim()}>
              {t('common.connections.configureModel')}
            </button>
          </form>
          {rows.length ? (
            <DirectoryBatch accountId={accountId} rows={rows} initialTarget={target} />
          ) : null}
        </>
      )}
    </div>
  );
}
