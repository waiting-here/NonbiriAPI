import { useTranslation } from 'react-i18next';
import { Affix, Field, Fold, Toggle } from '@shared/components/ui';
import type { GamesConfig, RPSModeConfig } from '../operations/economy';
import { percentBP } from './config';
import { fishingChanceFromPercent } from './fishing';
import { useGameAdminText } from './copy';

type Props = {
  draft: GamesConfig;
  disabled: boolean;
  edit: (change: (current: GamesConfig) => GamesConfig) => void;
};
const numeric = (value: number) => (Number.isNaN(value) ? '' : value);
const numberFromInput = (value: string) => (value === '' ? Number.NaN : Number(value));
const pumps = ['platform', 'welfare', 'thursday'] as const;

export function FishingFields({ draft, disabled, edit }: Props) {
  const { t } = useTranslation();
  const text = useGameAdminText();
  const value = draft.fishing;
  const update = (patch: Partial<typeof value>) =>
    edit((current) => ({ ...current, fishing: { ...current.fishing, ...patch } }));
  return (
    <>
      <fieldset className="nb-fieldset" disabled={disabled}>
        <legend>{text('鱼饵价格', 'Bait prices')}</legend>
        <div className="nb-grid nb-grid--3">
          {(['worm', 'lure', 'premium'] as const).map((bait) => (
            <Field key={bait} label={t(`admin.games.${bait}`)}>
              {(props) => (
                <Affix
                  {...props}
                  unit={text('积分', 'credits')}
                  type="number"
                  inputMode="decimal"
                  min="0.001"
                  max="9000000000000"
                  step="0.001"
                  value={value.bait_prices[bait]}
                  name={`fishing.bait_prices.${bait}`}
                  onChange={(event) =>
                    update({ bait_prices: { ...value.bait_prices, [bait]: event.target.value } })
                  }
                />
              )}
            </Field>
          ))}
        </div>
      </fieldset>
      <fieldset className="nb-fieldset" disabled={disabled}>
        <legend>{text('返还率与特别鱼种', 'Returns and special fish')}</legend>
        <p className="nb-fieldset__hint">
          {text(
            '长期平均每投入 100 积分返还多少。',
            'Average return for every 100 credits spent over time.',
          )}
        </p>
        <div className="nb-grid nb-grid--2">
          {(['standard', 'premium'] as const).map((mode) => (
            <Field
              key={mode}
              label={t(mode === 'standard' ? 'admin.games.standardRTP' : 'admin.games.premiumRTP')}
            >
              {(props) => (
                <Affix
                  {...props}
                  unit="%"
                  type="number"
                  min="0"
                  max="100"
                  step="1"
                  value={numeric(value.rtp_percent[mode])}
                  name={`fishing.rtp_percent.${mode}`}
                  onChange={(event) =>
                    update({
                      rtp_percent: {
                        ...value.rtp_percent,
                        [mode]: numberFromInput(event.target.value),
                      },
                    })
                  }
                />
              )}
            </Field>
          ))}
          <Field
            span
            label={text('蓝色大肥鱼概率', 'Blue-fish probability')}
            help={text(
              '传奇鱼变为蓝色大肥鱼的概率；奖励和长度保持不变。',
              'Chance to decorate a legendary catch; rewards and length stay the same.',
            )}
          >
            {(props) => (
              <Affix
                {...props}
                unit="%"
                type="number"
                inputMode="decimal"
                min="0"
                max="100"
                step="0.01"
                value={numeric(value.blue_fish_chance_bps / 100)}
                name="fishing.blue_fish_chance_bps"
                onChange={(event) =>
                  update({ blue_fish_chance_bps: fishingChanceFromPercent(event.target.value) })
                }
              />
            )}
          </Field>
        </div>
      </fieldset>
      <fieldset className="nb-fieldset" disabled={disabled}>
        <legend>{text('抽成去向', 'Fee destinations')}</legend>
        <p className="nb-fieldset__hint">
          {text('三项合计须小于 100%。', 'The total must be below 100%.')}
        </p>
        <div className="nb-grid nb-grid--3">
          {pumps.map((pump) => (
            <Field key={pump} label={t(`admin.games.rps.pumps.${pump}`)}>
              {(props) => (
                <Affix
                  {...props}
                  unit="%"
                  type="number"
                  min="0"
                  max="99.99"
                  step="0.01"
                  value={numeric(value.rake_bp[pump] / 100)}
                  name={`fishing.rake_bp.${pump}`}
                  onChange={(event) =>
                    update({ rake_bp: { ...value.rake_bp, [pump]: percentBP(event.target.value) } })
                  }
                />
              )}
            </Field>
          ))}
        </div>
      </fieldset>
      <Fold title={text('宝物倍率', 'Treasure multipliers')}>
        <p className="nb-field__help">
          {text(
            '抽中宝物时，扣费前奖励等于本次鱼饵价格乘以倍率。',
            'A treasure pays the bait price multiplied by this value, before fees.',
          )}
        </p>
        <div className="nb-grid nb-grid--3">
          {(['bottle', 'clover', 'shell'] as const).map((treasure) => (
            <Field key={treasure} label={t(`admin.games.${treasure}`)}>
              {(props) => (
                <Affix
                  {...props}
                  unit={text('倍', '×')}
                  type="number"
                  min="0"
                  max="1000000"
                  step="1"
                  disabled={disabled}
                  value={numeric(value.treasure_multipliers[treasure])}
                  name={`fishing.treasure_multipliers.${treasure}`}
                  onChange={(event) =>
                    update({
                      treasure_multipliers: {
                        ...value.treasure_multipliers,
                        [treasure]: numberFromInput(event.target.value),
                      },
                    })
                  }
                />
              )}
            </Field>
          ))}
        </div>
      </Fold>
    </>
  );
}

export function LinklinkFields({ draft, disabled, edit }: Props) {
  const { t } = useTranslation();
  const text = useGameAdminText();
  return (
    <>
      {(['6x8', '8x8', '10x10'] as const).map((spec) => {
        const value = draft.linklink.specs[spec];
        const update = (patch: Partial<typeof value>) =>
          edit((current) => ({
            ...current,
            linklink: {
              ...current.linklink,
              specs: {
                ...current.linklink.specs,
                [spec]: { ...current.linklink.specs[spec], ...patch },
              },
            },
          }));
        return (
          <fieldset key={spec} className="nb-fieldset admin-game-mode" disabled={disabled}>
            <legend>{t(`admin.games.linklink.specs.${spec}`)}</legend>
            <Toggle
              label={t('admin.games.linklink.specEnabled', {
                spec: t(`admin.games.linklink.specs.${spec}`),
              })}
              checked={value.enabled}
              disabled={disabled}
              onChange={(enabled) => update({ enabled })}
            />
            <Field label={text('入场价格', 'Entry price')}>
              {(props) => (
                <Affix
                  {...props}
                  unit={text('积分', 'credits')}
                  type="number"
                  inputMode="decimal"
                  min="0"
                  max="9000000000000"
                  step="0.001"
                  value={value.price}
                  name={`linklink.${spec}.price`}
                  onChange={(event) => update({ price: event.target.value })}
                />
              )}
            </Field>
          </fieldset>
        );
      })}
    </>
  );
}

export function RPSFields({ draft, disabled, edit }: Props) {
  const { t } = useTranslation();
  const text = useGameAdminText();
  return (
    <>
      {(['quick', 'standard', 'deathmatch'] as const).map((mode) => {
        const value = draft.rps.modes[mode];
        const update = (patch: Partial<RPSModeConfig>) =>
          edit((current) => ({
            ...current,
            rps: {
              ...current.rps,
              modes: { ...current.rps.modes, [mode]: { ...current.rps.modes[mode], ...patch } },
            },
          }));
        return (
          <fieldset key={mode} className="nb-fieldset admin-game-mode" disabled={disabled}>
            <legend>{t(`admin.games.rps.modes.${mode}`)}</legend>
            <Toggle
              label={t('admin.games.rps.modeEnabled', { mode: t(`admin.games.rps.modes.${mode}`) })}
              checked={value.enabled}
              disabled={disabled}
              onChange={(enabled) => update({ enabled })}
            />
            <Field label={t('admin.games.rps.base', { mode: t(`admin.games.rps.modes.${mode}`) })}>
              {(props) => (
                <Affix
                  {...props}
                  unit={text('积分', 'credits')}
                  type="number"
                  inputMode="decimal"
                  min="0"
                  max="9000000000000"
                  step="0.001"
                  value={value.base}
                  name={`rps.${mode}.base`}
                  onChange={(event) => update({ base: event.target.value })}
                />
              )}
            </Field>
            <fieldset className="nb-fieldset">
              <legend>{text('抽成去向', 'Fee destinations')}</legend>
              <p className="nb-fieldset__hint">
                {text('三项合计须小于 100%。', 'The total must be below 100%.')}
              </p>
              <div className="nb-grid nb-grid--3">
                {pumps.map((pump) => (
                  <Field key={pump} label={t(`admin.games.rps.pumps.${pump}`)}>
                    {(props) => (
                      <Affix
                        {...props}
                        unit="%"
                        type="number"
                        min="0"
                        max="99.99"
                        step="0.01"
                        value={numeric(value.pumps_bp[pump] / 100)}
                        name={`rps.${mode}.pumps_bp.${pump}`}
                        onChange={(event) =>
                          update({
                            pumps_bp: { ...value.pumps_bp, [pump]: percentBP(event.target.value) },
                          })
                        }
                      />
                    )}
                  </Field>
                ))}
              </div>
            </fieldset>
            <div className="nb-grid nb-grid--2">
              {(
                ['queue_seconds', 'gesture_seconds', 'dealer_seconds', 'follower_seconds'] as const
              ).map((field) => {
                const labels = {
                  queue_seconds: 'queue',
                  gesture_seconds: 'gesture',
                  dealer_seconds: 'dealerRaise',
                  follower_seconds: 'followers',
                };
                const deadlineKey = {
                  queue_seconds: 'queue',
                  gesture_seconds: 'gesture',
                  dealer_seconds: 'dealer',
                  follower_seconds: 'follower',
                };
                const bounds =
                  field === 'queue_seconds'
                    ? [30, 120]
                    : field === 'gesture_seconds'
                      ? [5, 20]
                      : [5, 15];
                return (
                  <Field
                    key={labels[field]}
                    label={t(`admin.games.rps.deadlines.${deadlineKey[field]}`)}
                  >
                    {(props) => (
                      <Affix
                        {...props}
                        unit={text('秒', 'seconds')}
                        type="number"
                        min={bounds[0]}
                        max={bounds[1]}
                        step="1"
                        value={numeric(value[field])}
                        name={`rps.${mode}.${field}`}
                        onChange={(event) =>
                          update({ [field]: numberFromInput(event.target.value) })
                        }
                      />
                    )}
                  </Field>
                );
              })}
            </div>
          </fieldset>
        );
      })}
    </>
  );
}
