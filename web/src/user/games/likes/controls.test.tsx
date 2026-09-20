import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import rawCatalog from '../../../../../internal/game/likes/catalog/quick.json';
import wire from './testdata/authority.json';
import { likesCatalog } from './catalog';
import { likesView } from './normalize';
import { PlanEditor } from './PlanEditor';
import { Arena } from './Arena';
import { ResourceMeter } from './ResourceMeter';
import { planControls } from './planControls';
import type { DuelState } from '../common/duel/types';
import type { LikesEvent, LikesView, Presentation, Status } from './types';

vi.mock('../common/duel/copy', () => ({ useDuelText: () => (_zh: string, en: string) => en }));
vi.mock('./Glossary', () => ({ SkillCost: () => null }));

const config = structuredClone(rawCatalog) as unknown as Record<string, unknown>;
const parameters = config.parameters as Record<string, number>;
delete parameters.POINT_TICKET;
delete parameters.FOLLOWUP_CAP;
config.paramMeta = (config.paramMeta as { id: string }[]).filter(
  (p) => !['POINT_TICKET', 'FOLLOWUP_CAP'].includes(p.id),
);
const mode = {
  rules_version: 1,
  design_version: '0.18.0',
  schema_version: 16,
  content_hash: 'a'.repeat(64),
  config,
};
const catalog = likesCatalog({
  rules_version: 1,
  design_version: '0.18.0',
  schema_version: 16,
  content_hash: 'a'.repeat(64),
  modes: { quick: mode, standard: { ...mode, config: { ...config, mode: 'standard' } } },
}).modes.quick;
function stateFixture(): DuelState<LikesView, Presentation, LikesEvent[]> {
  return {
    id: 'lik_' + 'A'.repeat(22),
    game: 'likes',
    mode: 'quick',
    contentHash: 'a'.repeat(64),
    revision: '1',
    phaseSeq: '1',
    phase: 'plan',
    round: 1,
    deadline: 120,
    serverNow: 100,
    you: 0,
    locked: [false, false],
    ticket: '0',
    rates: { platform: 0, welfare: 0, thursday: 0 },
    payment: { general: '0', game: '0' },
    profiles: [{ kind: 'anonymous' }, { kind: 'anonymous' }],
    view: likesView(wire.initial),
    resolution: null,
    roundStart: null,
  };
}
function stun(key: string): Status {
  return {
    key,
    kind: 'STUN',
    name: key,
    positive: false,
    p: 0,
    q: 0,
    remaining: 1,
    layers: 0,
    buffId: key,
    activeFrom: 1,
  };
}
describe('resource and casting controls', () => {
  it('groups subscription and image quota while keeping uncapped balances outside', () => {
    const state = stateFixture();
    render(
      <Arena
        catalog={catalog}
        view={state.view}
        profiles={state.profiles}
        you={0}
        round={1}
        locked={[false, false]}
        resolution={null}
        roundStart={null}
        now={100}
        reduced
        onInspect={vi.fn()}
      />,
    );
    const subscriptions = screen.getAllByRole('group', { name: 'SUBSCRIPTION USAGE' });
    expect(subscriptions).toHaveLength(2);
    for (const group of subscriptions) {
      expect(within(group).getByRole('meter', { name: 'Subscription burst' })).toBeInTheDocument();
      expect(within(group).getByRole('meter', { name: 'Subscription total' })).toBeInTheDocument();
      expect(within(group).queryByText('API reserve')).not.toBeInTheDocument();
      expect(within(group).queryByText('Gold')).not.toBeInTheDocument();
    }
    expect(
      within(subscriptions[0]).getByRole('meter', { name: 'Image quota' }),
    ).toBeInTheDocument();
    expect(within(subscriptions[1]).queryByText('Image quota')).not.toBeInTheDocument();
    expect(screen.queryByRole('meter', { name: 'API reserve' })).not.toBeInTheDocument();
    expect(screen.queryByRole('meter', { name: 'Gold' })).not.toBeInTheDocument();
  });
  it('animates uncapped values without presenting a capacity bar', () => {
    const { rerender } = render(
      <ResourceMeter label="API reserve" from={100} to={200} progress={0.2} />,
    );
    const first = screen.getByLabelText('API reserve: 200').textContent;
    expect(screen.queryByRole('meter')).not.toBeInTheDocument();
    expect(screen.getByText('+100')).toBeInTheDocument();
    rerender(<ResourceMeter label="API reserve" from={100} to={200} progress={0.7} />);
    expect(screen.getByLabelText('API reserve: 200').textContent).not.toBe(first);
    rerender(<ResourceMeter label="Gold" from={100} to={20} progress={0.2} reduced />);
    expect(screen.queryByRole('meter')).not.toBeInTheDocument();
    expect(screen.getByText('100 → 20')).toBeInTheDocument();
    expect(screen.getByLabelText('Gold: 20').textContent).toBe('20');
    rerender(<ResourceMeter label="Subscription" from={100} to={200} cap={400} />);
    expect(screen.getByRole('meter')).toHaveAttribute('aria-valuemax', '400');
  });
  it('requires a main skill and never offers an independent skip button normally', () => {
    const state = stateFixture(),
      onLock = vi.fn();
    render(
      <PlanEditor
        catalog={catalog}
        state={state}
        blocked={false}
        onLock={onLock}
        onInspect={vi.fn()}
      />,
    );
    expect(screen.getByRole('button', { name: 'Lock in plan' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: /Skip/ })).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', {
        name: new RegExp(
          catalog.skills.find((s) => s.id === state.view.players[0].loadout![0])!.name + 'Basic',
        ),
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Lock in plan' }));
    expect(onLock.mock.calls[0][0].main).not.toBeNull();
  });
  it('uses one skip action while stunned, then requires a cast after cleansing', () => {
    const state = stateFixture(),
      onLock = vi.fn();
    state.view.players[0].stunned = true;
    state.view.players[0].effects = [stun('stun-a')];
    render(
      <PlanEditor
        catalog={catalog}
        state={state}
        blocked={false}
        onLock={onLock}
        onInspect={vi.fn()}
      />,
    );
    expect(screen.queryByRole('button', { name: 'Lock in plan' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Skip casting' }));
    expect(onLock).toHaveBeenCalledWith({ purchases: [], main: null, extra: [] });
    fireEvent.click(screen.getByRole('button', { name: /^Shop/ }));
    const card = screen.getByText('Cleansing item').parentElement!;
    fireEvent.click(card.querySelector('button')!);
    expect(screen.queryByRole('button', { name: 'Skip casting' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Lock in plan' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Skills' }));
    fireEvent.click(
      screen.getByRole('button', {
        name: new RegExp(
          catalog.skills.find((s) => s.id === state.view.players[0].loadout![0])!.name + 'Basic',
        ),
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Lock in plan' }));
    expect(onLock.mock.calls[1][0].main).not.toBeNull();
    expect(onLock.mock.calls[1][0].purchases).toEqual([{ item: 'cleanse', target: 'stun-a' }]);
  });
  it('handles multiple stuns, unrelated cleansing, and insufficient shopping gold', () => {
    const p = stateFixture().view.players[0];
    p.stunned = true;
    p.effects = [stun('a'), stun('b')];
    const plan = { purchases: [{ item: 'cleanse' as const, target: 'a' }], main: null, extra: [] };
    expect(planControls(catalog, p, 1, plan).skipCasting).toBe(true);
    p.effects = [stun('a')];
    expect(planControls(catalog, p, 1, plan).skipCasting).toBe(false);
    p.gold = 0;
    expect(planControls(catalog, p, 1, plan)).toEqual({ affordable: false, skipCasting: true });
    p.gold = 1000;
    plan.purchases[0].target = 'unrelated';
    expect(planControls(catalog, p, 1, plan).skipCasting).toBe(true);
  });
});
