import { useTranslation } from 'react-i18next';
import { ExactCredits } from '../core/components';

function groupInteger(value: string): string {
  let result = '';
  for (let index = 0; index < value.length; index += 1) {
    if (index > 0 && (value.length - index) % 3 === 0) result += ',';
    result += value[index];
  }
  return result;
}

export function CreditAmount({ value, unit }: { value: string; unit?: string }) {
  const { t } = useTranslation();
  const displayUnit = unit ?? t('common.creditsUnit');
  return (
    <span className="nb-amount">
      <ExactCredits value={value} />
      {displayUnit ? <span className="nb-amount__unit"> {displayUnit}</span> : null}
    </span>
  );
}

export function ExactCount({ value, unit }: { value: string; unit?: string }) {
  return (
    <span className="economy-exact-count">
      {groupInteger(value)}
      {unit ? <span className="economy-exact-count__unit"> {unit}</span> : null}
    </span>
  );
}
