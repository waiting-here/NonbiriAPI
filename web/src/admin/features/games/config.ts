import { amount, boolean, integer, invalidResponse, record } from '@shared/operations/wire';
import { gameLabel, modeLabel, modesFor, type GameID } from './copy';

export interface DuelModeConfig {
  enabled: boolean;
  ticket: string;
  rake_bp: { platform: number; welfare: number; thursday: number };
}
export interface DuelGameConfig {
  enabled: boolean;
  modes: Record<string, DuelModeConfig>;
}
export function normalizeDuelConfig(value: unknown, game: GameID): DuelGameConfig {
  const r = record(value, ['enabled', 'modes'], `${game} configuration`),
    modes = record(r.modes, modesFor(game), `${game} modes`);
  return {
    enabled: boolean(r.enabled, 'game enabled'),
    modes: Object.fromEntries(
      modesFor(game).map((mode) => {
        const m = record(modes[mode], ['enabled', 'ticket', 'rake_bp'], 'mode configuration');
        const bp = record(m.rake_bp, ['platform', 'welfare', 'thursday'], 'mode rake');
        const rates = {
          platform: integer(bp.platform, 'platform percentage', 0, 9999),
          welfare: integer(bp.welfare, 'welfare percentage', 0, 9999),
          thursday: integer(bp.thursday, 'Thursday percentage', 0, 9999),
        };
        const ticket = amount(m.ticket, 'ticket', false, 9_000_000_000_000_000n);
        if (ticket === '0' || rates.platform + rates.welfare + rates.thursday >= 10000)
          invalidResponse('mode configuration');
        return [mode, { enabled: boolean(m.enabled, 'mode enabled'), ticket, rake_bp: rates }];
      }),
    ),
  };
}
export function percentBP(value: string): number {
  const match = /^(0|[1-9][0-9]?)(?:\.([0-9]{0,2}))?$/.exec(value);
  return match ? Number(match[1]) * 100 + Number((match[2] ?? '').padEnd(2, '0')) : Number.NaN;
}
export function validateDuelConfigurations(
  config: { master_enabled: boolean; bidding?: DuelGameConfig; likes?: DuelGameConfig },
  t: (zh: string, en: string) => string,
): string | null {
  for (const game of ['bidding', 'likes'] as const) {
    const value = config[game];
    if (!value) continue;
    if (value.enabled && !config.master_enabled)
      return t('请先开启小游戏总开关。', 'Enable the games master switch first.');
    for (const mode of modesFor(game)) {
      const m = value.modes[mode],
        label = `${gameLabel(game, t)} · ${modeLabel(mode, t)}`;
      if (m.enabled && !value.enabled)
        return `${label}: ${t('请先开启这个游戏。', 'Enable this game first.')}`;
      const parts = /^(0|[1-9][0-9]*)(?:\.([0-9]{1,3}))?$/.exec(m.ticket.trim());
      const milli = parts
        ? BigInt(parts[1]) * 1000n + BigInt((parts[2] ?? '').padEnd(3, '0'))
        : null;
      if (milli === null || milli < 1n || milli > 9_000_000_000_000_000n)
        return `${label}: ${t('票价需为0.001至9000000000000，最多三位小数。', 'Entry must be 0.001–9000000000000, with up to three decimal places.')}`;
      const rates = Object.values(m.rake_bp);
      if (
        rates.some((r) => !Number.isInteger(r) || r < 0 || r > 9999) ||
        rates.reduce((a, b) => a + b, 0) >= 10000
      )
        return `${label}: ${t('每项抽水需为0至99.99%，三项合计必须小于100%。', 'Each cut must be 0–99.99%, with a combined total below 100%.')}`;
    }
  }
  return null;
}
