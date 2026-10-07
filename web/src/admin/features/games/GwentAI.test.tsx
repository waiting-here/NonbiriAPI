import { screen, fireEvent, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { GwentAIAdmin } from './GwentAI';

it('saves an original faction preset and independent challenge terms without renewing the challenge', async () => {
  const policy = {
    id: 'aip_test',
    name: 'OpenAI',
    description: 'Local scorer',
    enabled: true,
    revision: '1',
    version: 1,
    definition: { schema: 'gwent-local/v1', faction: 'openai', preset: 'standard-balanced' },
  };
  const bot = {
    id: 'bot_test',
    name: 'OpenAI challenge',
    description: 'Original preset',
    enabled: false,
    revision: '1',
    policy_id: policy.id,
    policy_version: 1,
    challenge_id: 'aic_stable',
    ticket: '0',
    first_reward: '0',
    memory_days: 30,
    memory_games: 30,
  };
  const state = { settings: { enabled: false, revision: '1' }, policies: [policy], bots: [bot] };
  const writes: { url: string; body: Record<string, unknown> }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url, options) => {
      const address = String(url);
      if (options?.method === 'POST') {
        const body = JSON.parse(options.body) as Record<string, unknown>;
        writes.push({ url: address, body });
        if (address.endsWith('/policies')) {
          state.policies[0] = { ...policy, version: 2, revision: '2' };
          return new Response(JSON.stringify(state.policies[0]), {
            headers: { 'Content-Type': 'application/json' },
          });
        }
        state.bots[0] = {
          ...bot,
          policy_version: 2,
          ticket: '5',
          first_reward: '2',
          revision: '2',
        };
        return new Response(JSON.stringify(state.bots[0]), {
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response(JSON.stringify(state), {
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
  const { user, queryClient } = await renderWithProviders(<GwentAIAdmin />, { station: 'admin' });
  queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
  await user.click(await screen.findByRole('button', { name: 'Edit challenge' }));
  const selector = screen.getByRole('combobox', { name: 'Original deck preset' });
  expect(selector.querySelectorAll('option')).toHaveLength(4);
  fireEvent.change(selector, { target: { value: 'standard-control' } });
  fireEvent.change(screen.getByRole('textbox', { name: 'Ticket · credits' }), {
    target: { value: '5' },
  });
  fireEvent.change(screen.getByRole('textbox', { name: 'First clear · game credits' }), {
    target: { value: '2' },
  });
  expect(screen.queryByText('Use match memory')).not.toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[0]!.url).toContain('/games/gwent/ai/policies');
  expect(writes[0]!.body.definition).toEqual({
    schema: 'gwent-local/v1',
    faction: 'openai',
    preset: 'standard-control',
  });
  expect(writes[1]!.url).toContain('/games/gwent/ai/bots');
  expect(writes[1]!.body).toMatchObject({
    id: bot.id,
    policy_id: policy.id,
    policy_version: 2,
    ticket: '5',
    first_reward: '2',
    new_challenge: false,
  });
});
