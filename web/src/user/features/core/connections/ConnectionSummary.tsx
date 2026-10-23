import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { formatCount } from '@shared/utils/formatNumber';
import type { Connection } from './types';
import './connection-summary.css';

export function ConnectionSummary({
  connection,
  to,
  showEntryCount = true,
}: {
  connection: Connection;
  to?: string;
  showEntryCount?: boolean;
}) {
  const { t } = useTranslation();
  const name = connection.display_name;
  const entry = connection.primary_entry;
  const notes = [...new Set([connection.note, entry?.note])].filter(
    (note) => note && note !== name,
  );
  return (
    <div className="connection-summary nb-stack nb-stack--sm">
      <strong className="connection-summary__text" title={name}>
        {to ? <Link to={to}>{name}</Link> : name}
      </strong>
      {entry ? (
        <span className="connection-summary__text mono nb-small nb-muted" title={entry.base_url}>
          {entry.base_url}
        </span>
      ) : null}
      {notes.map((note) => (
        <span className="connection-summary__text nb-small nb-muted" title={note} key={note}>
          {note}
        </span>
      ))}
      {showEntryCount && BigInt(connection.entry_count) > 1n ? (
        <span className="connection-summary__count nb-small nb-muted">
          {t('common.connections.entryCount', {
            count: formatCount(connection.entry_count).display,
          })}
        </span>
      ) : null}
    </div>
  );
}
