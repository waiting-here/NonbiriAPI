import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { RecurringLimits, type RecurringLimitsProps } from './RecurringLimits';
import { copyForRecurringLimits } from './recurringLimitsCopy';

// The complete rule set is fetched only after this particular key is opened.
// Keep an opened editor mounted when folded so unresolved commands survive.
export function RecurringLimitsDisclosure({
  label,
  ...props
}: RecurringLimitsProps & { label?: string }) {
  const { i18n } = useTranslation();
  const [loaded, setLoaded] = useState(false);
  const copy = copyForRecurringLimits(i18n.language);
  return (
    <details
      className="nb-fold recurring-limits-disclosure"
      onToggle={(event) => {
        if (event.currentTarget.open) setLoaded(true);
      }}
    >
      <summary>
        <span className="nb-fold__title">
          <strong>
            {copy.title}
            {label ? ` · ${label}` : ''}
          </strong>
        </span>
      </summary>
      <div className="nb-fold__body">{loaded ? <RecurringLimits {...props} /> : null}</div>
    </details>
  );
}
