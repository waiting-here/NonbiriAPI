import { Fragment, type MouseEvent, type ReactNode } from 'react';

export interface DataColumn<Row> {
  key: string;
  header: ReactNode;
  cell: 'title' | 'status' | 'meta' | 'action' | 'hide-m' | 'select';

  mobileLabel?: string;
  align?: 'num' | 'action' | 'nowrap';
  render: (row: Row) => ReactNode;
}

function columnClassName<Row>(column: DataColumn<Row>): string | undefined {
  const align = column.align ?? (column.cell === 'action' ? 'action' : undefined);
  const classes = [align ? `is-${align}` : '', column.cell === 'select' ? 'is-select' : ''].filter(
    Boolean,
  );
  return classes.length > 0 ? classes.join(' ') : undefined;
}

export function DataTable<Row>({
  caption,
  columns,
  rows,
  rowKey,
  selectedKey,
  dense,
  renderDetail,
  onRowClick,
}: {
  caption: string;
  columns: readonly DataColumn<Row>[];
  rows: readonly Row[];
  rowKey: (row: Row) => string;
  selectedKey?: string;
  dense?: boolean;
  renderDetail?: (row: Row) => ReactNode;
  onRowClick?: (row: Row, event: MouseEvent<HTMLTableRowElement>) => void;
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
                data-column={column.key}
                className={columnClassName(column)}
              >
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const key = rowKey(row);
            const detail = renderDetail?.(row);
            return (
              <Fragment key={key}>
                <tr
                  onClick={onRowClick ? (event) => onRowClick(row, event) : undefined}
                  aria-current={selectedKey === key ? 'true' : undefined}
                >
                  {columns.map((column) => (
                    <td
                      key={column.key}
                      data-column={column.key}
                      data-cell={column.cell}
                      data-label={
                        column.mobileLabel ??
                        (typeof column.header === 'string' ? column.header : undefined)
                      }
                      className={columnClassName(column)}
                    >
                      {column.render(row)}
                    </td>
                  ))}
                </tr>
                {detail ? (
                  <tr className="nb-table__detail">
                    <td colSpan={columns.length}>{detail}</td>
                  </tr>
                ) : null}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
