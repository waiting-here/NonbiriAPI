import { ExactCredits } from '../core/components';

export function CreditChange({ value }: { value: string }) {
  const negative = value.startsWith('-');
  const zero = value === '0';
  return (
    <span className={'credit-change' + (zero ? '' : negative ? ' is-expense' : ' is-income')}>
      {zero ? '' : negative ? '−' : '+'}
      <ExactCredits value={negative ? value.slice(1) : value} />
    </span>
  );
}
