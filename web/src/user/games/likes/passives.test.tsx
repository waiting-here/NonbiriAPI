import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import wire from './testdata/passives.json';
import { catalogWire, legacyCatalogWire } from './testCatalog';
import { likesCatalog, matchingCatalog } from './catalog';
import { eventValue, likesView, presentationValue, roundFacts } from './normalize';
import { CharacterPassive } from './CharacterPassive';
import { Arena } from './Arena';
import { EffectSummary } from './GuideText';

vi.mock('../common/duel/copy', () => ({ useDuelText: () => (_zh: string, en: string) => en }));
const catalog = likesCatalog({
  ...catalogWire({ quick: wire.partial.content_hash, standard: wire.chain.content_hash }),
  compatible_modes: legacyCatalogWire(),
});

describe('versioned character passives and authoritative feedback', () => {
  it('selects the exact old catalog without applying new passives', () => {
    const old = matchingCatalog(catalog, 'quick', wire.legacy.content_hash)!;
    expect(old.roles.every((r) => !r.passive)).toBe(true);
    expect(matchingCatalog(catalog, 'standard', wire.legacy.content_hash)).toBeUndefined();
    expect(matchingCatalog(catalog, 'quick', 'c'.repeat(64))).toBeUndefined();
    render(<CharacterPassive role={old.roles[0]} />);
    expect(screen.queryByText(/Always-active/)).toBeNull();
    expect(roundFacts(wire.legacy.facts).events.some((e) => e.kind === 'effect-attempt')).toBe(
      false,
    );
  });
  it('requires complete character passives and closed effect classifications', () => {
    const badRole = catalogWire();
    delete (badRole.modes.quick.config.roles[0] as { passive?: unknown }).passive;
    expect(() => likesCatalog(badRole)).toThrow();
    const badBuff = catalogWire();
    badBuff.modes.quick.config.buffs[0].category = 'unknown';
    expect(() => likesCatalog(badBuff)).toThrow();
    render(
      <>
        {catalog.modes.quick.roles.map((role) => (
          <CharacterPassive role={role} key={role.id} />
        ))}
      </>,
    );
    expect(screen.getAllByText(/Always-active/)).toHaveLength(5);
  });
  it('decodes partial layers, guaranteed hits and long Flash chains without adding attempt beats', () => {
    for (const source of Object.values(wire)) {
      likesView(source.before);
      likesView(source.after);
      const full = roundFacts(source.facts),
        summary = presentationValue(source.summary);
      expect(summary.events.some((e) => e.kind === 'effect-attempt')).toBe(false);
      expect(summary.timeline!.reduce((n, s) => n + s.durationMS, 0)).toBe(source.seconds * 1000);
      for (const event of full.events.filter((e) => e.kind === 'effect-attempt')) {
        if (event.data.draw === null) expect(event.data.success).toBe(true);
        else {
          const draw = full.draws.find((d) => d.ordinal === event.data.draw)!;
          expect(draw.candidate_count).toBe(event.data.denominator);
          expect(draw.index < (event.data.numerator as number)).toBe(event.data.success);
        }
      }
    }
    const partial = presentationValue(wire.partial.summary).events.find((e) => e.cast)!.cast!;
    expect(partial.applications?.map((a) => [a.success, a.resisted, a.derived])).toEqual([
      [1, 1, false],
      [0, 1, true],
    ]);
    expect(roundFacts(wire.guaranteed.facts).draws).toHaveLength(0);
    expect(
      presentationValue(wire.chain.summary).events.filter((e) => e.cast?.derived).length,
    ).toBeGreaterThanOrEqual(4);
  });
  it('rejects inconsistent probability metadata and impossible draws', () => {
    const event = wire.partial.facts.events.find((e) => e.kind === 'effect-attempt')!;
    for (const patch of [
      { hit: 1 },
      { resist: 75 },
      { numerator: 99 },
      { draw: null },
      { draw: 0 },
      { layer: 5 },
      { rules_version: 1 },
      { target: 0 },
    ])
      expect(() => eventValue({ ...event, data: { ...event.data, ...patch } })).toThrow();
    const guaranteed = wire.guaranteed.facts.events.find((e) => e.kind === 'effect-attempt')!;
    expect(() => eventValue({ ...guaranteed, data: { ...guaranteed.data, draw: 1 } })).toThrow();
  });
  it.each([false, true])(
    'shows partial application and the cast result with reduced motion %s',
    (reduced) => {
      const source = wire.partial,
        summary = presentationValue(source.summary);
      const at = source.summary.timeline.findIndex((s) => s.stage === 'score');
      const now =
        100 +
        source.summary.timeline.slice(0, at).reduce((n, s) => n + s.duration_ms, 0) / 1000 +
        0.5;
      render(
        <Arena
          catalog={catalog.modes.quick}
          view={likesView(source.after)}
          profiles={[{ kind: 'anonymous' }, { kind: 'anonymous' }]}
          you={0}
          round={1}
          locked={[true, true]}
          resolution={{ round: 1, startedAt: 100, endsAt: 100 + source.seconds, summary }}
          roundStart={null}
          now={now}
          reduced={reduced}
          onInspect={vi.fn()}
        />,
      );
      expect(screen.getByText(/Applied 1 \/ Resisted 1/)).toBeInTheDocument();
      expect(screen.getByText(/Applied 0 \/ Resisted 1/)).toHaveTextContent('Derived effect');
      expect(screen.getByText(/SOTA pressure/)).toBeInTheDocument();
      expect(screen.getByText(/Security shield/)).toBeInTheDocument();
    },
  );
  it('shows skill flavor directly with its functional summary', () => {
    const c = catalog.modes.quick;
    render(<EffectSummary catalog={c} id="GPT01" />);
    expect(document.querySelector('q')).toHaveTextContent(
      c.skills.find((s) => s.id === 'GPT01')!.meme,
    );
  });
});
