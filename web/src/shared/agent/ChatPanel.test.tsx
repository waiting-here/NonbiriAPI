import { act, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { ApiError } from '@shared/query/http';
import type { AgentJob, AgentMessage, AgentPlan, Conversation } from './api';
import { ChatPanel } from './ChatPanel';
import { OperationPlan } from './OperationPlan';
import { planMessageID } from './toolActivityData';
import * as activityData from './toolActivityData';
import { ToolActivity } from './ToolActivity';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  send: vi.fn(),
  edit: vi.fn(),
  clear: vi.fn(),
  decide: vi.fn(),
  older: vi.fn(),
  plans: vi.fn(),
  elevate: vi.fn(),
}));
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  getConversation: mocks.get,
  sendMessage: mocks.send,
  editMessage: mocks.edit,
  clearConversation: mocks.clear,
  decidePlan: mocks.decide,
  getMessages: mocks.older,
  getPlans: mocks.plans,
}));
vi.mock('@shared/operations/api', async (original) => ({
  ...(await original<typeof import('@shared/operations/api')>()),
  elevateAdmin: mocks.elevate,
}));
const message = (id: string, role: string, content: string): AgentMessage => ({
  id,
  role,
  content,
  created_at: 1700000000000,
  active: true,
  generation: '1',
});
const conversation = (messages: AgentMessage[] = []): Conversation => ({
  enabled: true,
  id: '7',
  generation: '1',
  messages,
  plans: [],
  summary: '',
  has_older: false,
});
const plan: AgentPlan = {
  id: '41',
  job_id: '9',
  state: 'pending',
  tool_call_ids: ['call1'],
  created_at: 1700000000000,
  operations: [
    { kind: 'memory.edit', target_id: 'chat', input: { content: 'new', expected_revision: '1' } },
    { kind: 'users.profile', target_id: '5', input: { rpm_limit: '20', expected_revision: '1' } },
  ],
  preview: [
    {
      title: 'Update memory',
      description: '',
      before: 'Keep\nold',
      after: 'Keep\nnew',
      dangerous: false,
      requires_elevation: false,
    },
    {
      title: 'Update limits',
      description: '',
      before: '10',
      after: '20',
      dangerous: false,
      requires_elevation: false,
    },
  ],
};
const job: AgentJob = {
  id: '9',
  kind: 'chat',
  actor_id: '0',
  actor_role: 'admin',
  actor_label: 'Operator',
  conversation_id: '7',
  generation: '1',
  donation_id: '0',
  state: 'waiting_confirmation',
  request_id: '0',
  error: '',
  created_at: 1700000000000,
  updated_at: 1700000000000,
  queue: { length: 0, active: 0, limit: 1, position: 0 },
};
beforeEach(() => {
  vi.clearAllMocks();
  mocks.get.mockResolvedValue(conversation());
  mocks.plans.mockResolvedValue({ items: [], has_older: false });
  mocks.decide.mockResolvedValue({ ...plan, state: 'approved' });
});
async function mount(role: 'admin' | 'steward' = 'admin') {
  const view = await renderWithProviders(
    <ChatPanel role={role} account="synthetic-operator" active />,
    role === 'admin' ? { station: 'admin', role: 'admin' } : { station: 'user', role: 'level5' },
  );
  view.queryClient.setQueryData(
    role === 'admin' ? ['admin', 'session'] : ['user', 'session'],
    role === 'admin'
      ? { admin: { username: 'synthetic-operator' } }
      : { user: { id: '5', username: 'synthetic-operator', effective_level: 5 } },
  );
  await screen.findByRole('textbox', { name: 'Message' });
  return view;
}
describe('Agent conversation and plans', () => {
  it('keeps loaded history indexes stable while composing and refreshes them for a delivered message', async () => {
    const history = Array.from({ length: 100 }, (_, index) => {
      const id = index * 3 + 1;
      return [
        message(String(id), 'user', 'Question ' + index),
        {
          ...message(String(id + 1), 'assistant', ''),
          tool_calls: [{ id: 'read-' + index, function: { name: 'donations', arguments: '{}' } }],
        },
        {
          ...message(
            String(id + 2),
            'tool',
            JSON.stringify({
              items: [{ description: 'Result ' + index, detail: 'x'.repeat(1024) }],
            }),
          ),
          tool_call_id: 'read-' + index,
        },
      ];
    }).flat();
    const plans = Array.from({ length: 30 }, (_, index) => ({
      ...plan,
      id: String(index + 1000),
      state: 'approved',
      tool_call_ids: ['read-' + index],
      operations: [],
      preview: [],
    }));
    const current = {
      ...conversation(history.slice(200)),
      has_older: true,
      job: { ...job, state: 'completed' },
    };
    mocks.get.mockResolvedValue(current);
    mocks.older
      .mockResolvedValueOnce(history.slice(100, 200))
      .mockResolvedValueOnce(history.slice(0, 100));
    mocks.plans
      .mockResolvedValueOnce({ items: plans, has_older: false })
      .mockResolvedValue({ items: [], has_older: false });
    const indexes = vi.spyOn(activityData, 'indexToolResults');
    const calls = vi.spyOn(activityData, 'readToolCalls');
    try {
      const view = await mount();
      await view.user.click(screen.getByRole('button', { name: 'Load older messages' }));
      await waitFor(() => expect(mocks.older).toHaveBeenCalledTimes(1));
      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Load older messages' })).toBeEnabled(),
      );
      await view.user.click(screen.getByRole('button', { name: 'Load older messages' }));
      await screen.findByText('Question 0');
      expect(indexes.mock.calls.at(-1)![0]).toHaveLength(300);
      const indexCount = indexes.mock.calls.length;
      const callCount = calls.mock.calls.length;
      await view.user.type(screen.getByRole('textbox', { name: 'Message' }), 'Keep composing now');
      expect(indexes).toHaveBeenCalledTimes(indexCount);
      expect(calls).toHaveBeenCalledTimes(callCount);
      expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue('Keep composing now');
      await act(async () => {
        view.queryClient.setQueryData(['admin', 'ai', 'synthetic-operator', 'chat'], {
          ...current,
          messages: [...current.messages, message('301', 'assistant', 'Live reply arrived')],
        });
      });
      await screen.findByText('Live reply arrived');
      expect(indexes).toHaveBeenCalledTimes(indexCount + 1);
      expect(indexes.mock.calls.at(-1)![0]).toHaveLength(301);
      expect(screen.getByText('Question 0')).toBeVisible();
      expect(mocks.send).not.toHaveBeenCalled();
    } finally {
      indexes.mockRestore();
      calls.mockRestore();
    }
  });
  it('parses tool payloads once across state renders and displays an updated result', async () => {
    const input = JSON.stringify({ query: 'Stable synthetic input' });
    const output = JSON.stringify({
      items: [{ description: 'Synthetic tool result', detail: 'x'.repeat(8192) }],
    });
    const updated = JSON.stringify({ items: [{}, {}] });
    const assistant = {
      ...message('1', 'assistant', ''),
      tool_calls: [{ id: 'read-stable', function: { name: 'donations', arguments: input } }],
    };
    const response = { ...message('2', 'tool', output), tool_call_id: 'read-stable' };
    const results = new Map([[0, response]]);
    const parse = vi.spyOn(JSON, 'parse');
    const count = (value: string) => parse.mock.calls.filter(([text]) => text === value).length;
    try {
      const view = await renderWithProviders(
        <ToolActivity message={assistant} results={results} />,
        { station: 'admin', role: 'admin' },
      );
      expect(screen.getByText('Stable synthetic input · Items returned: 1')).toBeVisible();
      expect(count(input)).toBe(1);
      expect(count(output)).toBe(1);
      view.rerender(<ToolActivity message={assistant} results={results} running />);
      expect(count(input)).toBe(1);
      expect(count(output)).toBe(1);
      view.rerender(
        <ToolActivity
          message={assistant}
          results={new Map([[0, { ...response, content: updated }]])}
        />,
      );
      expect(screen.getByText('Stable synthetic input · Items returned: 2')).toBeVisible();
      expect(count(output)).toBe(1);
      expect(count(updated)).toBe(1);
      expect(screen.queryByText(/Synthetic tool result/)).not.toBeInTheDocument();
    } finally {
      parse.mockRestore();
    }
  });
  it('presents compact as a command and uses a distinct submit action', async () => {
    mocks.get.mockResolvedValue(
      conversation([message('1', 'user', '/compact keep pending plans')]),
    );
    const view = await mount();
    expect(screen.getByText('Context command')).toBeInTheDocument();
    expect(screen.getByText('keep pending plans')).toBeInTheDocument();
    await view.user.type(screen.getByRole('textbox', { name: 'Message' }), '/compact keep IDs');
    await view.user.click(screen.getByRole('button', { name: 'Compact context' }));
    expect(mocks.send).toHaveBeenCalledWith(
      'admin',
      expect.objectContaining({ content: '/compact keep IDs' }),
      expect.any(AbortSignal),
    );
  });
  it('recognizes edited user commands without treating assistant prose as a command', async () => {
    mocks.get.mockResolvedValue(conversation([message('1', 'assistant', '/compact')]));
    const view = await mount();
    expect(screen.queryByText('Context command')).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Edit and regenerate' }));
    expect(screen.getByRole('button', { name: 'Regenerate' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Compact context' })).not.toBeInTheDocument();
  });
  it('only attaches plans to a complete call set in the known job', () => {
    const later = {
      ...message('2', 'assistant', ''),
      created_at: plan.created_at + 1,
      tool_calls: [{ id: 'call1' }],
    };
    const current = { ...later, created_at: plan.created_at };
    expect(planMessageID(plan, [current], job)).toBe('2');
    expect(planMessageID(plan, [current])).toBeUndefined();
    expect(planMessageID(plan, [current], { ...job, id: '10' })).toBeUndefined();
    expect(planMessageID(plan, [{ ...current, generation: '0' }], job)).toBeUndefined();
    expect(
      planMessageID(plan, [{ ...current, created_at: job.created_at - 1 }], job),
    ).toBeUndefined();
    expect(planMessageID(plan, [later], job)).toBeUndefined();
    expect(
      planMessageID({ ...plan, tool_call_ids: ['call1', 'call2'] }, [current], job),
    ).toBeUndefined();
  });
  it('retains unknown call fields in its expanded detail', async () => {
    mocks.get.mockResolvedValue(
      conversation([
        {
          ...message('1', 'assistant', ''),
          tool_calls: [
            { function: { arguments: 'raw arguments' }, extra: 'retained unknown detail' },
          ],
        },
      ]),
    );
    const view = await mount();
    expect(screen.queryByText(/retained unknown detail/)).not.toBeInTheDocument();
    await view.user.click(screen.getByText('Tool calls'));
    expect(screen.getByText(/retained unknown detail/)).toBeInTheDocument();
  });
  it('summarizes a tool and reveals its paired full result only on demand', async () => {
    mocks.get.mockResolvedValue(
      conversation([
        {
          ...message('1', 'assistant', ''),
          tool_calls: [
            { id: 'read-1', type: 'function', function: { name: 'donations', arguments: '{}' } },
          ],
        },
        {
          ...message('2', 'tool', '{"items":[{"description":"synthetic-donation-result"}]}'),
          tool_call_id: 'read-1',
        },
      ]),
    );
    const view = await mount();
    expect(screen.getByText('Read donations')).toBeInTheDocument();
    expect(screen.getByText('Items returned: 1')).toBeInTheDocument();
    expect(screen.getByText('Result received')).toBeInTheDocument();
    expect(screen.queryByText(/synthetic-donation-result/)).not.toBeInTheDocument();
    await view.user.click(screen.getByText('Read donations'));
    expect(screen.getAllByText(/synthetic-donation-result/)).toHaveLength(1);
  });
  it.each([
    { error: 'Read failed. Narrow the selection.', detail: 'Read failed. Narrow the selection.' },
    {
      error: {
        code: 'tool_result_too_large',
        message: 'Result exceeds the available budget.',
        retry: 'Retry with a specific user_id.',
        retryable: true,
      },
      detail: 'Result exceeds the available budget. Retry with a specific user_id.',
    },
  ])(
    'shows a tool failure and recovery hint before expanding its result: $detail',
    async ({ error, detail }) => {
      mocks.get.mockResolvedValue(
        conversation([
          {
            ...message('1', 'assistant', ''),
            tool_calls: [{ id: 'read-limit', function: { name: 'users', arguments: '{}' } }],
          },
          {
            ...message('2', 'tool', JSON.stringify({ error })),
            tool_call_id: 'read-limit',
          },
        ]),
      );
      await mount();
      expect(screen.getByText('Failed')).toBeInTheDocument();
      expect(screen.getByText(detail)).toBeInTheDocument();
      expect(screen.queryByText('Result received')).not.toBeInTheDocument();
      expect(screen.queryByText('[object Object]')).not.toBeInTheDocument();
    },
  );
  it('shows the actual interruption instead of a successful tool result', async () => {
    mocks.get.mockResolvedValue(
      conversation([
        {
          ...message('1', 'assistant', ''),
          tool_calls: [
            { id: 'write-1', function: { name: 'propose_operations', arguments: '{}' } },
          ],
        },
        {
          ...message(
            '2',
            'tool',
            '{"status":"interrupted","message":"Work was interrupted; nothing was applied."}',
          ),
          tool_call_id: 'write-1',
        },
      ]),
    );
    await mount();
    expect(screen.getByText('Interrupted')).toBeInTheDocument();
    expect(screen.getByText('Work was interrupted; nothing was applied.')).toBeInTheDocument();
    expect(screen.queryByText('Result received')).not.toBeInTheDocument();
  });
  it('keeps history readable while the server says chat is unavailable', async () => {
    mocks.get.mockResolvedValue({
      ...conversation([message('1', 'user', 'Retained request')]),
      enabled: false,
      plans: [plan],
    });
    await mount();
    expect(screen.getByText('Retained request')).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Message' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Edit and regenerate' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Approve and run' })).not.toBeInTheDocument();
    expect(screen.getByText(/Chat is not available\./)).toBeInTheDocument();
  });
  it.each(['clear', 'edit'] as const)(
    'does not append late older messages or plans after %s',
    async (action) => {
      let current = { ...conversation([message('3', 'user', 'Latest request')]), has_older: true };
      let finish!: (value: AgentMessage[]) => void;
      mocks.get.mockImplementation(async () => current);
      mocks.older.mockReturnValue(
        new Promise((resolve) => {
          finish = resolve;
        }),
      );
      mocks.plans.mockResolvedValue({ items: [plan], has_older: false });
      const advance = async () => {
        current = { ...conversation(), generation: '2', has_older: false };
        return {};
      };
      mocks.clear.mockImplementation(advance);
      mocks.edit.mockImplementation(advance);
      const view = await mount();
      await view.user.click(screen.getByRole('button', { name: 'Load older messages' }));
      await waitFor(() => expect(mocks.older).toHaveBeenCalledTimes(1));
      if (action === 'clear') {
        await view.user.click(screen.getByRole('button', { name: 'Clear current chat' }));
        await view.user.click(
          within(await screen.findByRole('alertdialog')).getByRole('button', {
            name: 'Clear current chat',
          }),
        );
      } else {
        await view.user.click(screen.getByRole('button', { name: 'Edit and regenerate' }));
        await view.user.click(screen.getByRole('button', { name: 'Regenerate' }));
      }
      await waitFor(() => expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue(''));
      await act(async () =>
        finish([
          { ...message('1', 'assistant', 'Stale older reply'), tool_calls: [{ id: 'call1' }] },
        ]),
      );
      expect(screen.queryByText('Stale older reply')).not.toBeInTheDocument();
      expect(screen.queryByRole('region', { name: 'Operation plan' })).not.toBeInTheDocument();
    },
  );
  it.each([
    ['approved', 'Approved'],
    ['superseded', 'Invalidated'],
  ])('shows the backend plan state %s', async (state, label) => {
    await renderWithProviders(
      <OperationPlan role="admin" account="synthetic" plan={{ ...plan, state }} />,
      { station: 'admin', role: 'admin' },
    );
    expect(screen.getByText(label)).toBeInTheDocument();
    expect(screen.queryByText('Status unavailable')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Approve and run' })).not.toBeInTheDocument();
  });
  it('summarizes targets, changed limits, credits and binding APIs while folding raw snapshots', async () => {
    const operations = [
      {
        kind: 'users.profile',
        target_id: '5',
        input: { expected_revision: '2', rpm_limit: '2000' },
      },
      {
        kind: 'users.economy',
        target_id: '5',
        input: {
          expected_revision: '2',
          target: 'balance',
          direction: 'increase',
          amount: '1234.5',
          reason: 'Adjustment',
        },
      },
      {
        kind: 'charity.binding.add',
        target_id: '9',
        input: {
          expected_binding_revision: '1',
          targets: [
            {
              donation_key_id: '7',
              upstream_model_id: '8',
              native_services: ['chat_completions', 'responses'],
            },
          ],
        },
      },
    ];
    await renderWithProviders(
      <OperationPlan
        role="admin"
        account="synthetic"
        plan={{
          ...plan,
          operations,
          preview: operations.map((op, index) => ({
            title: op.kind,
            description: 'Technical description',
            dangerous: false,
            requires_elevation: false,
            before: index === 0 ? '{"rpm_limit":"1000"}' : '{}',
            after: JSON.stringify(op.input),
          })),
        }}
      />,
      { station: 'admin', role: 'admin' },
    );
    const card = screen.getByRole('region', { name: 'Operation plan' });
    const rows = within(card).getAllByRole('listitem');
    expect(rows[0]).toHaveTextContent('User ID: 5');
    expect(rows[0]).toHaveTextContent('Requests per minute: 1,000 → 2,000');
    expect(rows[1]).toHaveTextContent('Increase');
    expect(rows[1]).toHaveTextContent('1,234.5 credits');
    expect(rows[2]).toHaveTextContent('Charity model ID: 9');
    expect(rows[2]).toHaveTextContent('Donation key ID: 7');
    expect(rows[2]).toHaveTextContent('Chat completions');
    expect(rows[2]).toHaveTextContent('Responses');
    expect(card).not.toHaveTextContent('expected_revision');
    expect(card).not.toHaveTextContent('Technical description');
    expect(mocks.decide).not.toHaveBeenCalled();
  });
  it('opens identity confirmation when the server requires elevated authority', async () => {
    mocks.decide.mockRejectedValueOnce(
      new ApiError('elevated_required', 'Fresh identity confirmation required', 403),
    );
    const view = await renderWithProviders(
      <OperationPlan role="admin" account="synthetic" plan={plan} />,
      { station: 'admin', role: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'synthetic' } });
    await view.user.click(screen.getByRole('button', { name: 'Approve and run' }));
    await screen.findByRole('alert');
    await view.user.click(screen.getByRole('button', { name: 'Approve and run' }));
    expect(
      within(await screen.findByRole('alertdialog')).getByLabelText('Administrator password'),
    ).toBeInTheDocument();
  });
  it('keeps the draft and original request key until an uncertain send is confirmed', async () => {
    let attempts = 0;
    mocks.send.mockImplementation(async () => {
      if (++attempts < 3) throw new ApiError('network_error', 'Response lost', 0);
      return {};
    });
    const view = await mount();
    await view.user.type(
      screen.getByRole('textbox', { name: 'Message' }),
      'Inspect the pending donation',
    );
    await view.user.click(screen.getByRole('button', { name: 'Send' }));
    const check = await screen.findByRole('button', { name: 'Check saved result' });
    expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue(
      'Inspect the pending donation',
    );
    await view.user.click(check);
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue(''));
    expect(mocks.send).toHaveBeenCalledTimes(3);
    expect(mocks.send.mock.calls[0]![1]).toEqual(mocks.send.mock.calls[2]![1]);
    expect(mocks.send.mock.calls[0]![1].request_key).toBeTruthy();
  });
  it('offers stewards only the latest user message and preserves edited text after conflict', async () => {
    mocks.get.mockResolvedValue(
      conversation([
        message('1', 'user', 'Old request'),
        message('2', 'assistant', 'Old answer'),
        message('3', 'user', 'Latest request'),
        message('4', 'assistant', 'Latest answer'),
      ]),
    );
    mocks.edit.mockRejectedValue(new ApiError('conflict', 'Context changed', 409));
    const view = await mount('steward');
    expect(screen.getAllByRole('button', { name: 'Edit and regenerate' })).toHaveLength(1);
    await view.user.click(screen.getByRole('button', { name: 'Edit and regenerate' }));
    await view.user.type(screen.getByRole('textbox', { name: 'Message' }), ' revised');
    await view.user.click(screen.getByRole('button', { name: 'Regenerate' }));
    await screen.findByText('Context changed');
    expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue('Latest request revised');
    expect(mocks.edit.mock.calls[0]![1]).toBe('3');
  });
  it('places an ordered plan at its assistant tool call and waits for an explicit decision', async () => {
    mocks.get.mockResolvedValue({
      ...conversation([
        {
          ...message('2', 'assistant', 'I propose these changes'),
          tool_calls: [
            {
              id: 'call1',
              type: 'function',
              function: { name: 'propose_operations', arguments: '{}' },
            },
          ],
        },
      ]),
      plans: [plan],
      job,
    });
    const view = await mount();
    const card = screen.getByRole('region', { name: 'Operation plan' });
    expect(screen.getByRole('log')).toContainElement(card);
    expect(within(card).getByText('Awaiting your decision')).toBeInTheDocument();
    expect(
      within(card)
        .getAllByRole('listitem')
        .map((row) => row.querySelector('strong')?.textContent),
    ).toEqual(['Update memory', 'Update user profile and limits']);
    expect(card.querySelector('.agent-diff__remove')).toHaveTextContent('old');
    expect(card.querySelector('.agent-diff__add')).toHaveTextContent('new');
    expect(mocks.decide).not.toHaveBeenCalled();
    await view.user.click(within(card).getByRole('button', { name: 'Reject plan' }));
    await waitFor(() => expect(mocks.decide).toHaveBeenCalledTimes(1));
    expect(mocks.decide.mock.calls[0]![2]).toEqual({ approve: false, feedback: '' });
  });
  it('does not execute a sensitive plan after the administrator session changes during elevation', async () => {
    let finish!: (value: { token: string; expires_at: number }) => void;
    mocks.elevate.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    const sensitive = {
      ...plan,
      preview: [
        { ...plan.preview[0]!, dangerous: true, requires_elevation: true },
        plan.preview[1]!,
      ],
    };
    const view = await renderWithProviders(
      <OperationPlan role="admin" account="synthetic-operator" plan={sensitive} />,
      { station: 'admin', role: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], {
      admin: { username: 'synthetic-operator' },
    });
    await view.user.click(screen.getByRole('button', { name: 'Approve and run' }));
    const dialog = await screen.findByRole('alertdialog');
    await view.user.type(
      within(dialog).getByLabelText('Administrator password'),
      'synthetic-password',
    );
    await view.user.click(within(dialog).getByRole('button', { name: 'Approve and run' }));
    view.queryClient.setQueryData(['admin', 'session'], {
      admin: { username: 'different-operator' },
    });
    finish({ token: 'synthetic-elevation', expires_at: 2000000000 });
    await waitFor(() => expect(mocks.elevate).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeEnabled(),
    );
    expect(mocks.decide).not.toHaveBeenCalled();
  });
});
