import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { screen, waitFor } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { HomeDashboard } from './HomePage';
import { coreKeys } from '../features/core/queries';
import { normalizeUserEnvelope } from '../features/core/normalizers';
import type { HomeAdapters } from '../features/core/types';

const profile = normalizeUserEnvelope(
  JSON.parse(
    readFileSync(resolve(process.cwd(), '../internal/auth/testdata/user_envelope.json'), 'utf8'),
  ),
);
const unavailable: HomeAdapters = {
  checkin: { state: 'unavailable' },
  gameCheckin: { state: 'unavailable' },
  games: { state: 'unavailable' },
  announcements: { state: 'unavailable' },
};

describe('home onboarding requests', () => {
  it.each(['existing', 'empty', 'failed'] as const)(
    'reads charity models only when the personal model result is %s',
    async (result) => {
      const calls: string[] = [];
      const model = {
        id: '31',
        provider: 'fixture',
        model: 'fixture-model',
        full_name: 'fixture/fixture-model',
        model_types: ['chat_completions'],
        route_strategy: 'ordered',
        silent_retry: false,
        transport_rule: 'passthrough',
        flatten_tool_calls: false,
        revision: '1',
        binding_revision: '1',
        binding_count: '1',
        created_at: 1_700_000_000,
        updated_at: 1_700_000_000,
      };
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: unknown) => {
          const path = new URL(String(input), 'http://user.test').pathname;
          calls.push(path);
          let body: unknown;
          let status = 200;
          if (path === '/api/caller-key') body = null;
          else if (path === '/api/models') {
            body =
              result === 'failed'
                ? { error: { code: 'internal_error', message: 'Try again' } }
                : {
                    data: result === 'existing' ? [model] : [],
                    next_cursor: null,
                    pagination: {
                      page: '1',
                      page_size: 10,
                      total_items: result === 'existing' ? '1' : '0',
                      total_pages: '1',
                    },
                  };
            if (result === 'failed') status = 500;
          } else if (path === '/api/charity/models')
            body = {
              models: [],
              donation_intake: 'closed',
              server_now: 1_800_000_000,
              pagination: { page: '1', page_size: 10, total_items: '0', total_pages: '1' },
            };
          else throw new Error('Unexpected request: ' + path);
          return new Response(JSON.stringify(body), {
            status,
            headers: { 'Content-Type': 'application/json', 'X-Caller-Key-Generation': '0' },
          });
        }),
      );
      const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      client.setQueryData(coreKeys.session, profile);
      const rendered = await renderWithProviders(
        <QueryClientProvider client={client}>
          <HomeDashboard user={profile.user} adapters={unavailable} />
        </QueryClientProvider>,
        { station: 'user', role: 'user' },
      );
      await screen.findByRole('heading', { name: 'Get started' });
      await waitFor(() => expect(calls.filter((path) => path === '/api/models')).toHaveLength(1));
      if (result !== 'existing')
        await waitFor(() => expect(calls).toContain('/api/charity/models'));
      else
        await waitFor(() =>
          expect(document.querySelectorAll('.home-checklist li')[1]).toHaveAttribute(
            'data-done',
            'true',
          ),
        );
      expect(calls.filter((path) => path === '/api/charity/models')).toHaveLength(
        result === 'existing' ? 0 : 1,
      );
      expect(calls).not.toContain('/api/me');
      rendered.unmount();
      client.clear();
    },
  );
});
