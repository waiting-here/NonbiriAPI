import type { FormattedNumber } from '@shared/utils/formatNumber';

// Renders a formatted number. Abbreviated values expose the exact figure
// through a native tooltip on hover and keyboard focus, and through the
// accessible name, so the precise amount never depends on visual hover alone.
export function CompactNumber({ value }: { value: FormattedNumber }) {
  if (!value.abbreviated) return <>{value.display}</>;
  return (
    <span className="compact-number" tabIndex={0} title={value.exact} aria-label={value.exact}>
      {value.display}
    </span>
  );
}
