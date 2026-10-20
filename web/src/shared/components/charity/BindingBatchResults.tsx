import { formatCount } from '@shared/utils/formatNumber';
import { useTranslation } from 'react-i18next';
import type { BindingMatrixReceipt } from '@shared/operations/charityBindingBatch';
import type { CharityRole } from '@shared/operations/charity';
import { usePagePager } from '@shared/operations/usePagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import { DataTable, Note } from '../ui';

const bindingStatusCopy = {
  added: 'common.bindingBatch.status.added',
  already_bound: 'common.bindingBatch.status.already_bound',
  missing_model: 'common.bindingBatch.status.missing_model',
  invalid_resource: 'common.bindingBatch.status.invalid_resource',
  capacity_exceeded: 'common.bindingBatch.status.capacity_exceeded',
} as const;

export interface BindingResultLabel {
  full_name: string;
  upstream_model_id: string;
}
export function BindingBatchResults({
  role,
  receipt,
  labels,
  scopeKey,
  keyNames = {},
}: {
  role: CharityRole;
  receipt: BindingMatrixReceipt;
  labels: BindingResultLabel[];
  scopeKey: string;
  keyNames?: Record<string, string>;
}) {
  const { t } = useTranslation();
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'binding-batch-results',
    scopeKey,
  });
  const rows = receipt.items.flatMap((key) =>
    key.results.map((status, index) => ({
      id: `${key.donation_key_id}:${index}`,
      key:
        keyNames[key.donation_key_id] ||
        t('common.bindingBatch.keyNumber', { id: key.donation_key_id }),
      ...labels[index]!,
      status,
    })),
  );
  const pages = Math.max(1, Math.ceil(rows.length / pager.pageSize));
  const page = Math.min(Number(pager.page), pages);
  return (
    <section className="nb-form" aria-label={t('common.bindingBatch.results')}>
      <Note tone="ok">
        {t('common.bindingBatch.resultSummary', {
          count: formatCount(receipt.added_count).display,
        })}
      </Note>
      <DataTable
        caption={t('common.bindingBatch.results')}
        rows={rows.slice((page - 1) * pager.pageSize, page * pager.pageSize)}
        rowKey={(row) => row.id}
        columns={[
          {
            key: 'key',
            header: t('common.bindingBatch.key'),
            mobileLabel: t('common.bindingBatch.key'),
            cell: 'title',
            render: (row) => row.key,
          },
          {
            key: 'target',
            header: t('common.workflow.model'),
            mobileLabel: t('common.workflow.model'),
            cell: 'meta',
            render: (row) => <span className="nb-workflow-id">{row.full_name}</span>,
          },
          {
            key: 'upstream',
            header: t('common.bindingBatch.upstream'),
            mobileLabel: t('common.bindingBatch.upstream'),
            cell: 'meta',
            render: (row) => <span className="mono nb-workflow-id">{row.upstream_model_id}</span>,
          },
          {
            key: 'status',
            header: t('common.bindingBatch.result'),
            mobileLabel: t('common.bindingBatch.result'),
            cell: 'meta',
            render: (row) => t(bindingStatusCopy[row.status]),
          },
        ]}
      />
      <PagePagination
        metadata={{
          page: String(page),
          page_size: pager.pageSize,
          total_items: String(rows.length),
          total_pages: String(pages),
        }}
        requestedPage={pager.page}
        onPageChange={pager.setPage}
        onPageSizeChange={pager.setPageSize}
      />
    </section>
  );
}
