import { milliStringToDisplayInput } from './economyInput';

export function humanReadableSeconds(value: number, language: string): string {
  if (!Number.isSafeInteger(value)) return '';
  const negative = value < 0;
  let remaining = Math.abs(value);
  const parts: string[] = [];
  const units = language.startsWith('zh')
    ? [[86_400, '天'], [3_600, '小时'], [60, '分钟'], [1, '秒']] as const
    : [[86_400, 'd'], [3_600, 'h'], [60, 'm'], [1, 's']] as const;
  for (const [size, label] of units) {
    const count = Math.floor(remaining / size);
    if (count > 0 || (size === 1 && parts.length === 0)) {
      parts.push(language.startsWith('zh') ? `${count} ${label}` : `${count}${label}`);
    }
    remaining %= size;
  }
  return `${negative ? '-' : ''}${parts.join(' ')}`;
}

export function exactCreditDisplay(milliCredits: string): string | null {
  const display = milliStringToDisplayInput(milliCredits);
  return display === '' && milliCredits !== '0' ? null : display;
}
