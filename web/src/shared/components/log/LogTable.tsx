import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

// Column-configuration table shared by both log screens. The caller owns the
// column set (the two stations project different fields), the row key, and
// the optional trailing action cell; this component only owns structure and
// accessibility. All cell content is rendered as React children — no HTML
// string ever reaches this table.

export interface LogColumn<Row> {
  key: string;
  header: string;
  render: (row: Row) => ReactNode;
}

interface LogTableProps<Row> {
  caption: string;
  columns: readonly LogColumn<Row>[];
  rows: readonly Row[];
  rowKey: (row: Row) => string;
  /** Optional trailing action column (e.g. the detail button). */
  actions?: (row: Row) => ReactNode;
}

export function LogTable<Row>({ caption, columns, rows, rowKey, actions }: LogTableProps<Row>) {
  const { t } = useTranslation();
  return (
    <div className="table-wrap">
      <table>
        <caption>{caption}</caption>
        <thead>
          <tr>
            {columns.map((column) => (
              <th key={column.key} scope="col">
                {column.header}
              </th>
            ))}
            {actions ? <th scope="col">{t('logs.details')}</th> : null}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={rowKey(row)}>
              {columns.map((column) => (
                <td key={column.key}>{column.render(row)}</td>
              ))}
              {actions ? <td>{actions(row)}</td> : null}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
