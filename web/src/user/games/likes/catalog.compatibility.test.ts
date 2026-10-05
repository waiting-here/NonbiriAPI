import { describe, expect, it } from 'vitest';
import priorQuick from '../../../../../internal/game/likes/catalog/prior-balance/quick.json';
import priorStandard from '../../../../../internal/game/likes/catalog/prior-balance/standard.json';
import balanceQuick from '../../../../../internal/game/likes/catalog/balance-v3/quick.json';
import balanceStandard from '../../../../../internal/game/likes/catalog/balance-v3/standard.json';
import previousQuick from '../../../../../internal/game/likes/catalog/previous/quick.json';
import previousStandard from '../../../../../internal/game/likes/catalog/previous/standard.json';
import { catalogWire, legacyCatalogWire } from './testCatalog';
import { likesCatalog, matchingCatalog } from './catalog';

function compatibleCatalogs() {
  const versions = [
    ['0.19.0', balanceQuick],
    ['0.19.0', balanceStandard],
    ['0.18.1', priorQuick],
    ['0.18.1', priorStandard],
    ['0.18.0', previousQuick],
    ['0.18.0', previousStandard],
  ] as const;
  return [
    ...versions.map(([design, raw], index) => {
      const config = structuredClone(raw);
      const parameters = config.parameters as Record<string, number>;
      delete parameters.POINT_TICKET;
      delete parameters.FOLLOWUP_CAP;
      config.paramMeta = config.paramMeta.filter(
        (p) => !['POINT_TICKET', 'FOLLOWUP_CAP'].includes(p.id),
      );
      return {
        rules_version: 1,
        design_version: design,
        schema_version: config.schemaVersion,
        content_hash: String(index + 1).repeat(64),
        config,
      };
    }),
    ...legacyCatalogWire(),
  ];
}

describe('supported historical Likes catalogs', () => {
  it('loads all retained versions for both modes and selects the exact saved rules', () => {
    const compatible_modes = compatibleCatalogs();
    const decoded = likesCatalog({ ...catalogWire(), compatible_modes });
    expect(decoded.compatibleModes).toHaveLength(8);
    for (const snapshot of compatible_modes) {
      expect(
        matchingCatalog(decoded, snapshot.config.mode, snapshot.content_hash)?.designVersion,
      ).toBe(snapshot.design_version);
      expect(
        matchingCatalog(
          decoded,
          snapshot.config.mode === 'quick' ? 'standard' : 'quick',
          snapshot.content_hash,
        ),
      ).toBeUndefined();
    }
  });
});
