import { screen, within, fireEvent, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { AIPlayersAdmin } from './AIPlayers';
import type { AIAdminState, AIPolicyDefinition } from '@shared/aiPlayers';
const policy: AIPolicyDefinition = {
  schema: 'bidding-local/v1',
  parameters: {
    hand_value: 0.65,
    urgency: 0.15,
    exploration: 0.03,
    memory_weight: 0.65,
    joker_cost: 0.25,
    endgame_guard: true,
  },
  rules: [],
};
const data: AIAdminState = {
  settings: { enabled: false, revision: '1' },
  policies: [
    {
      id: 'aip_test',
      name: 'Balanced',
      description: 'Default',
      enabled: true,
      revision: '1',
      version: 1,
      source_id: 'bidding-local',
      schema_id: policy.schema,
      definition: policy,
    },
  ],
  bots: [],
  presets: [
    { id: 'balanced', name: 'Balanced', description: 'Default', policy },
    {
      id: 'patient',
      name: 'Patient',
      description: 'Hold strong cards',
      policy: { ...policy, parameters: { ...policy.parameters, hand_value: 0.95 } },
    },
  ],
  scenarios: [
    {
      id: 'opening',
      name: 'Opening bid',
      observation: {
        round: 1,
        phase: 'bid',
        seat: 0,
        view: {
          hand_remaining: [
            [1, 2, 3],
            [1, 2, 3],
          ],
          pool_points: 18,
          scores: [0, 0],
        },
      },
    },
  ],
};
it('duplicates a strategy, edits ordered conditions and previews through the backend', async () => {
  const previews: unknown[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url, options) => {
      if (String(url).endsWith('/preview')) {
        previews.push(JSON.parse(options.body));
        return new Response(
          JSON.stringify({
            candidates: [{ id: 'bid-2', score: 0.45, probability: 1, guaranteed_win: false }],
            matched_rule: 0,
            filter_empty: false,
            memory_weight: 0,
          }),
          { headers: { 'Content-Type': 'application/json' } },
        );
      }
      return new Response(JSON.stringify(data), {
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
  const { user } = await renderWithProviders(<AIPlayersAdmin />, {
    station: 'admin',
  });
  await user.click(await screen.findByRole('button', { name: 'Duplicate' }));
  expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('Balanced copy');
  await user.click(screen.getByRole('button', { name: 'Patient' }));
  expect(screen.getByRole('spinbutton', { name: 'Preserve strong cards' })).toHaveValue(0.95);
  await user.click(screen.getByRole('button', { name: 'Add rule' }));
  await user.click(screen.getByRole('button', { name: 'Add rule' }));
  const conditions = screen.getAllByRole('spinbutton', { name: 'Condition value' });
  fireEvent.change(conditions[1]!, { target: { value: '11' } });
  await user.click(screen.getAllByRole('button', { name: 'Move rule up' })[1]!);
  await user.click(screen.getByRole('button', { name: 'Preview choices' }));
  await screen.findByText('Matched rule 1');
  expect(previews).toEqual([
    {
      scenario: 'opening',
      definition: {
        ...policy,
        parameters: { ...policy.parameters, hand_value: 0.95 },
        rules: [
          {
            match: 'all',
            conditions: [{ field: 'round', operator: 'gte', value: 11 }],
            override: {},
            filter: '',
          },
          {
            match: 'all',
            conditions: [{ field: 'round', operator: 'gte', value: 9 }],
            override: {},
            filter: '',
          },
        ],
      },
    },
  ]);
  expect(within(screen.getByRole('table')).getByText('100.0%')).toBeVisible();
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save strategy version' })).toBeEnabled(),
  );
});
