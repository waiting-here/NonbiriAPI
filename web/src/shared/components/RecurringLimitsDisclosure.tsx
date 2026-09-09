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
      className="ops-subcard recurring-limits-disclosure"
      onToggle={(event) => {
        if (event.currentTarget.open) setLoaded(true);
      }}
    >
      <summary>
        {copy.title}
        {label ? ` · ${label}` : ''}
      </summary>
      {loaded ? <RecurringLimits {...props} /> : null}
    </details>
  );
}
