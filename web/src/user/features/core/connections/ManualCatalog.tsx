import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Field, Note } from '@shared/components/ui';
import { listEndpointsPage, listEndpointKeysPage } from '../pageApi';
import type { Endpoint, EndpointKey } from '../types';
import { ConnectionResourceList } from './ResourceList';
import { useConnectionOperation } from './useConnectionOperation';
import { ConnectionOperationNotice } from './OperationNotice';

export function ManualCatalog({
  accountId,
  onPick,
  disabled = false,
}: {
  accountId: string;
  disabled?: boolean;
  onPick?: (entry: Endpoint, key: EndpointKey) => void;
}) {
  const { t } = useTranslation();
  const [entry, setEntry] = useState<Endpoint | null>(null);
  const [entryKey, setEntryKey] = useState<EndpointKey | null>(null);
  const [model, setModel] = useState('');
  const [provider, setProvider] = useState('');
  const operation = useConnectionOperation(accountId, () => {
    setModel('');
  });
  const locked = disabled || operation.isPending || operation.outcome === 'unknown';
  if (!entry)
    return (
      <ConnectionResourceList
        embedded
        accountId={accountId}
        scope="manual"
        listType="manual-entries"
        title={t('common.connections.chooseEntry')}
        searchPlaceholder={t('common.connections.entrySearch')}
        load={(window, q, signal) => listEndpointsPage(window, signal, { q })}
        rowKey={(row) => row.id}
        columns={[
          {
            key: 'name',
            header: t('common.connections.entry'),
            cell: 'title',
            render: (row) => row.note || row.base_url,
          },
          {
            key: 'select',
            header: t('common.connections.actions'),
            cell: 'action',
            render: (row) => (
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                onClick={() => setEntry(row)}
              >
                {t('common.connections.select')}
              </button>
            ),
          },
        ]}
      />
    );
  return (
    <div className="nb-stack">
      <button
        type="button"
        className="nb-btn nb-btn--secondary"
        disabled={locked}
        onClick={() => {
          setEntry(null);
          setEntryKey(null);
        }}
      >
        {t('common.connections.changeEntry')}
      </button>
      {!entryKey ? (
        <ConnectionResourceList
          embedded
          accountId={accountId}
          scope={entry.id}
          listType="manual-keys"
          title={t('common.connections.chooseCredential')}
          searchPlaceholder={t('common.connections.credentialSearch')}
          load={(window, q, signal) => listEndpointKeysPage(entry.id, window, signal, q)}
          rowKey={(row) => row.id}
          columns={[
            {
              key: 'name',
              header: t('common.connections.credential'),
              cell: 'title',
              render: (row) => row.note || row.display_head + '…' + row.display_tail,
            },
            {
              key: 'select',
              header: t('common.connections.actions'),
              cell: 'action',
              render: (row) => (
                <button
                  type="button"
                  className="nb-btn nb-btn--secondary"
                  disabled={locked || row.credential_available === false || !row.enabled}
                  onClick={() => {
                    if (onPick) onPick(entry, row);
                    else setEntryKey(row);
                  }}
                >
                  {t('common.connections.select')}
                </button>
              ),
            },
          ]}
        />
      ) : (
        <form
          className="nb-form"
          onSubmit={(event) => {
            event.preventDefault();
            void operation
              .run({
                kind: 'manual',
                endpointId: entry.id,
                keyId: entryKey.id,
                entries: [{ upstream_model_id: model.trim(), provider: provider.trim() }],
              })
              .catch(() => undefined);
          }}
        >
          <Note>{t('common.connections.manualTargetNote')}</Note>
          <Field label={t('common.connections.upstreamModel')}>
            {(props) => (
              <input
                {...props}
                value={model}
                required
                disabled={locked}
                onChange={(event) => setModel(event.target.value)}
              />
            )}
          </Field>
          <Field
            label={t('common.connections.provider')}
            optional={t('common.connections.optional')}
          >
            {(props) => (
              <input
                {...props}
                value={provider}
                disabled={locked}
                onChange={(event) => setProvider(event.target.value)}
              />
            )}
          </Field>
          <ConnectionOperationNotice operation={operation} />
          <button
            type="submit"
            className="nb-btn nb-btn--primary"
            disabled={locked || !model.trim()}
          >
            {t('common.connections.saveCatalogModel')}
          </button>
        </form>
      )}
    </div>
  );
}
