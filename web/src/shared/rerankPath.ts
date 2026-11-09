export function rerankURLPreview(baseURL: string, path: string): string {
  try {
    const base = new URL(baseURL.trim());
    if (!['https:', 'http:'].includes(base.protocol)) return '';
    if (!path) return baseURL.trim().replace(/\/+$/, '') + '/rerank';
    if (!path.startsWith('/') || path.startsWith('//') || /[?#\\]/.test(path)) return '';
    const target = new URL(path, base.origin);
    return target.origin === base.origin ? target.href : '';
  } catch {
    return '';
  }
}
