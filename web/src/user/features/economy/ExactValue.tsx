import { Amount } from '@shared/components/Amount';
import { amountToMilli } from './normalize';

function groupInteger(value: string): string {
  let result = '';
  for (let index = 0; index < value.length; index += 1) {
    if (index > 0 && (value.length - index) % 3 === 0) result += ',';
    result += value[index];
  }
  return result;
}

export function CreditAmount({ value, unit }: { value: string; unit?: string }) {
  return <Amount value={amountToMilli(value)} unit={unit} />;
}

export function ExactCount({ value, unit }: { value: string; unit?: string }) {
  return (
    <span className="economy-exact-count">
      {groupInteger(value)}
      {unit ? <span className="economy-exact-count__unit"> {unit}</span> : null}
    </span>
  );
}
