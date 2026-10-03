import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { DataTable, type DataColumn } from '@shared/components/ui/DataTable';
import './logs.css';

export interface LogColumn<Row> {
  key: string;
  header: string;
  render: (row: Row) => ReactNode;
  cell?: DataColumn<Row>['cell'];
  mobileLabel?: string;
  align?: DataColumn<Row>['align'];
}

interface LogTableProps<Row> {
  caption: string;
  columns: readonly LogColumn<Row>[];
  rows: readonly Row[];
  rowKey: (row: Row) => string;
  actions?: (row: Row) => ReactNode;
}

export function LogTable<Row>({ caption, columns, rows, rowKey, actions }: LogTableProps<Row>) {
  const { t } = useTranslation();
  const dataColumns: DataColumn<Row>[] = columns.map((column, index) => ({
    ...column,
    cell: column.cell ?? (index === 0 ? 'title' : 'meta'),
    mobileLabel: column.mobileLabel ?? column.header,
  }));
  if (actions)
    dataColumns.push({
      key: 'actions',
      header: t('logs.details'),
      cell: 'action',
      align: 'action',
      render: actions,
    });
  return (
    <div className="log-table">
      <DataTable caption={caption} columns={dataColumns} rows={rows} rowKey={rowKey} dense />
    </div>
  );
}
