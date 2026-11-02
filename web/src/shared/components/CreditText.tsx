import { Trans } from 'react-i18next';
import type { CopyKey, CopyParameters } from '@shared/i18n/useRegisteredCopy';
import { CreditNumber } from './CreditNumber';

/** Known translation slots for formatted credits; plain copy APIs remain strings. */
export function CreditText({
  i18nKey,
  amounts,
  values,
}: {
  readonly i18nKey: CopyKey;
  readonly amounts: Readonly<Record<string, string>>;
  readonly values?: CopyParameters;
}) {
  const components = Object.fromEntries(
    Object.entries(amounts).map(([slot, value]) => [
      slot,
      <CreditNumber key={slot} value={value} />,
    ]),
  );
  return (
    <Trans<string> i18nKey={i18nKey} values={{ ...values, ...amounts }} components={components} />
  );
}
