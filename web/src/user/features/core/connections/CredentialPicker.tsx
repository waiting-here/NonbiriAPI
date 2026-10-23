import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Fold } from '@shared/components/ui';
import { connectionAPI } from './api';
import { ConnectionResourceList } from './ResourceList';
import { ConnectionSummary } from './ConnectionSummary';
import type { Credential } from './types';

export function credentialLabel(value: Credential) {
  return value.note || value.display_head + '••••' + value.display_tail;
}
export function CredentialPicker({
  accountId,
  connectionId,
  onPick,
}: {
  accountId: string;
  connectionId: string;
  onPick: (value: Credential) => void;
}) {
  const { t } = useTranslation();
  const [sourceConnection, setSourceConnection] = useState(connectionId);
  return (
    <div className="nb-stack">
      {sourceConnection ? (
        <ConnectionResourceList
          embedded
          accountId={accountId}
          scope={sourceConnection}
          listType="credential-picker"
          title={t('common.connections.chooseCredential')}
          searchPlaceholder={t('common.connections.credentialSearch')}
          load={(window, q, signal) =>
            connectionAPI.credentials(sourceConnection, window, q, signal)
          }
          rowKey={(row) => row.id}
          columns={[
            {
              key: 'name',
              header: t('common.connections.credentialName'),
              cell: 'title',
              render: (row) => <span>{credentialLabel(row)}</span>,
            },
            {
              key: 'version',
              header: t('common.connections.version'),
              cell: 'meta',
              render: (row) => (
                <span>
                  {t('common.connections.versionNumber', { version: row.credential_version })}
                </span>
              ),
            },
            {
              key: 'choose',
              header: t('common.connections.actions'),
              cell: 'action',
              render: (row) => (
                <button
                  type="button"
                  className="nb-btn nb-btn--secondary"
                  disabled={!row.enabled}
                  onClick={() => onPick(row)}
                >
                  {row.enabled ? t('common.connections.select') : t('common.connections.disabled')}
                </button>
              ),
            },
          ]}
        />
      ) : null}
      <Fold
        lazy
        title={t('common.connections.chooseOtherConnection')}
        defaultOpen={!sourceConnection}
      >
        <ConnectionResourceList
          embedded
          accountId={accountId}
          scope="all"
          listType="credential-connections"
          title={t('common.connections.title')}
          searchPlaceholder={t('common.connections.connectionSearch')}
          load={connectionAPI.list}
          rowKey={(row) => row.id}
          columns={[
            {
              key: 'name',
              header: t('common.connections.name'),
              cell: 'title',
              render: (row) => <ConnectionSummary connection={row} />,
            },
            {
              key: 'choose',
              header: t('common.connections.actions'),
              cell: 'action',
              render: (row) => (
                <button
                  type="button"
                  className="nb-btn nb-btn--secondary"
                  onClick={() => setSourceConnection(row.id)}
                >
                  {t('common.connections.showCredentials')}
                </button>
              ),
            },
          ]}
        />
      </Fold>
    </div>
  );
}
