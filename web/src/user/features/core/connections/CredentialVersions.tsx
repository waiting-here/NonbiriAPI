import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { DataTable, Note, Panel, PanelBody, PanelHead } from '@shared/components/ui';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { ApiError } from '@shared/query/http';
import { CoreEmpty, CoreErrorPanel, CoreLoading, CoreTime } from '../components';
import type { PageWindow } from '../pageTypes';
import { connectionAPI, resourcePath } from './api';
import { connectionKeys } from './ResourceList';
import { useMetadataOperation } from './useMetadataOperation';
import { ConnectionOperationNotice } from './OperationNotice';
import type { CredentialVersion } from './types';

export function CredentialVersions({
  accountId,
  credentialId,
}: {
  accountId: string;
  credentialId: string;
}) {
  const { t } = useTranslation();
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'credential-versions',
    scopeKey: accountId + ':' + credentialId,
    pageParam: 'version_page',
    pageSizeParam: 'version_page_size',
  });
  const query = useQuery({
    queryKey: [
      ...connectionKeys.root(accountId),
      'versions',
      credentialId,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) => connectionAPI.versions(credentialId, pager, signal),
  });
  return (
    <Panel>
      <PanelHead title={t('common.connections.versionHistory')} />
      <PanelBody>
        <Note>{t('common.connections.versionSwitchEffect')}</Note>
        {query.isPending ? (
          <CoreLoading />
        ) : query.error ? (
          <CoreErrorPanel error={query.error} onRetry={() => void query.refetch()} />
        ) : query.data ? (
          <>
            {query.data.data.length ? (
              <DataTable
                caption={t('common.connections.versionHistory')}
                rowKey={(row) => row.credential_version}
                rows={query.data.data}
                columns={[
                  {
                    key: 'version',
                    header: t('common.connections.version'),
                    cell: 'title',
                    render: (row) =>
                      t('common.connections.versionNumber', { version: row.credential_version }),
                  },
                  {
                    key: 'display',
                    header: t('common.connections.credential'),
                    cell: 'meta',
                    render: (row) => (
                      <span className="nb-mono">
                        {row.display_head}••••{row.display_tail}
                      </span>
                    ),
                  },
                  {
                    key: 'created',
                    header: t('common.connections.created'),
                    cell: 'meta',
                    render: (row) => <CoreTime value={row.created_at} />,
                  },
                  {
                    key: 'actions',
                    header: t('common.connections.actions'),
                    cell: 'action',
                    render: (row) => (
                      <VersionControl
                        accountId={accountId}
                        version={row}
                        window={{
                          page: query.data!.pagination.page,
                          pageSize: query.data!.pagination.page_size,
                        }}
                      />
                    ),
                  },
                ]}
              />
            ) : (
              <CoreEmpty
                title={t('common.connections.emptyTitle')}
                body={t('common.connections.emptyBody')}
              />
            )}
            <PagePagination
              metadata={query.data.pagination}
              requestedPage={pager.page}
              busy={query.isFetching}
              onPageChange={pager.setPage}
              onPageSizeChange={pager.setPageSize}
            />
          </>
        ) : null}
      </PanelBody>
    </Panel>
  );
}
function VersionControl({
  accountId,
  version,
  window,
}: {
  accountId: string;
  version: CredentialVersion;
  window: PageWindow;
}) {
  const { t } = useTranslation();
  const [disabling, setDisabling] = useState<string | null>(null);
  const operation = useMetadataOperation(
    accountId,
    '/api/credentials/' +
      resourcePath(version.credential_id) +
      '/versions/' +
      resourcePath(version.credential_version),
    'PATCH',
    async (signal) => {
      const page = await connectionAPI.versions(version.credential_id, window, signal);
      const current = page.data.find(
        (item) => item.credential_version === version.credential_version,
      );
      if (!current)
        throw new ApiError(
          'conflict',
          'The version list changed. Read the current page again.',
          409,
        );
      return current;
    },
    () => setDisabling(null),
  );
  return (
    <>
      <span>
        {version.enabled ? t('common.connections.enabled') : t('common.connections.disabled')}
      </span>
      <button
        type="button"
        className={'nb-btn ' + (version.enabled ? 'nb-btn--danger-outline' : 'nb-btn--secondary')}
        disabled={operation.isPending || operation.outcome === 'unknown'}
        onClick={() => {
          if (version.enabled) {
            operation.reset();
            setDisabling(version.revision);
          } else
            void operation
              .mutateAsync({ enabled: true, expected_revision: version.revision })
              .catch(() => undefined);
        }}
      >
        {version.enabled ? t('common.connections.disable') : t('common.connections.enable')}
      </button>
      {disabling === null ? <ConnectionOperationNotice operation={operation} /> : null}
      <ConfirmDialog
        open={disabling !== null}
        title={t('common.connections.disableVersion')}
        description={t('common.connections.versionSwitchEffect')}
        confirmLabel={t('common.connections.disable')}
        danger
        busy={operation.isPending}
        confirmDisabled={operation.outcome === 'unknown' || operation.outcome === 'conflict'}
        onCancel={() => setDisabling(null)}
        onConfirm={() => {
          if (disabling)
            void operation
              .mutateAsync({ enabled: false, expected_revision: disabling })
              .catch(() => undefined);
        }}
      >
        <ConnectionOperationNotice operation={operation} />
      </ConfirmDialog>
    </>
  );
}
