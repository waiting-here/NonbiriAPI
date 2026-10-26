import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { EndpointTagRules } from './EndpointTagRules';
import type { EndpointTagRule } from '../operations/endpoints';

function fixture({ loseResponse = false } = {}) {
  const rules = new Map<string, EndpointTagRule>();
  const writes: { method: string; key: string | null; body: unknown }[] = [];
  let lost = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), location.origin);
      expect(url.pathname).toMatch(/\/overview\/endpoints\/tag-rules$/);
      const method = init?.method ?? 'GET';
      if (method !== 'GET') {
        const body = JSON.parse(String(init?.body));
        writes.push({ method, key: new Headers(init?.headers).get('Idempotency-Key'), body });
        if (method === 'PUT') rules.set(body.pattern, body);
        else rules.delete(body.pattern);
        if (loseResponse && !lost) {
          lost = true;
          throw new TypeError('Response lost');
        }
        return new Response(null, { status: 204 });
      }
      const data = [...rules.values()].filter((rule) =>
        rule.pattern.includes(url.searchParams.get('q') ?? ''),
      );
      return Response.json({
        data,
        next_cursor: null,
        pagination: {
          page: '1',
          page_size: Number(url.searchParams.get('page_size') ?? 20),
          total_items: String(data.length),
          total_pages: '1',
        },
      });
    }),
  );
  return { rules, writes };
}

describe('endpoint wildcard tag rules', () => {
  it.each(['admin', 'steward'] as const)(
    'lets an authorized %s save, edit and remove a rule',
    async (role) => {
      const { writes } = fixture();
      const view = await renderWithProviders(<EndpointTagRules role={role} account="7" canTag />, {
        station: role === 'admin' ? 'admin' : 'user',
        route: '/endpoints',
      });
      await screen.findByText('No matching rules');
      await view.user.click(screen.getByRole('button', { name: 'Add rule' }));
      await view.user.type(
        screen.getByRole('textbox', { name: 'Domain pattern' }),
        '*.example.com',
      );
      await view.user.click(
        screen.getByRole('checkbox', { name: 'Mainstream channels and derivatives' }),
      );
      await view.user.click(screen.getByRole('button', { name: 'Save rule' }));
      await screen.findByText('*.example.com');
      expect(writes[0].body).toEqual({
        pattern: '*.example.com',
        action: 'add',
        tags: ['mainstream'],
      });
      await view.user.click(screen.getByRole('button', { name: 'Edit rule' }));
      expect(screen.getByRole('textbox', { name: 'Domain pattern' })).toHaveAttribute('readonly');
      await view.user.click(screen.getByRole('radio', { name: 'Clear tags' }));
      await view.user.click(screen.getByRole('button', { name: 'Save rule' }));
      await waitFor(() => expect(writes).toHaveLength(2));
      expect(writes[1].body).toEqual({ pattern: '*.example.com', action: 'remove', tags: [] });
      await view.user.click(await screen.findByRole('button', { name: 'Delete rule' }));
      await screen.findByText('No matching rules');
      expect(writes[2]).toMatchObject({ method: 'DELETE', body: { pattern: '*.example.com' } });
      expect(writes.every((write) => !!write.key)).toBe(true);
    },
  );

  it('allows read-only users to search rules without exposing mutation controls', async () => {
    const { rules, writes } = fixture();
    rules.set('*.example.com', { pattern: '*.example.com', action: 'remove', tags: [] });
    const view = await renderWithProviders(
      <EndpointTagRules role="steward" account="7" canTag={false} />,
      { station: 'user' },
    );
    await screen.findByText('*.example.com');
    expect(screen.queryByRole('button', { name: 'Add rule' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Edit rule' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete rule' })).not.toBeInTheDocument();
    await view.user.type(screen.getByRole('searchbox', { name: 'Find a domain rule' }), 'missing');
    await view.user.click(screen.getByRole('button', { name: 'Apply filter' }));
    await screen.findByText('No matching rules');
    expect(writes).toEqual([]);
  });

  it('retries an unknown save result with the same body and request identity', async () => {
    const { writes } = fixture({ loseResponse: true });
    const view = await renderWithProviders(<EndpointTagRules role="admin" account="7" canTag />, {
      station: 'admin',
    });
    await screen.findByText('No matching rules');
    await view.user.click(screen.getByRole('button', { name: 'Add rule' }));
    await view.user.type(screen.getByRole('textbox', { name: 'Domain pattern' }), '*.example.com');
    await view.user.click(screen.getByRole('radio', { name: 'Clear tags' }));
    await view.user.click(screen.getByRole('button', { name: 'Save rule' }));
    await view.user.click(await screen.findByRole('button', { name: 'Retry' }));
    await screen.findByText('*.example.com');
    expect(writes).toHaveLength(2);
    expect(writes[1]).toEqual(writes[0]);
  });
});
