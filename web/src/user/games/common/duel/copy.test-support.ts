import en from '../../../i18n/en.json' with { type: 'json' };
import zh from '../../../i18n/zh.json' with { type: 'json' };
import type { DuelText } from './copy';

export function testDuelText(
  duelCopyKeys: Readonly<Record<Parameters<DuelText>[0], string>>,
  language: 'zh' | 'en' = 'en',
): DuelText {
  return (alias, parameters) => {
    const root = language === 'zh' ? zh : en;
    const value = duelCopyKeys[alias]
      .split('.')
      .reduce<unknown>(
        (node, part) =>
          node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined,
        root,
      );
    if (typeof value !== 'string') throw new Error(`Missing registered game copy: ${alias}`);
    return value.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
      parameters?.[key] === undefined ? match : String(parameters[key]),
    );
  };
}
