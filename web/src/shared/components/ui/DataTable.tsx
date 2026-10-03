import { type ReactNode } from 'react';

export interface DataColumn<Row> {
  key: string;
  header: ReactNode;
  cell: 'title' | 'status' | 'meta' | 'action' | 'hide-m';

  mobileLabel?: string;
  align?: 'num' | 'action' | 'nowrap';
  render: (row: Row) => ReactNode;
}

export function DataTable<Row>({
  caption,
  columns,
  rows,
  rowKey,
  selectedKey,
  dense,
}: {
  caption: string;
  columns: readonly DataColumn<Row>[];
  rows: readonly Row[];
  rowKey: (row: Row) => string;
  selectedKey?: string;
  dense?: boolean;
}) {
  return (
    <div className="nb-table-wrap">
      <table className={`nb-table${dense ? ' nb-table--dense' : ''}`}>
        <caption className="nb-sr">{caption}</caption>
        <thead>
          <tr>
            {columns.map((column) => (
              <th
                key={column.key}
                scope="col"
                className={column.align ? `is-${column.align}` : undefined}
              >
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const key = rowKey(row);
            return (
              <tr key={key} aria-selected={selectedKey === key ? true : undefined}>
                {columns.map((column) => (
                  <td
                    key={column.key}
                    data-cell={column.cell}
                    data-label={column.mobileLabel}
                    className={column.align ? `is-${column.align}` : undefined}
                  >
                    {column.render(row)}
                  </td>
                ))}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
