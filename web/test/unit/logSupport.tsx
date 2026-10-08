import { renderWithProviders } from './support';

/** Historical fixtures explicitly request an unbounded range unless the test supplies one. */
export function renderHistoricalLogs(
  ui: Parameters<typeof renderWithProviders>[0],
  options: Parameters<typeof renderWithProviders>[1],
) {
  const url = new URL(options.route ?? '/logs', 'https://fixture.invalid');
  if (!url.searchParams.has('from') && !url.searchParams.has('to')) {
    url.searchParams.set('from', '');
    url.searchParams.set('to', '');
  }
  return renderWithProviders(ui, { ...options, route: `${url.pathname}${url.search}` });
}
