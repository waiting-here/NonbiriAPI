import { useId, useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { fetchTimeZones } from '@shared/time';
import type { CharityRole } from '@shared/operations/charity';
import {
  weekdayKeys,
  validPeriodTime,
  type DiscountPeriod,
  type RecurringDiscount,
} from '@shared/operations/recurringDiscount';
import { Button, Field, Note, Toggle } from '../ui';
import { ErrorState } from '../States';

export function RecurringDiscountFields({
  value,
  role,
  fixedEnabled,
  onChange,
}: {
  value: RecurringDiscount;
  role: CharityRole;
  fixedEnabled: boolean;
  onChange: (value: RecurringDiscount) => void;
}) {
  const { t } = useTranslation();
  const listId = useId();
  const registry = useQuery({
    queryKey: ['time-zones', role],
    queryFn: ({ signal }) => fetchTimeZones(role, signal),
    staleTime: Infinity,
    retry: false,
  });
  const previousTimes = useRef(
    new WeakMap<DiscountPeriod, Pick<DiscountPeriod, 'start_time' | 'end_time'>>(),
  );
  const set = (fields: Partial<RecurringDiscount>) => onChange({ ...value, ...fields });
  const update = (index: number, fields: Partial<DiscountPeriod>, remember = false) =>
    set({
      periods: value.periods.map((row, i) => {
        if (i !== index) return row;
        const next = { ...row, ...fields };
        const prior = remember
          ? { start_time: row.start_time, end_time: row.end_time }
          : previousTimes.current.get(row);
        if (prior) previousTimes.current.set(next, prior);
        return next;
      }),
    });
  return (
    <div className="nb-form nb-recurring-discount">
      <Toggle
        label={t('common.recurringDiscount.enabled')}
        checked={value.enabled}
        onChange={(enabled) => set({ enabled })}
      />
      {fixedEnabled ? <Note>{t('common.recurringDiscount.paused')}</Note> : null}
      <div className="nb-grid nb-grid--2">
        <Field
          label={t('common.recurringDiscount.timeZone')}
          help={t('common.recurringDiscount.zoneHelp')}
        >
          {(props) => (
            <>
              <input
                {...props}
                list={listId}
                value={value.time_zone}
                maxLength={64}
                autoComplete="off"
                onChange={(event) => set({ time_zone: event.target.value })}
                aria-invalid={
                  registry.data ? !registry.data.zones.includes(value.time_zone) : undefined
                }
              />
              <datalist id={listId}>
                {registry.data?.zones.map((zone) => (
                  <option key={zone} value={zone} />
                ))}
              </datalist>
            </>
          )}
        </Field>
        <Field
          label={t('common.recurringDiscount.defaultPercent')}
          help={t('common.recurringDiscount.defaultHelp')}
        >
          {(props) => (
            <input
              {...props}
              type="number"
              min={0}
              max={100}
              value={value.default_percent}
              onChange={(event) => set({ default_percent: Number(event.target.value) })}
            />
          )}
        </Field>
      </div>
      {registry.error ? (
        <ErrorState error={registry.error} onRetry={() => void registry.refetch()} />
      ) : null}
      <p className="muted">{t('common.recurringDiscount.percentHelp')}</p>
      {value.periods.map((row, index) => {
        const allDay = row.start_time === '00:00' && row.end_time === '24:00';
        return (
          <fieldset key={index} className="nb-recurring-discount__period">
            <legend>{t('common.recurringDiscount.period', { number: index + 1 })}</legend>
            <fieldset className="nb-recurring-discount__weekdays">
              <legend>{t('common.recurringDiscount.weekdays')}</legend>
              {weekdayKeys.map((key, day) => (
                <label key={key}>
                  <input
                    type="checkbox"
                    checked={row.weekdays.includes(day + 1)}
                    onChange={(event) =>
                      update(index, {
                        weekdays: (event.target.checked
                          ? [...row.weekdays, day + 1]
                          : row.weekdays.filter((value) => value !== day + 1)
                        ).sort((a, b) => a - b),
                      })
                    }
                  />
                  {t(key)}
                </label>
              ))}
            </fieldset>
            <label className="nb-inline">
              <input
                type="checkbox"
                checked={allDay}
                onChange={(event) => {
                  if (event.target.checked)
                    update(index, { start_time: '00:00', end_time: '24:00' }, true);
                  else
                    update(
                      index,
                      previousTimes.current.get(row) ?? { start_time: '09:00', end_time: '18:00' },
                    );
                }}
              />
              {t('common.recurringDiscount.allDay')}
            </label>
            <div className="nb-grid nb-grid--3">
              <Field label={t('common.recurringDiscount.start')}>
                {(props) => (
                  <input
                    {...props}
                    type="time"
                    step={60}
                    value={row.start_time}
                    aria-invalid={!validPeriodTime(row.start_time)}
                    onChange={(event) => update(index, { start_time: event.target.value })}
                  />
                )}
              </Field>
              <Field label={t('common.recurringDiscount.end')}>
                {(props) => (
                  <div className="nb-form">
                    <input
                      {...props}
                      type="time"
                      step={60}
                      value={row.end_time === '24:00' ? '00:00' : row.end_time}
                      disabled={row.end_time === '24:00'}
                      aria-invalid={!validPeriodTime(row.end_time, true)}
                      onChange={(event) => update(index, { end_time: event.target.value })}
                    />
                    <label className="nb-inline">
                      <input
                        type="checkbox"
                        checked={row.end_time === '24:00'}
                        onChange={(event) =>
                          update(index, { end_time: event.target.checked ? '24:00' : '23:59' })
                        }
                      />
                      {t('common.recurringDiscount.endOfDay')}
                    </label>
                  </div>
                )}
              </Field>
              <Field label={t('common.recurringDiscount.percent')}>
                {(props) => (
                  <input
                    {...props}
                    type="number"
                    min={0}
                    max={100}
                    value={row.percent}
                    onChange={(event) => update(index, { percent: Number(event.target.value) })}
                  />
                )}
              </Field>
            </div>
            {row.end_time < row.start_time ? (
              <p className="muted">{t('common.recurringDiscount.overnight')}</p>
            ) : null}
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                set({ periods: value.periods.filter((_, i) => i !== index) });
              }}
            >
              {t('common.recurringDiscount.remove', { number: index + 1 })}
            </Button>
          </fieldset>
        );
      })}
      {!value.periods.length ? <p>{t('common.recurringDiscount.noPeriods')}</p> : null}
      <Button
        type="button"
        disabled={value.periods.length >= 16}
        onClick={() =>
          set({
            periods: [
              ...value.periods,
              { weekdays: [1, 2, 3, 4, 5], start_time: '09:00', end_time: '18:00', percent: 100 },
            ],
          })
        }
      >
        {t('common.recurringDiscount.add')}
      </Button>
      <p className="muted">{t('common.recurringDiscount.limits')}</p>
    </div>
  );
}
