import { describe, expect, it, vi } from 'vitest';
import {
  getConversation,
  getDriver,
  getReviews,
  getRequests,
  getPlans,
  getArchivePlans,
  putSettings,
} from './api';
const job = {
  id: '9007199254740993',
  kind: 'chat',
  actor_id: '5',
  actor_role: 'level5',
  actor_label: 'Synthetic operator',
  conversation_id: '7',
  generation: '2',
  donation_id: '0',
  state: 'waiting_confirmation',
  request_id: '8',
  error: '',
  created_at: 1700000000000,
  updated_at: 1700000000001,
  queue: { length: 3, active: 1, limit: 1, position: 2 },
};
const plan = {
  id: '41',
  job_id: job.id,
  state: 'approved',
  operations: [],
  preview: [],
  tool_call_ids: ['call1'],
  result: { status: 'completed' },
  created_at: 1700000000000,
  decided_at: 1700000000001,
};
const settings = {
  chat_enabled: false,
  revision: '2',
  mode: 'preview',
  auto_template: [{ role: 'user', content: '{{intelligence}} {{memory}}' }],
  chat_template: [
    { role: 'system', content: '{{memory}}' },
    { role: 'user', content: '{{conversation}}' },
  ],
  auto_prefill: '',
  chat_prefill: '',
  tools: { donations: true },
  tool_descriptions: { donations: 'Read the current donation and report missing facts.' },
  search_url: 'https://search.example/query',
  search_has_key: true,
};
function respond(value: unknown) {
  const network = vi.fn<typeof fetch>(
    async () =>
      new Response(JSON.stringify(value), { headers: { 'content-type': 'application/json' } }),
  );
  vi.stubGlobal('fetch', network);
  return network;
}
describe('Agent HTTP projections', () => {
  it.each([
    { path: '/admin/api/ai/chat/plans', read: (before: string) => getPlans('admin', before) },
    { path: '/api/steward/ai/chat/plans', read: (before: string) => getPlans('steward', before) },
    {
      path: '/admin/api/ai/archives/5/plans',
      read: (before: string) => getArchivePlans('5', before),
    },
  ])(
    'omits initial plan cursor and preserves exact older cursor for $path',
    async ({ path, read }) => {
      for (const before of ['', '0', '9007199254740993']) {
        const network = respond({ items: [], has_older: false });
        await read(before);
        expect(network).toHaveBeenCalledExactlyOnceWith(
          path + (before && before !== '0' ? '?before_id=9007199254740993' : ''),
          expect.objectContaining({ credentials: 'same-origin' }),
        );
      }
    },
  );
  it('preserves queue, tool call linkage and execution receipt while excluding unconsumed fields', async () => {
    respond({
      enabled: true,
      id: '7',
      generation: '2',
      messages: [
        {
          id: '4',
          role: 'assistant',
          content: 'Proposed changes',
          tool_calls: [{ id: 'call1' }],
          created_at: 1700000000000,
          active: true,
          generation: '2',
        },
      ],
      summary: '',
      job,
      plans: [plan],
      has_older: false,
      unused_server_field: 'ignored',
    });
    const value = await getConversation('steward');
    expect(value.job?.queue).toEqual(job.queue);
    expect(value.job?.id).toBe('9007199254740993');
    expect(value.messages[0]?.tool_calls).toEqual([{ id: 'call1' }]);
    expect(value.plans[0]).toMatchObject({
      tool_call_ids: ['call1'],
      result: { status: 'completed' },
      decided_at: 1700000000001,
    });
    expect(value).not.toHaveProperty('unused_server_field');
  });
  it('keeps complete configuration after save, including optional sampling parameters', async () => {
    respond(settings);
    const { revision, search_has_key, ...body } = settings;
    expect(search_has_key).toBe(true);
    const saved = await putSettings({
      ...body,
      mode: 'preview',
      expected_revision: revision,
    } as Parameters<typeof putSettings>[0]);
    expect(saved.chat_template).toEqual(settings.chat_template);
    expect(saved.tools).toEqual(settings.tools);
    expect(saved.tool_descriptions).toEqual(settings.tool_descriptions);
    expect(saved.search_has_key).toBe(true);
    respond({
      revision: '1',
      base_url: 'https://driver.example/v1',
      has_key: true,
      model: 'synthetic',
      context_window: 32768,
      max_output_tokens: 4096,
      extra_parameters: {},
      concurrency: 1,
      timeout_seconds: 600,
      output_token_parameter: 'max_tokens',
      temperature: 0.2,
      top_p: 0.8,
    });
    expect(await getDriver()).toMatchObject({ temperature: 0.2, top_p: 0.8, has_key: true });
  });
  it('reads nested review records rather than treating them as bare jobs', async () => {
    respond({
      items: [
        {
          job,
          intelligence: {},
          result: {
            decision: 'manual',
            internal_reason: 'No suitable model',
            user_note: '',
            bindings: [],
          },
        },
      ],
      total: '1',
      page: 1,
      page_size: 20,
    });
    const page = await getReviews('steward', '1', 20);
    expect(page.items[0]?.job.id).toBe(job.id);
    expect(page.items[0]?.result?.decision).toBe('manual');
  });
  it('retains the fixed cleanup cutoff and exact token totals from request pages', async () => {
    const usage = {
      input_tokens: '9007199254740993',
      cached_input_tokens: '0',
      cache_write_input_tokens: '0',
      output_tokens: '2',
      reasoning_tokens: '0',
      unknown: true,
    };
    respond({
      items: [
        {
          id: '9',
          client_request_id: 'synthetic',
          actor_id: '5',
          actor_role: 'level5',
          actor_label: 'Synthetic operator',
          source: 'steward_chat',
          kind: 'model',
          state: 'succeeded',
          created_at: 1700000000000,
          usage,
          response_complete: true,
          base_url: 'https://driver.example/v1',
          model: 'synthetic',
          status_code: 200,
          error_code: '',
          content_redacted: false,
        },
      ],
      total: '1',
      page: 1,
      page_size: 20,
      cutoff_id: '9007199254740997',
    });
    const value = await getRequests({ source: '', kind: '', state: '', actor_id: '' }, '1', 20);
    expect(value.cutoff_id).toBe('9007199254740997');
    expect(value.items[0]?.usage.input_tokens).toBe('9007199254740993');
    expect(value.items[0]?.actor_label).toBe('Synthetic operator');
  });
});
