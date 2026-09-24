import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Arena, FrameChanges } from './Arena';
import { LikesRoundLog } from './Log';
import { effectLayers, effectName } from './labels';
import { viewFrame } from './motion';
import { eventValue, likesView, presentationValue, roundFacts } from './normalize';
import { testCatalog } from './testCatalog';
import wire from './testdata/authority.json';
import type { EffectCue, Status } from './types';

const language = vi.hoisted(() => ({ value: 'en' }));
vi.mock('../common/duel/copy', () => ({
  useDuelText: () => (zh: string, en: string) => (language.value === 'zh' ? zh : en),
}));
const catalog = testCatalog.modes.quick;
const status: Status = {
  key: 'B07:原版',
  buffId: 'B07:原版',
  kind: 'CACHE',
  name: '短效缓存·Flash',
  positive: true,
  category: 'buff',
  p: 30,
  q: 3,
  remaining: 0,
  layers: 3,
  activeFrom: 4,
  persistentLayers: 3,
};
const cue: EffectCue = {
  key: status.key,
  buff_id: status.buffId,
  kind: status.kind,
  layers: 3,
  remaining: 0,
  active_from: 4,
  persistent_layers: 3,
};
function view() {
  const source = structuredClone(wire.initial);
  return likesView({
    ...source,
    players: [{ ...source.players[0], effects: [status] }, source.players[1]],
  });
}
function summary(effect: EffectCue) {
  const source = wire.rounds[0].summary;
  return {
    ...source,
    after: {
      ...source.after,
      players: [{ ...source.after.players[0], effects: [effect] }, source.after.players[1]],
    },
  };
}
describe('persistent cache presentation', () => {
  it('keeps counts through current, compact and full-log projections and leaves old summaries unknown', () => {
    expect(viewFrame(view()).players[0].effects[0].persistent_layers).toBe(3);
    expect(presentationValue(summary(cue)).after.players[0].effects[0].persistent_layers).toBe(3);
    const source = wire.rounds[0].facts;
    const facts = roundFacts({
      ...source,
      after: {
        ...source.after,
        players: [{ ...source.after.players[0], effects: [status] }, source.after.players[1]],
      },
    });
    expect(facts.after.players[0].effects[0].persistent_layers).toBe(3);
    const legacy = { ...cue };
    delete legacy.persistent_layers;
    const old = presentationValue(summary(legacy)).after.players[0].effects[0];
    expect(old.persistent_layers).toBeUndefined();
    expect(effectName(catalog, old, (zh) => zh)).toBe('短效缓存·Flash');
    expect(effectLayers(old, (zh) => zh)).toBe('层数: 3');
    for (const n of [-1, 4, 1.5]) {
      expect(() => presentationValue(summary({ ...cue, persistent_layers: n }))).toThrow();
    }
  });
  it.each(['en', 'zh'])(
    'shows persistent live state and retains the original glossary identity in %s',
    (locale) => {
      language.value = locale;
      const inspect = vi.fn();
      render(
        <Arena
          catalog={catalog}
          view={view()}
          profiles={[{ kind: 'anonymous' }, { kind: 'anonymous' }]}
          you={0}
          round={4}
          locked={[false, false]}
          resolution={null}
          roundStart={null}
          now={100}
          reduced
          onInspect={inspect}
        />,
      );
      const button = screen.getByRole('button', {
        name: locale === 'zh' ? /长效缓存·Flash/ : /Persistent cache·Flash/,
      });
      expect(button).toHaveTextContent(locale === 'zh' ? '持久层: 3' : 'Persistent layers: 3');
      fireEvent.click(button);
      expect(inspect).toHaveBeenCalledWith('B07:原版');
    },
  );
  it.each(['en', 'zh'])(
    'distinguishes mixed layers in before/after facts and conversion events in %s',
    (locale) => {
      language.value = locale;
      const before = viewFrame(view()),
        after = structuredClone(before);
      before.players[0].effects[0].persistent_layers = 0;
      after.players[0].effects[0].persistent_layers = 1;
      const { unmount } = render(<FrameChanges before={before} after={after} catalog={catalog} />);
      expect(
        screen.getByText(
          locale === 'zh'
            ? /持久层: 1 · 短效层: 2/
            : /Persistent layers: 1 · Short-lived layers: 2/,
        ),
      ).toBeInTheDocument();
      unmount();
      const facts = roundFacts(wire.rounds[0].facts);
      facts.events = [
        eventValue({
          id: 999,
          round: 4,
          stage: 'aftereffects',
          kind: 'persist',
          seat: 0,
          data: { buffId: status.buffId, layers: 3 },
        }),
      ];
      render(
        <LikesRoundLog
          catalog={catalog}
          you={0}
          round={{
            round: 4,
            before: view(),
            after: view(),
            facts,
            startEvents: [],
            timeouts: [false, false],
          }}
        />,
      );
      expect(
        screen.getByText(locale === 'zh' ? '长效缓存·Flash' : 'Persistent cache·Flash'),
      ).toBeInTheDocument();
      expect(
        screen.getByText(locale === 'zh' ? '持久层数' : 'Persistent layers'),
      ).toBeInTheDocument();
    },
  );
});
