import { Link } from 'react-router';
import type { GamesConfig } from '../operations/economy';
import {
  directions,
  directionUnits,
  naturalToUnits,
  unitsToNatural,
  type LakeSettings,
} from '@shared/lakenotes/api';
import { useGameAdminText } from '../games/copy';
import { useLakeAdminCopy } from './copy';
import './periods.css';

export function lakeDraft(value: LakeSettings): LakeSettings {
  return {
    ...value,
    exchanges: Object.fromEntries(
      directions.map((d) => {
        const v = value.exchanges[d],
          pair = directionUnits[d];
        return [
          d,
          {
            ...v,
            source_amount: v.source_amount ? unitsToNatural(v.source_amount, pair[0]) : '',
            target_amount: v.target_amount ? unitsToNatural(v.target_amount, pair[1]) : '',
          },
        ];
      }),
    ) as LakeSettings['exchanges'],
  };
}
export function lakeWire(value: LakeSettings): LakeSettings {
  return {
    ...value,
    exchanges: Object.fromEntries(
      directions.map((d) => {
        const v = value.exchanges[d],
          pair = directionUnits[d];
        if (!v.enabled && !v.source_amount && !v.target_amount) return [d, v];
        return [
          d,
          {
            ...v,
            source_amount: naturalToUnits(v.source_amount, pair[0]),
            target_amount: naturalToUnits(v.target_amount, pair[1]),
          },
        ];
      }),
    ) as LakeSettings['exchanges'],
  };
}
export function LakeFields({
  draft,
  edit,
  disabled,
}: {
  draft: GamesConfig;
  edit: (fn: (current: GamesConfig) => GamesConfig) => void;
  disabled: boolean;
}) {
  const text = useGameAdminText(),
    { t: lakeText } = useLakeAdminCopy();
  return (
    <fieldset className="lake-period-form" disabled={disabled}>
      <label className="lake-period-check">
        <input
          type="checkbox"
          checked={draft.lakenotes.enabled}
          onChange={(e) =>
            edit((c) => ({ ...c, lakenotes: { ...c.lakenotes, enabled: e.target.checked } }))
          }
        />
        {lakeText('enabled')}
      </label>
      <p>
        {text(
          '免费游玩。每个兑换方向可分别开启；先填写每份扣除和获得的数量。',
          'Free to play. Enable each exchange direction independently and set the amounts paid and received per lot.',
        )}
      </p>
      <div className="lake-period-grid">
        {directions.map((d) => {
          const value = draft.lakenotes.exchanges[d],
            pair = directionUnits[d];
          const change = (
            field: 'enabled' | 'source_amount' | 'target_amount',
            v: string | boolean,
          ) =>
            edit((c) => ({
              ...c,
              lakenotes: {
                ...c.lakenotes,
                exchanges: {
                  ...c.lakenotes.exchanges,
                  [d]: { ...c.lakenotes.exchanges[d], [field]: v },
                },
              },
            }));
          return (
            <section className="lake-exchange-setting" key={d}>
              <h3>{lakeText(d)}</h3>
              <label className="lake-period-check">
                <input
                  type="checkbox"
                  checked={value.enabled}
                  onChange={(e) => change('enabled', e.target.checked)}
                />
                {lakeText('enabled')}
              </label>
              {(['source_amount', 'target_amount'] as const).map((field, i) => (
                <label key={field}>
                  {lakeText(i === 0 ? 'source' : 'target')} · {lakeText(pair[i])}
                  <input
                    inputMode={pair[i] === 'coins' ? 'numeric' : 'decimal'}
                    value={value[field]}
                    required={value.enabled}
                    onChange={(e) => change(field, e.target.value)}
                  />
                </label>
              ))}
            </section>
          );
        })}
      </div>
      <Link to="/games/lake-notes/periods">
        {text('查看历史活动期次', 'View past activity periods')}
      </Link>
    </fieldset>
  );
}
