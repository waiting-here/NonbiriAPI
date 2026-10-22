import { useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ExpandablePanel } from '@shared/components/ui/ExpandablePanel';
import { Fold, Note, Panel, PanelBody, PanelHead } from '@shared/components/ui';
import { DiscoveryFailureNotice, CoreErrorPanel, CoreLoading, CoreTime } from '../components';
import { connectionAPI, resourcePath } from './api';
import { connectionKeys } from './ResourceList';
import { ConnectionOperationNotice } from './OperationNotice';
import { useConnectionOperation } from './useConnectionOperation';
import { useMetadataOperation } from './useMetadataOperation';
import { SourceForm } from './SourceForm';
import { SourceAssociations } from './SourceAssociations';
import { SourceModelMapping } from './SourceModelMapping';
import type { DiscoverySource } from './types';

export function DiscoverySourceDetail({
  accountId,
  id,
  onBack,
}: {
  accountId: string;
  id: string;
  onBack: () => void;
}) {
  const { t } = useTranslation();
  const polling = useRef({ marker: '', started: 0 });
  const source = useQuery({
    queryKey: [...connectionKeys.root(accountId), 'source', id],
    queryFn: ({ signal }) => connectionAPI.source(id, signal),
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data || data.state !== 'checking') return false;
      const marker = data.revision + ':' + data.updated_at;
      if (polling.current.marker !== marker) polling.current = { marker, started: Date.now() };
      return Date.now() - polling.current.started < 30000 ? 1500 : false;
    },
  });
  const back = (
    <button type="button" className="nb-btn nb-btn--secondary" onClick={onBack}>
      {t('common.connections.backToSources')}
    </button>
  );
  if (source.isPending)
    return (
      <div className="nb-stack">
        {back}
        <CoreLoading />
      </div>
    );
  if (source.error || !source.data)
    return (
      <div className="nb-stack">
        {back}
        <CoreErrorPanel error={source.error} onRetry={() => void source.refetch()} />
      </div>
    );
  return (
    <SourceDetails
      key={accountId + ':' + source.data.id}
      accountId={accountId}
      source={source.data}
      onBack={onBack}
      onCheck={() => void source.refetch()}
    />
  );
}
function SourceDetails({
  accountId,
  source,
  onBack,
  onCheck,
}: {
  accountId: string;
  source: DiscoverySource;
  onBack: () => void;
  onCheck: () => void;
}) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState(false);
  const [disabling, setDisabling] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);
  const refresh = useConnectionOperation(accountId, () => undefined);
  const path = '/api/discovery-sources/' + resourcePath(source.id);
  const change = useMetadataOperation(
    accountId,
    path,
    'PATCH',
    (signal) => connectionAPI.source(source.id, signal),
    () => undefined,
  );
  const remove = useMetadataOperation(
    accountId,
    path,
    'DELETE',
    (signal) => connectionAPI.source(source.id, signal),
    onBack,
  );
  const locked =
    refresh.isPending ||
    refresh.outcome === 'unknown' ||
    change.isPending ||
    change.outcome === 'unknown' ||
    remove.isPending ||
    remove.outcome === 'unknown';
  return (
    <div className="nb-stack">
      <button type="button" className="nb-btn nb-btn--secondary" onClick={onBack}>
        {t('common.connections.backToSources')}
      </button>
      <Panel>
        <PanelHead
          title={source.name || t('common.connections.discoverySource')}
          actions={
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              onClick={() => setEditing((value) => !value)}
            >
              {t('common.connections.edit')}
            </button>
          }
        />
        <PanelBody>
          <dl className="nb-facts">
            <div>
              <dt>{t('common.connections.modelsURL')}</dt>
              <dd className="nb-mono">{source.models_url}</dd>
            </div>
            <div>
              <dt>{t('common.connections.status')}</dt>
              <dd>
                {source.credential_available === false
                  ? t('common.connections.credentialUnavailable')
                  : t(`common.connections.sourceStates.${source.state}`)}
              </dd>
            </div>
            <div>
              <dt>{t('common.connections.version')}</dt>
              <dd>{source.credential_version}</dd>
            </div>
            <div>
              <dt>{t('common.connections.checkedAt')}</dt>
              <dd>
                <CoreTime value={source.checked_at} />
              </dd>
            </div>
          </dl>
          {source.state === 'failed' ? (
            <DiscoveryFailureNotice failure={source.safe_class} />
          ) : null}
          {source.safe_diag ? <Note tone="warn">{source.safe_diag}</Note> : null}
          <div className="nb-actions">
            {source.state === 'checking' ? (
              <button type="button" className="nb-btn nb-btn--secondary" onClick={onCheck}>
                {t('common.connections.checkResult')}
              </button>
            ) : null}
            <button
              type="button"
              className="nb-btn nb-btn--primary"
              disabled={
                locked ||
                !source.enabled ||
                source.credential_available === false ||
                source.state === 'checking'
              }
              onClick={() =>
                void refresh
                  .run({
                    kind: 'source_refresh',
                    sourceId: source.id,
                    expectedRevision: source.revision,
                  })
                  .catch(() => undefined)
              }
            >
              {t('common.connections.refreshSource')}
            </button>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={locked}
              onClick={() => {
                if (source.enabled) {
                  change.reset();
                  setDisabling(source.revision);
                } else
                  void change
                    .mutateAsync({ enabled: true, expected_revision: source.revision })
                    .catch(() => undefined);
              }}
            >
              {source.enabled ? t('common.connections.disable') : t('common.connections.enable')}
            </button>
            <button
              type="button"
              className="nb-btn nb-btn--danger-outline"
              disabled={locked}
              onClick={() => {
                remove.reset();
                setDeleting(source.revision);
              }}
            >
              {t('common.connections.delete')}
            </button>
          </div>
          <ConnectionOperationNotice operation={refresh} />
          <ConnectionOperationNotice operation={change} />
        </PanelBody>
      </Panel>
      {editing ? (
        <ExpandablePanel
          open
          title={t('common.connections.editSource')}
          closeLabel={t('common.connections.close')}
          onClose={() => setEditing(false)}
        >
          <SourceForm
            accountId={accountId}
            connectionId={source.connection_id ?? ''}
            source={source}
            onSaved={() => setEditing(false)}
          />
        </ExpandablePanel>
      ) : null}
      <Fold title={t('common.connections.associateEntries')}>
        <SourceAssociations accountId={accountId} source={source} />
      </Fold>
      <SourceModelMapping accountId={accountId} source={source} />
      <ConfirmDialog
        open={disabling !== null}
        title={t('common.connections.disable')}
        description={t('common.connections.disableSourceEffect')}
        confirmLabel={t('common.connections.disable')}
        danger
        busy={change.isPending}
        confirmDisabled={change.outcome === 'unknown' || change.outcome === 'conflict'}
        onCancel={() => setDisabling(null)}
        onConfirm={() => {
          if (disabling)
            void change
              .mutateAsync({ enabled: false, expected_revision: disabling })
              .then(() => setDisabling(null))
              .catch(() => undefined);
        }}
      >
        <ConnectionOperationNotice operation={change} />
      </ConfirmDialog>
      <ConfirmDialog
        open={deleting !== null}
        title={t('common.connections.deleteSource')}
        description={t('common.connections.deleteSourceEffect')}
        confirmLabel={t('common.connections.delete')}
        danger
        busy={remove.isPending}
        confirmDisabled={remove.outcome === 'unknown' || remove.outcome === 'conflict'}
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting)
            void remove.mutateAsync({ expected_revision: deleting }).catch(() => undefined);
        }}
      >
        <ConnectionOperationNotice operation={remove} />
      </ConfirmDialog>
    </div>
  );
}
