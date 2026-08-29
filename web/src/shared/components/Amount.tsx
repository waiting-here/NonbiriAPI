import type { HTMLAttributes, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

export type AmountValue = string | bigint;

export interface AmountProps extends Omit<HTMLAttributes<HTMLSpanElement>, 'children'> {
  /** Canonical integer milli-credits; bigint is reserved for trusted callers. */
  value: AmountValue;
  unit?: ReactNode;
}

const CANONICAL_MILLI = /^-?(0|[1-9][0-9]*)$/;

function groupDigits(value: string): string {
  let result = '';
  for (let index = 0; index < value.length; index += 1) {
    if (index > 0 && (value.length - index) % 3 === 0) result += ',';
    result += value[index];
  }
  return result;
}

/**
 * Formats a canonical integer milli-credit value as display credits. Strings
 * are validated before BigInt conversion so hostile values (whitespace,
 * exponent notation, a plus sign, decimals, and non-canonical leading zeros)
 * never get silently coerced. BigInt callers are trusted internal values.
 */
export function formatAmount(value: AmountValue): string {
  let integer: bigint;
  try {
    if (typeof value === 'string' && (!CANONICAL_MILLI.test(value) || value === '-0')) return '—';
    integer = typeof value === 'bigint' ? value : BigInt(value);
  } catch {
    return '—';
  }
  const negative = integer < 0n;
  const absolute = negative ? -integer : integer;
  const whole = absolute / 1000n;
  const fraction = (absolute % 1000n).toString().padStart(3, '0').replace(/0+$/, '');
  const rendered = `${negative ? '-' : ''}${groupDigits(whole.toString())}${fraction ? `.${fraction}` : ''}`;
  return rendered;
}

export function Amount({ value, unit, className = '', ...props }: AmountProps) {
  const { t } = useTranslation();
  const rendered = formatAmount(value);
  const displayUnit = unit ?? t('common.creditsUnit');
  return (
    <span className={`nb-amount${className ? ` ${className}` : ''}`} {...props}>
      {rendered}
      {displayUnit ? <span className="nb-amount__unit"> {displayUnit}</span> : null}
    </span>
  );
}
