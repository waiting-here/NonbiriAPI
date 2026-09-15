import { Card } from '@shared/components/States';
import { gameLabel, modeLabel, modesFor, useGameAdminText, type GameID } from './copy';
import { percentBP, type DuelGameConfig } from './config';

export function DuelConfiguration({
  game,
  value,
  disabled,
  onChange,
}: {
  game: GameID;
  value: DuelGameConfig;
  disabled: boolean;
  onChange: (value: DuelGameConfig) => void;
}) {
  const t = useGameAdminText();
  return (
    <Card>
      <h2>{gameLabel(game, t)}</h2>
      <p>
        {t(
          '已入队和正在进行的对局沿用进入时的票价与抽水。关闭后，队列按原币种退款，在局玩家可以完成对战。',
          'Queued and active matches keep their entry terms. Closing refunds queued entries to their original wallets and lets active matches finish.',
        )}
      </p>
      <label className="checkbox-label">
        <input
          type="checkbox"
          checked={value.enabled}
          disabled={disabled}
          onChange={(e) => onChange({ ...value, enabled: e.target.checked })}
        />
        {t('开启游戏', 'Enable game')}
      </label>
      <div className="ops-stack">
        {modesFor(game).map((mode) => {
          const m = value.modes[mode],
            update = (patch: Partial<typeof m>) =>
              onChange({ ...value, modes: { ...value.modes, [mode]: { ...m, ...patch } } });
          return (
            <section className="ops-subcard" key={mode}>
              <h3>{modeLabel(mode, t)}</h3>
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={m.enabled}
                  disabled={disabled}
                  onChange={(e) => update({ enabled: e.target.checked })}
                />
                {t('开启场次', 'Enable mode')}
              </label>
              <div className="ops-field-grid">
                <label>
                  <span>{t('每人票价（积分）', 'Entry per player (credits)')}</span>
                  <input
                    type="text"
                    inputMode="decimal"
                    maxLength={20}
                    value={m.ticket}
                    disabled={disabled}
                    onChange={(e) => update({ ticket: e.target.value })}
                  />
                </label>
                {(['platform', 'welfare', 'thursday'] as const).map((field) => (
                  <label key={field}>
                    <span>
                      {
                        {
                          platform: t('平台抽水（%）', 'Platform cut (%)'),
                          welfare: t('低保池抽水（%）', 'Welfare pool cut (%)'),
                          thursday: t('周四池抽水（%）', 'Thursday pool cut (%)'),
                        }[field]
                      }
                    </span>
                    <input
                      type="number"
                      min="0"
                      max="99.99"
                      step="0.01"
                      value={Number.isNaN(m.rake_bp[field]) ? '' : m.rake_bp[field] / 100}
                      disabled={disabled}
                      onChange={(e) =>
                        update({ rake_bp: { ...m.rake_bp, [field]: percentBP(e.target.value) } })
                      }
                    />
                  </label>
                ))}
              </div>
            </section>
          );
        })}
      </div>
    </Card>
  );
}
