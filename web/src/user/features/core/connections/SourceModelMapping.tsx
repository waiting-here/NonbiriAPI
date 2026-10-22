import { useSearchParams } from 'react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Field, Fold, Note } from '@shared/components/ui';
import { NativeServicesSummary } from '@shared/components/ServiceRoutesSummary';
import { connectionAPI } from './api';
import { ConnectionResourceList } from './ResourceList';
import { DirectoryBatch } from './DirectoryBatch';
import type { DiscoveryModel, DiscoverySource } from './types';

export function SourceModelMapping({
  accountId,
  source,
}: {
  accountId: string;
  source: DiscoverySource;
}) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState<DiscoveryModel[]>([]);
  const [params, setParams] = useSearchParams();
  const rawState = params.get('source_models_state');
  const state = rawState === 'current' || rawState === 'stale' ? rawState : '';
  const setState = (value: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (value) next.set('source_models_state', value);
      else next.delete('source_models_state');
      return next;
    });
  const [manual, setManual] = useState('');
  const add = (row: DiscoveryModel) =>
    setSelected((previous) =>
      previous.some((item) => item.id === row.id) ? previous : [...previous, row],
    );
  return (
    <div className="nb-stack">
      <ConnectionResourceList
        accountId={accountId}
        scope={source.id}
        listType="source-models"
        title={t('common.connections.catalog')}
        searchPlaceholder={t('common.connections.modelSearch')}
        filterKey={state}
        filters={
          <Field label={t('common.connections.catalogState')}>
            {(props) => (
              <select
                {...props}
                value={state}
                onChange={(event) => setState(event.target.value as typeof state)}
              >
                <option value="">{t('common.connections.all')}</option>
                <option value="current">{t('common.connections.currentCatalog')}</option>
                <option value="stale">{t('common.connections.staleCatalog')}</option>
              </select>
            )}
          </Field>
        }
        load={(window, q, signal) =>
          connectionAPI.models(source.id, window, q, state || undefined, signal)
        }
        rowKey={(row) => row.id}
        columns={[
          {
            key: 'name',
            header: t('common.connections.upstreamModel'),
            cell: 'title',
            render: (row) => <strong className="nb-mono">{row.upstream_model_id}</strong>,
          },
          {
            key: 'services',
            header: t('common.connections.nativeServices'),
            cell: 'meta',
            render: (row) => <NativeServicesSummary value={row.native_services} />,
          },
          {
            key: 'choose',
            header: t('common.connections.actions'),
            cell: 'action',
            render: (row) => (
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                disabled={selected.some((item) => item.id === row.id)}
                onClick={() => add(row)}
              >
                {t('common.connections.configureModel')}
              </button>
            ),
          },
        ]}
      />
      <Fold title={t('common.connections.manualModel')}>
        <form
          className="nb-form"
          onSubmit={(event) => {
            event.preventDefault();
            add({
              id: 'manual:' + manual.trim(),
              upstream_model_id: manual.trim(),
              provider: '',
              native_services: [],
              service_evidence: 'unknown',
              needs_service_selection: true,
            });
            setManual('');
          }}
        >
          <Note>{t('common.connections.manualModelNote')}</Note>
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
      </Fold>
      {selected.length ? <DirectoryBatch accountId={accountId} rows={selected} /> : null}
    </div>
  );
}
