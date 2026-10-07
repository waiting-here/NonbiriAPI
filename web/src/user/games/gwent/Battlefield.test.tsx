import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { Battlefield } from './Battlefield';
import { sendDuelIntent } from '../common/duel/api';
import type { Card, View } from './types';

const card: Card = {
  id: 'openai_scout',
  name: 'Scout',
  faction: 'openai',
  type: 'unit',
  power: 4,
  base_power: 4,
  row: 'agile',
  abilities: ['spy'],
  maxCopies: 3,
  image: 'assets/cards/scout.webp',
  starterCopies: 1,
  instance_id: 7,
};
const leader: Card = {
  ...card,
  id: 'openai_leader',
  name: 'Leader',
  type: 'leader',
  instance_id: 1,
  abilities: ['leader_openai'],
};
function fixture(): View {
  const player = {
    faction: 'openai' as const,
    lives: 2,
    passed: false,
    hand_count: 1,
    deck_count: 20,
    grave: [],
    leader,
    leader_available: true,
    boost: 0,
    shield: false,
  };
  return {
    version: 1,
    round: 1,
    phase: 'turn',
    turn: 0,
    self: player,
    enemy: { ...player, leader: { ...leader, instance_id: 2 } },
    hand: [card],
    board: ['enemy', 'self'].flatMap((side) =>
      ['close', 'ranged', 'siege'].map((row) => ({
        side: side as 'enemy' | 'self',
        row,
        total: 0,
        weather: false,
        cards: [],
      })),
    ),
    weather: [],
    rounds: [],
    legal_actions: [
      { kind: 'play', card: 7, row: 'close' },
      { kind: 'play', card: 7, row: 'ranged' },
    ],
  };
}
it('inspects a card before sending an explicit legal row choice', async () => {
  const onAction = vi.fn(),
    view = fixture();
  const rendered = await renderWithProviders(
    <Battlefield view={view} disabled={false} onAction={onAction} />,
    { station: 'user' },
  );
  await rendered.user.click(screen.getByTitle('Scout'));
  expect(onAction).not.toHaveBeenCalled();
  await rendered.user.click(screen.getByRole('button', { name: 'Play · Sensor matrix' }));
  expect(onAction).toHaveBeenCalledExactlyOnceWith({ kind: 'play', card: 7, row: 'ranged' });
  rendered.rerender(
    <Battlefield view={{ ...view, legal_actions: [] }} disabled={false} onAction={onAction} />,
  );
  expect(screen.queryByRole('button', { name: /^Play/ })).not.toBeInTheDocument();
  expect(screen.getByRole('heading', { name: 'Scout' })).toBeInTheDocument();
});
it('keeps optional choices explicit and disables dispatch while a response is pending', async () => {
  const view = fixture(),
    onAction = vi.fn();
  view.phase = 'choice';
  view.choice = { kind: 'analysis', cards: [], rows: [], remaining: 1, can_quit: true };
  view.legal_actions = [{ kind: 'continue' }];
  const rendered = await renderWithProviders(
    <Battlefield view={view} disabled onAction={onAction} />,
    { station: 'user' },
  );
  expect(screen.getByRole('button', { name: 'Continue' })).toBeDisabled();
  rendered.rerender(<Battlefield view={view} disabled={false} onAction={onAction} />);
  await rendered.user.click(screen.getByRole('button', { name: 'Continue' }));
  expect(onAction).toHaveBeenCalledExactlyOnceWith({ kind: 'continue' });
});
it('submits the decision token with a sequential action', async () => {
  const id = 'gwt_AAAAAAAAAAAAAAAAAAAAAA';
  const fetch = vi.fn(
    async () =>
      new Response(
        JSON.stringify({ session_id: id, revision: '3', phase_seq: '2', locked: false }),
        { headers: { 'content-type': 'application/json' } },
      ),
  );
  vi.stubGlobal('fetch', fetch);
  await sendDuelIntent(
    'gwent',
    { kind: 'action', id, phaseSeq: '2', decisionID: '9', action: { kind: 'continue' } },
    'decision-retry',
  );
  const options = fetch.mock.calls[0] as unknown as [unknown, RequestInit];
  expect(JSON.parse(String(options[1].body))).toEqual({
    phase_seq: '2',
    decision_id: '9',
    action: { kind: 'continue' },
  });
});
