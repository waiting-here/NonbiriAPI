import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import wire from './testdata/authority.json';
import { likesCatalog } from './catalog';
import { likesCodec, likesView, presentationValue, roundFacts, startEvents } from './normalize';
import { roundValue } from '../common/duel/normalize';
import { artRegistry, assertArtCoverage, castSlot, characterSlot } from './art';

export function catalogFixture() {
  const modes = Object.fromEntries(
    ['quick', 'standard'].map((mode) => {
      const source = readFileSync(
        resolve(process.cwd(), '../internal/game/likes/catalog', `${mode}.json`),
        'utf8',
      );
      const config = JSON.parse(source);
      delete config.parameters.POINT_TICKET;
      delete config.parameters.FOLLOWUP_CAP;
      config.paramMeta = config.paramMeta.filter(
        (v: { id: string }) => !['POINT_TICKET', 'FOLLOWUP_CAP'].includes(v.id),
      );
      for (const buff of config.buffs)
        if (buff.kind === 'OVERLOAD')
          buff.target = '自身；共享电能不足时仅本轮报价大于零的席位过载';
      return [
        mode,
        {
          rules_version: 1,
          design_version: '0.17.0',
          schema_version: 15,
          content_hash: createHash('sha256')
            .update('likes@1;positive-energy-overload;separate-round-start\n' + source)
            .digest('hex'),
          config,
        },
      ];
    }),
  );
  return {
    rules_version: 1,
    design_version: '0.17.0',
    schema_version: 15,
    content_hash: 'a'.repeat(64),
    modes,
  };
}
describe('likes catalog, art and projections', () => {
  it('loads both complete catalogs without legacy operating settings', () => {
    const c = likesCatalog(catalogFixture());
    expect(c.modes.quick.skills).toHaveLength(48);
    expect(c.modes.standard.buffs).toHaveLength(46);
    expect(c.modes.quick.parameters).not.toHaveProperty('POINT_TICKET');
    assertArtCoverage(c.modes.quick);
    assertArtCoverage(c.modes.standard);
    const missingParameter = catalogFixture();
    delete missingParameter.modes.quick.config.parameters.TARGET_LIKES;
    expect(() => likesCatalog(missingParameter)).toThrow();
    const missingPreset = catalogFixture();
    missingPreset.modes.quick.config.loadouts = [];
    expect(() => likesCatalog(missingPreset)).toThrow();
  });
  it('provides exactly 127 independent replacement slots and 84 legal cast slots', () => {
    const slots = Object.values(artRegistry);
    expect(slots).toHaveLength(127);
    expect(new Set(slots.map((s) => s.key)).size).toBe(127);
    expect(slots.filter((s) => s.key.startsWith('cast.'))).toHaveLength(84);
    expect(characterSlot('ChatGPT', 'chibi_stunned').key).not.toBe(
      characterSlot('ChatGPT', 'chibi_overloaded').key,
    );
    expect(castSlot('Claude', 'GEM01')).not.toBeNull();
    expect(castSlot('Claude', 'GPT01')).toBeNull();
    expect(new Set(slots.map((s) => s.source)).size).toBe(127);
    expect(slots.every((s) => s.transparentRequired && !s.placeholder)).toBe(true);
    expect(slots.every((s) => s.source.endsWith('.webp'))).toBe(true);
    expect(slots.every((s) => s.sourceFile.includes('/game-likes/'))).toBe(true);
  });
  it('decodes genuine rule-engine states, complete rounds, compact summaries and replenishment', () => {
    for (const view of [wire.initial, ...Object.values(wire.role_views)])
      expect(likesView(view).players).toHaveLength(2);
    for (const item of wire.rounds) {
      expect(likesView(item.after).players[1].fog).toBe(true);
      expect(roundFacts(item.facts).frames.length).toBeGreaterThan(0);
      expect(presentationValue(item.summary).events.some((e) => e.cast)).toBe(true);
      expect(startEvents(item.start)[0].transition).not.toBeNull();
      expect(
        roundValue(
          {
            round: 1,
            before: item.before,
            after: item.after,
            facts: item.facts,
            start_events: item.start,
            timeouts: [false, false],
          },
          likesCodec,
        ).round,
      ).toBe(1);
    }
  });
  it('rejects concealed loadouts, secret runtime counters, private slots and malformed events', () => {
    const source = structuredClone(wire.initial);
    expect(() => likesView({ ...source, cast_seq: 1 })).toThrow();
    expect(() =>
      likesView({
        ...source,
        players: [source.players[0], { ...source.players[1], distill: source.players[0].distill }],
      }),
    ).toThrow();
    expect(() =>
      likesView({
        ...source,
        players: [source.players[0], { ...source.players[1], loadout: ['GEM01'] }],
      }),
    ).toThrow();
    expect(() =>
      likesView({
        ...source,
        players: [
          source.players[0],
          { ...source.players[1], slots: ['GEM01', null, null, null, null] },
        ],
      }),
    ).toThrow();
    expect(() =>
      presentationValue({
        ...wire.rounds[0].summary,
        events: [
          { id: 1, round: 1, kind: 'cast', stage: 'score', seat: 0, data: { success: false } },
        ],
      }),
    ).toThrow();
  });
});
