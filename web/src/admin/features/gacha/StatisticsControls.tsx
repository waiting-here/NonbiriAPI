import { Fragment } from 'react';
import { formatCount } from '@shared/utils/formatNumber';

export function StatisticsCount({ value }: { value: string }) {
  const exact = formatCount(value).display;
  const groups = exact.split(',');
  return (
    <span className="gacha-stats-count" aria-label={exact} title={exact}>
      {groups.map((group, index) => (
        <Fragment key={index}>
          <span className="gacha-stats-count-group">
            {group}
            {index < groups.length - 1 ? ',' : ''}
          </span>
          {index < groups.length - 1 ? <wbr /> : null}
        </Fragment>
      ))}
    </span>
  );
}
