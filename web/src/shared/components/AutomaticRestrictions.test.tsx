import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { LoginRestrictions } from './AutomaticRestrictions';

function response(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
}
afterEach(() => window.history.replaceState(null, '', '/'));

describe('verified login denial reasons', () => {
  it('ignores fragment claims, pages through server reasons, and translates automatic facts without rewriting manual notes', async () => {
    window.history.replaceState(null, '', '/access-denied#restrictions=untrusted');
    const fetcher = vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://example.test');
      expect(url.pathname).toBe('/api/auth/access-denied-reasons');
      expect(url.searchParams.has('discord_id')).toBe(false);
      return url.searchParams.get('cursor')
        ? response({ restricted: true, items: [{ kind: 'blacklist_note', reason: '追加人工原文', started_at: 2, ends_at: null }] })
        : response({ restricted: true, next_cursor: 'next-safe-page', items: [{ kind: 'ban', reason: 'Server-language summary', started_at: 1, ends_at: null, automatic_reason: { kind: 'client_rules', schema_version: 1, params: {}, rules: [{ name: '<script>rule label</script>' }], manual_text: 'Original manual note 原文' } }] });
    });
    vi.stubGlobal('fetch', fetcher);
    const view = await renderWithProviders(<LoginRestrictions />, { station: 'user', role: 'anonymous' });
    expect(await screen.findByText('The following client usage rules were matched:')).toBeVisible();
    expect(screen.getByText('<script>rule label</script>')).toBeVisible();
    expect(document.querySelector('script')).not.toBeInTheDocument();
    expect(window.location.hash).toBe('');
    await view.user.click(screen.getByRole('button', { name: 'Load more notes' }));
    expect(await screen.findByText('追加人工原文')).toBeVisible();
    await act(async () => { await view.i18n.changeLanguage('zh'); });
    expect(screen.getByText('匹配了以下客户端使用规则：')).toBeVisible();
    expect(screen.getByText(/Original manual note 原文/)).toBeVisible();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('shows a new login action when the short-lived grant is absent or expired', async () => {
    window.history.replaceState(null, '', '/access-denied');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ error: { code: 'unauthorized', message: 'Expired fixture grant' } }, 401)));
    await renderWithProviders(<LoginRestrictions />, { station: 'user', role: 'anonymous' });
    await waitFor(() => expect(screen.getByText(/sign in with Discord again/)).toBeVisible());
    expect(screen.queryByRole('heading', { name: 'Current access restriction reasons' })).not.toBeInTheDocument();
    expect(screen.queryByText('Expired fixture grant')).not.toBeInTheDocument();
  });
});
