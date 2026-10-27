import { Fragment, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Field } from '@shared/components/ui';
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
export function StatisticsSearchField({
  label,
  placeholder,
  value,
  onSearch,
}: {
  label: string;
  placeholder: string;
  value: string;
  onSearch: (value: string) => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(value);
  return (
    <>
      <Field label={label}>
        {(props) => (
          <input
            {...props}
            type="search"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder={placeholder}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                onSearch(draft.trim());
              }
            }}
          />
        )}
      </Field>
      <Button type="button" onClick={() => onSearch(draft.trim())}>
        {t('admin.gachaStats.search')}
      </Button>
    </>
  );
}
