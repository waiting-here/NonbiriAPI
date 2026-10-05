import { duelCopyKeys } from '../common/duel/copy';
import { testDuelText } from '../common/duel/copy.test-support';
import { expect, it } from 'vitest';
import oldQuick from '../../../../../internal/game/likes/catalog/prior-balance/quick.json' with { type: 'json' };
import oldStandard from '../../../../../internal/game/likes/catalog/prior-balance/standard.json' with { type: 'json' };
import { likesCatalog } from './catalog';
import { catalogWire, testCatalog } from './testCatalog';
import { knowledge } from './knowledge';
import { shortageValue } from './shortage';

const text = testDuelText(duelCopyKeys);
it('explains the new rules while preserving the prior balance explanations', () => {
  const wire = catalogWire();
  wire.design_version = '0.18.1';
  wire.schema_version = 16;
  for (const [mode, raw] of Object.entries({ quick: oldQuick, standard: oldStandard })) {
    const value = wire.modes[mode];
    value.design_version = '0.18.1';
    value.schema_version = 16;
    const config = structuredClone(raw);
    delete (config.parameters as Record<string, number>).POINT_TICKET;
    delete (config.parameters as Record<string, number>).FOLLOWUP_CAP;
    config.paramMeta = config.paramMeta.filter(
      (p) => !['POINT_TICKET', 'FOLLOWUP_CAP'].includes(p.id),
    );
    value.config = config;
  }
  const prior = likesCatalog(wire).modes.quick;
  const current = testCatalog.modes.quick;
  expect(knowledge(current, 'DS23', 'base', text).summary).toContain('Overload');
  expect(knowledge(prior, 'DS23', 'base', text).summary).toContain('Stun');
  expect(knowledge(current, 'B34:状态', 'base', text).paragraphs.join(' ')).toContain(
    'image costs ×3',
  );
  expect(knowledge(current, 'GPT44', 'base', text).summary).toContain('costs ×3');
  expect(knowledge(current, 'DS41', 'base', text).summary).toContain('2 Subscription Squeeze');
  expect(knowledge(current, 'DS41', 'I', text).summary).toContain('1 Subscription Squeeze');
  expect(knowledge(current, 'SOTA_PRESSURE', 'base', text).paragraphs.join(' ')).toContain(
    'debuff layers',
  );
  expect(knowledge(prior, 'B34:状态', 'base', text).summary).toContain('×3');
  expect(knowledge(current, 'HP02', 'base', text).summary).toContain('1 like');
  expect(knowledge(prior, 'HP02', 'base', text).summary).not.toContain('1 like');
});

it('accepts actual image shortage and combined token facts without duplicates', () => {
  expect(
    shortageValue({
      payment: 'image',
      resources: [{ resource: 'R_IMAGE', required: 2, available: 1 }],
    }).resources[0].available,
  ).toBe(1);
  expect(
    shortageValue({
      payment: 'mix',
      resources: ['burst', 'sub', 'api', 'R_IMAGE'].map((resource) => ({
        resource,
        required: 2,
        available: 0,
      })),
    }).resources,
  ).toHaveLength(4);
  expect(() =>
    shortageValue({
      payment: 'image',
      resources: Array.from({ length: 2 }, () => ({
        resource: 'R_IMAGE',
        required: 2,
        available: 1,
      })),
    }),
  ).toThrow();
});
