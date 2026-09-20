import quick from '../../../../../internal/game/likes/catalog/quick.json' with { type: 'json' };
import standard from '../../../../../internal/game/likes/catalog/standard.json' with { type: 'json' };
import oldQuick from '../../../../../internal/game/likes/catalog/legacy/quick.json' with { type: 'json' };
import oldStandard from '../../../../../internal/game/likes/catalog/legacy/standard.json' with { type: 'json' };
import { likesCatalog } from './catalog';

export function catalogWire(hashes = { quick: 'a'.repeat(64), standard: 'b'.repeat(64) }) {
  const modes = Object.fromEntries(
    Object.entries({ quick, standard }).map(([mode, raw]) => {
      const config = structuredClone(raw);
      const parameters = config.parameters as Record<string, number>;
      delete parameters.POINT_TICKET;
      delete parameters.FOLLOWUP_CAP;
      config.paramMeta = config.paramMeta.filter(
        (p) => !['POINT_TICKET', 'FOLLOWUP_CAP'].includes(p.id),
      );
      return [
        mode,
        {
          rules_version: 1,
          design_version: '0.18.0',
          schema_version: 16,
          content_hash: hashes[mode as keyof typeof hashes],
          config,
        },
      ];
    }),
  );
  return {
    rules_version: 1,
    design_version: '0.18.0',
    schema_version: 16,
    content_hash: 'a'.repeat(64),
    modes,
  };
}
export const testCatalog = likesCatalog(catalogWire());

export function legacyCatalogWire() {
  return [oldQuick, oldStandard].map((raw, index) => {
    const config = structuredClone(raw);
    const parameters = config.parameters as Record<string, number>;
    delete parameters.POINT_TICKET;
    delete parameters.FOLLOWUP_CAP;
    config.paramMeta = config.paramMeta.filter(
      (p) => !['POINT_TICKET', 'FOLLOWUP_CAP'].includes(p.id),
    );
    return {
      rules_version: 1,
      design_version: '0.17.0',
      schema_version: 15,
      content_hash:
        index === 0
          ? '65512e407ece9c31486cc808100780607b524cc6a95d1e2a9b6ef0424c6f7333'
          : '55473a4623bf7974a64210f1afbcc2f0411d7557888c9a20fc8df88ad23d0dd7',
      config,
    };
  });
}
