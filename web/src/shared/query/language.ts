/** The document language is synchronized with the station language switch. */
export function requestLanguage(): string {
  return typeof document !== 'undefined' &&
    document.documentElement.lang.toLowerCase().startsWith('zh')
    ? 'zh-CN'
    : 'en';
}
