import { useTranslation } from 'react-i18next';
import { Note } from '@shared/components/ui';
import { connectionAPI } from './api';
import { ConnectionResourceList } from './ResourceList';
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
  if (!connectionId) return <Note>{t('common.connections.chooseConnectionFirst')}</Note>;
  return (
    <div className="nb-stack">
      {connectionId ? (
        <ConnectionResourceList
          embedded
          accountId={accountId}
          scope={connectionId}
          listType="credential-picker"
          title={t('common.connections.chooseCredential')}
          searchPlaceholder={t('common.connections.credentialSearch')}
          load={(window, q, signal) => connectionAPI.credentials(connectionId, window, q, signal)}
          rowKey={(row) => row.id}
          columns={[
            {
              key: 'name',
              header: t('common.connections.credentialName'),
              cell: 'title',
              render: (row) => <span>{credentialLabel(row)}</span>,
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
    </div>
  );
}
