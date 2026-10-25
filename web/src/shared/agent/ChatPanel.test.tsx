import { act, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { ApiError } from '@shared/query/http';
import type { AgentMessage, AgentPlan, Conversation } from './api';
import { ChatPanel } from './ChatPanel';
import { OperationPlan } from './OperationPlan';

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
    });
    const view = await mount();
    const card = screen.getByRole('region', { name: 'Operation plan' });
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
