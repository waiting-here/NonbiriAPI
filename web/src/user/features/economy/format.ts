export function maskedKey(head: string, tail: string): string {
  if (head && tail) return `${head}…${tail}`;
  if (head) return `${head}…`;
  if (tail) return `…${tail}`;
  return '••••';
}
