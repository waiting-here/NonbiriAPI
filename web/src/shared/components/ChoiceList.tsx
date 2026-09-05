import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

/** Browses one authoritative page without changing its order or fetching extra pages. */
export function ChoiceList<T>({
  items,
  getKey,
  getSearchText,
  label,
  searchable = true,
  children,
}: {
  items: readonly T[];
  getKey: (item: T) => string;
  getSearchText: (item: T) => string;
  label: string;
  searchable?: boolean;
  children: (item: T) => ReactNode;
}) {
  const { t } = useTranslation();
  const [filter, setFilter] = useState('');
  const query = filter.trim().toLocaleLowerCase();
  const visible = query
    ? items.filter((item) => getSearchText(item).toLocaleLowerCase().includes(query))
    : items;

  return (
    <section className="nb-choice-list" aria-label={label}>
      {searchable && (items.length > 8 || filter) ? (
        <div className="nb-choice-list__toolbar">
          <label>
            <span>{t('common.choices.filterPage')}</span>
            <input
              type="search"
              value={filter}
              maxLength={512}
              onChange={(event) => setFilter(event.target.value)}
            />
          </label>
          <span>{t('common.choices.count', { count: visible.length, total: items.length })}</span>
        </div>
      ) : null}
      {visible.length ? (
        <ul className="nb-choice-list__items">
          {visible.map((item) => (
            <li key={getKey(item)}>{children(item)}</li>
          ))}
        </ul>
      ) : (
        <p className="muted">{t('common.choices.noPageMatches')}</p>
      )}
    </section>
  );
}
