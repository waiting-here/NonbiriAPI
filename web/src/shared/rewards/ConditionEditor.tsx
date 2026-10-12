import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Field } from '@shared/components/ui';
import { TimeInput } from '@shared/components/TimeInput';
import { createTimeDraft, timeDraftValue } from '@shared/time';
import { assetName, logicLabels, fieldLabels, comparisonLabels } from './copy';
import type { Asset, Condition } from './api';
const fields = ['level', 'donation', 'registered_at', 'registered_days', 'banned', 'asset'];
const comparisons = ['gte', 'gt', 'eq', 'ne', 'lte', 'lt'];
const leaf = (): Condition => ({ op: 'compare', field: 'level', compare: 'gte', value: '1' });
function ConditionDate({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const [draft, setDraft] = useState(() => createTimeDraft(value ? Number(value) : null));
  return (
    <TimeInput
      station="admin"
      draft={draft}
      onChange={(next) => {
        const result = typeof next === 'function' ? next(draft) : next;
        setDraft(result);
        const v = timeDraftValue(result);
        onChange(v == null ? '' : String(v));
      }}
    />
  );
}
export function ConditionEditor({
  value,
  onChange,
  assets,
  depth = 0,
}: {
  value: Condition;
  onChange: (value: Condition) => void;
  assets: Asset[];
  depth?: number;
}) {
  const { t, i18n } = useTranslation();
  const english = i18n.language.startsWith('en');
  const children = value.children ?? [];
  return (
    <fieldset className="reward-condition">
      <legend>{t('common.rewards.conditionGroup')}</legend>
      <Field label={t('common.rewards.logic')}>
        {(props) => (
          <select
            {...props}
            value={value.op}
            onChange={(e) => {
              const op = e.target.value as Condition['op'];
              onChange(
                op === 'all'
                  ? { op }
                  : op === 'compare'
                    ? leaf()
                    : { op, children: op === 'not' ? [leaf()] : [leaf()] },
              );
            }}
          >
            {(['all', 'and', 'or', 'not', 'compare'] as const).map((op) => (
              <option key={op} value={op}>
                {t(logicLabels[op])}
              </option>
            ))}
          </select>
        )}
      </Field>
      {value.op === 'compare' ? (
        <div className="reward-condition-fields">
          <Field label={t('common.rewards.field')}>
            {(props) => (
              <select
                {...props}
                value={value.field}
                onChange={(e) =>
                  onChange({
                    op: 'compare',
                    field: e.target.value,
                    compare: e.target.value === 'banned' ? 'eq' : 'gte',
                    value:
                      e.target.value === 'banned'
                        ? 'false'
                        : e.target.value === 'level'
                          ? '1'
                          : '0',
                    ...(e.target.value === 'asset' ? { asset: 'general' } : {}),
                  })
                }
              >
                {fields.map((f) => (
                  <option key={f} value={f}>
                    {t(fieldLabels[f ?? 'level'])}
                  </option>
                ))}
              </select>
            )}
          </Field>
          {value.field === 'asset' && (
            <Field label={t('common.rewards.asset')}>
              {(props) => (
                <select
                  {...props}
                  value={value.asset}
                  onChange={(e) => onChange({ ...value, asset: e.target.value })}
                >
                  {assets.map((a) => (
                    <option key={a.id} value={a.id}>
                      {assetName(assets, a.id, english)}
                    </option>
                  ))}
                </select>
              )}
            </Field>
          )}
          <Field label={t('common.rewards.compare')}>
            {(props) => (
              <select
                {...props}
                value={value.compare}
                onChange={(e) => onChange({ ...value, compare: e.target.value })}
              >
                {(value.field === 'banned' ? ['eq', 'ne'] : comparisons).map((c) => (
                  <option key={c} value={c}>
                    {t(comparisonLabels[c ?? 'gte'])}
                  </option>
                ))}
              </select>
            )}
          </Field>
          {value.field === 'registered_at' ? (
            <ConditionDate
              value={value.value ?? ''}
              onChange={(v) => onChange({ ...value, value: v })}
            />
          ) : (
            <Field label={t('common.rewards.threshold')}>
              {(props) =>
                value.field === 'banned' ? (
                  <select
                    {...props}
                    value={value.value}
                    onChange={(e) => onChange({ ...value, value: e.target.value })}
                  >
                    <option value="false">{t('common.rewards.notBanned')}</option>
                    <option value="true">{t('common.rewards.banned')}</option>
                  </select>
                ) : (
                  <input
                    {...props}
                    inputMode="decimal"
                    value={value.value ?? ''}
                    onChange={(e) => onChange({ ...value, value: e.target.value })}
                  />
                )
              }
            </Field>
          )}
        </div>
      ) : (
        value.op !== 'all' && (
          <>
            {children.map((c, index) => (
              <div className="reward-condition-child" key={index}>
                <ConditionEditor
                  value={c}
                  assets={assets}
                  depth={depth + 1}
                  onChange={(next) =>
                    onChange({
                      ...value,
                      children: children.map((old, i) => (i === index ? next : old)),
                    })
                  }
                />
                {value.op !== 'not' && children.length > 1 && (
                  <Button
                    type="button"
                    size="sm"
                    onClick={() =>
                      onChange({ ...value, children: children.filter((_, i) => i !== index) })
                    }
                  >
                    {t('common.rewards.removeCondition')}
                  </Button>
                )}
              </div>
            ))}
            {value.op !== 'not' && depth < 8 && children.length < 16 && (
              <Button
                type="button"
                size="sm"
                onClick={() => onChange({ ...value, children: [...children, leaf()] })}
              >
                {t('common.rewards.addCondition')}
              </Button>
            )}
          </>
        )
      )}
    </fieldset>
  );
}
