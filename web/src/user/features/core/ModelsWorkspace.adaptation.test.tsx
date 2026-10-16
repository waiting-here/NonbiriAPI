import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { ModelsWorkspace } from './ModelsWorkspace';
import { coreKeys } from './queries';
import type { UserProfile } from './types';

const user = JSON.parse(
  readFileSync(resolve(process.cwd(), '..', 'internal/auth/testdata/user_envelope.json'), 'utf8'),
).user as UserProfile;
const model = JSON.parse(
  readFileSync(
    resolve(process.cwd(), '..', 'internal/resources/testdata/manual_update.json'),
    'utf8',
  ),
).affected_models[0].model;
const page = {
  data: [],
  pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
};

describe('personal model request adaptation', () => {
  it.each([false, true])(
    'loads once on expansion and derives Gateway controls from all bindings (mixed=%s)',
    async (mixed) => {
      const reads: string[] = [];
      const writes: Record<string, unknown>[] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
          const url = new URL(
            input instanceof Request ? input.url : String(input),
            window.location.origin,
          );
          const json = (value: unknown) =>
            new Response(JSON.stringify(value), {
              headers: { 'Content-Type': 'application/json' },
            });
          if (init?.method !== 'PUT') reads.push(url.pathname);
          if (url.pathname.endsWith('/request-adaptation')) {
            if (init?.method === 'PUT')
              writes.push(JSON.parse(String(init.body)) as Record<string, unknown>);
            return json({
              revision: '1',
              forward_headers: { mode: 'replace', values: [] },
              fixed_headers: { mode: 'replace', values: {} },
              body_defaults: { mode: 'replace', values: {} },
              body_forced: { mode: 'replace', values: {} },
              native_extension_paths: { mode: 'replace', values: [] },
              excluded_request_fields: [],
            });
          }
          if (url.pathname === '/api/models/' + model.id) return json(model);
          if (url.pathname.endsWith('/bindings'))
            return json({
              bindings: ['1', '2'].map((id) => ({
                id,
                endpoint_key_id: id,
                endpoint_base_url: 'https://example.test',
                connector_type: mixed && id === '2' ? 'openai-compatible' : 'ai-sdk-gateway-v3',
                endpoint_note: '',
                endpoint_key_display_head: '',
                endpoint_key_display_tail: '',
                endpoint_key_note: '',
                upstream_model_id: 'test-model',
                ord: Number(id) - 1,
              })),
              binding_revision: model.binding_revision,
            });
          if (url.pathname.endsWith('/binding-candidates'))
            return json({ ...page, pagination: { ...page.pagination, page_size: 10 } });
          if (url.pathname === '/api/models' || url.pathname === '/api/endpoints')
            return json(page);
          throw new Error('Unexpected request: ' + url.pathname);
        }),
      );
      const view = await renderWithProviders(<ModelsWorkspace user={user} />, {
        station: 'user',
        route: '/models?model_id=' + model.id,
      });
      view.queryClient.setQueryData(coreKeys.session, { user });
      await screen.findByRole('button', { name: 'Edit model' });
      await waitFor(() =>
        expect(reads.filter((path) => path.endsWith('/bindings'))).toHaveLength(1),
      );
      expect(reads.some((path) => path.endsWith('/request-adaptation'))).toBe(false);
      const advanced = screen.getByText('Advanced behavior', { selector: 'strong' });
      await view.user.click(advanced);
      await screen.findByRole('button', { name: 'Save request rewriting' });
      if (mixed) {
        expect(screen.queryByLabelText('Automatic cache default')).not.toBeInTheDocument();
      } else {
        const cache = await screen.findByLabelText('Automatic cache default');
        await view.user.selectOptions(cache, '1h');
        await view.user.click(advanced);
        await view.user.click(advanced);
        expect(cache).toHaveValue('1h');
        await view.user.click(screen.getByRole('button', { name: 'Save request rewriting' }));
        await waitFor(() => expect(writes).toHaveLength(1));
        expect(writes[0].body_defaults).toEqual({
          mode: 'replace',
          values: {
            '/cache_control': { action: 'replace', value: { type: 'ephemeral', ttl: '1h' } },
          },
        });
      }
      expect(reads.filter((path) => path.endsWith('/request-adaptation'))).toHaveLength(1);
      expect(reads.filter((path) => path.endsWith('/bindings'))).toHaveLength(1);
    },
  );
});
