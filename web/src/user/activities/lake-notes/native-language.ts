import zh from '../../i18n/zh.json';
import en from '../../i18n/en.json';
import original from './native.en.json';

const words = new Map<string, string>();
function collect(source: unknown, target: unknown) {
  if (typeof source === 'string' && typeof target === 'string') {
    if (!source.includes('{{')) words.set(source, target);
  } else if (source && target && typeof source === 'object' && typeof target === 'object') {
    for (const [key, value] of Object.entries(source))
      collect(value, (target as Record<string, unknown>)[key]);
  }
}
collect(zh.user.lakeNotes, en.user.lakeNotes);
for (const [source, target] of Object.entries(original)) words.set(source, target);
const replacements = [...words]
  .filter(([source]) => /[\u4e00-\u9fff]/.test(source))
  .sort(([a], [b]) => b.length - a.length);
export function nativeEnglish(text: string): string {
  for (const [source, target] of replacements) text = text.replaceAll(source, target);
  return text;
}
