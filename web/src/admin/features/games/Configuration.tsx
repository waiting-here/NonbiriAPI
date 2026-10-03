import { Affix, Field, Note, Toggle } from '@shared/components/ui';
import { modeLabel, modesFor, useGameAdminText, type GameID } from './copy';
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
  const text = useGameAdminText();
  return (
    <>
      <Note>
        {text(
          '已入队和进行中的对局沿用进入时的票价与费用。关闭后，排队费用按原币种退还。',
          'Queued and active matches keep their entry terms. Closing refunds queued entries to their original wallets.',
        )}
      </Note>
      {modesFor(game).map((mode) => {
        const current = value.modes[mode];
        const update = (patch: Partial<typeof current>) =>
          onChange({ ...value, modes: { ...value.modes, [mode]: { ...current, ...patch } } });
        return (
          <fieldset className="nb-fieldset admin-game-mode" key={mode} disabled={disabled}>
            <legend>{modeLabel(mode, text)}</legend>
            <Toggle
              label={text('开启场次', 'Enable mode')}
              checked={current.enabled}
              disabled={disabled}
              onChange={(enabled) => update({ enabled })}
            />
            <Field label={text('每人票价', 'Entry per player')}>
              {(props) => (
                <Affix
                  {...props}
                  unit={text('积分', 'credits')}
                  type="text"
                  inputMode="decimal"
                  maxLength={20}
                  value={current.ticket}
                  onChange={(event) => update({ ticket: event.target.value })}
                />
              )}
            </Field>
            <fieldset className="nb-fieldset">
              <legend>{text('抽成去向', 'Fee destinations')}</legend>
              <p className="nb-fieldset__hint">
                {text('三项合计须小于 100%。', 'The total must be below 100%.')}
              </p>
              <div className="nb-grid nb-grid--3">
                {(['platform', 'welfare', 'thursday'] as const).map((field) => (
                  <Field
                    key={field}
                    label={
                      {
                        platform: text('平台', 'Platform'),
                        welfare: text('低保池', 'Welfare pool'),
                        thursday: text('周四池', 'Thursday pool'),
                      }[field]
                    }
                  >
                    {(props) => (
                      <Affix
                        {...props}
                        unit="%"
                        type="number"
                        min="0"
                        max="99.99"
                        step="0.01"
                        value={
                          Number.isNaN(current.rake_bp[field]) ? '' : current.rake_bp[field] / 100
                        }
                        onChange={(event) =>
                          update({
                            rake_bp: { ...current.rake_bp, [field]: percentBP(event.target.value) },
                          })
                        }
                      />
                    )}
                  </Field>
                ))}
              </div>
            </fieldset>
          </fieldset>
        );
      })}
    </>
  );
}
