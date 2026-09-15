import type { ModeCatalog } from './catalog';
import type { Plan, Player } from './types';

// This controls the local form only. The server validates the submitted plan.
export function planControls(catalog: ModeCatalog, player: Player, round: number, plan: Plan) {
  const harness = catalog.harnesses.find((h) => h.id === player.harness);
  const discount =
    catalog.passives.find(
      (p) => p.kind === 'VALUE_SUBSCRIPTION' && harness?.passives.includes(p.id),
    )?.p ?? 0;
  const prices = {
    sub: Math.max(0, catalog.parameters.SUB_PRICE - discount),
    api: catalog.parameters.API_PRICE,
    charge: catalog.parameters.CHARGE_PRICE,
    cleanse: catalog.parameters.CLEANSE_PRICE,
    regulator: catalog.parameters.REGULATOR_PRICE,
  };
  const affordable = plan.purchases.reduce((sum, p) => sum + prices[p.item], 0) <= player.gold;
  const target = affordable ? plan.purchases.find((p) => p.item === 'cleanse')?.target : undefined;
  const skipCasting =
    player.stunned &&
    player.effects.some((s) => s.kind === 'STUN' && s.activeFrom <= round && s.key !== target);
  return { affordable, skipCasting };
}
