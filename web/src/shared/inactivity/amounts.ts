const maxAmount = 170141183460469231731687303715884105727n;
export function decimalAmount(value: string, places: number): string {
  if (!/^(0|[1-9][0-9]{0,38})$/.test(value)) return '';
  const scale = 10n ** BigInt(places),
    amount = BigInt(value);
  const fraction = (amount % scale).toString().padStart(places, '0').replace(/0+$/, '');
  return `${amount / scale}${fraction ? '.' + fraction : ''}`;
}
export function scaledAmount(value: string, places: number): string | null {
  if (!new RegExp(`^(0|[1-9][0-9]{0,38})([.][0-9]{1,${places}})?$`).test(value)) return null;
  const [whole, fraction = ''] = value.split('.');
  const amount = BigInt(whole) * 10n ** BigInt(places) + BigInt(fraction.padEnd(places, '0'));
  return amount <= maxAmount ? amount.toString() : null;
}
