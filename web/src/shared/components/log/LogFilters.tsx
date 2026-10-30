import { Button, Field, FilterBar } from '@shared/components/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { TimeInput } from '@shared/components/TimeInput';
import { TimeContextNotice } from '@shared/components/TimeContext';
import { createTimeDraft, timeDraftValue, type TimeDraft, type TimeStation } from '@shared/time';
import './logs.css';
import { validLogDiscordID } from './data';
import type { LogUrlState } from './useLogUrlState';

export interface LogFilterField {
  name: string;
  label: string;
  ariaLabel: string;
  inputType?: 'text' | 'number';
  placeholder?: string;
  maxLength?: number;
  suggestions?: readonly string[];
  options?: readonly { value: string; label: string }[];
}

const QUICK_RANGES = [
  { key: 'logs.range1h', seconds: 3600 },
  { key: 'logs.range24h', seconds: 86_400 },
  { key: 'logs.range7d', seconds: 7 * 86_400 },
] as const;

type FilterState = Pick<LogUrlState, 'filters' | 'fromUnix' | 'toUnix'>;
interface LogFiltersProps {
  station: TimeStation;
  fields: readonly LogFilterField[];
  state: FilterState;
  onApply: (next: FilterState) => void;
}

export function LogFilters({ station, fields, state, onApply }: LogFiltersProps) {
  const { t } = useTranslation();
  const [drafts, setDrafts] = useState<Record<string, string>>(() => ({ ...state.filters }));
  const [draftFrom, setDraftFrom] = useState<TimeDraft>(() =>
    createTimeDraft(state.fromUnix ?? null),
  );
  const [draftTo, setDraftTo] = useState<TimeDraft>(() => createTimeDraft(state.toUnix ?? null));
  const [invalidRange, setInvalidRange] = useState(false);
  const [invalidDiscord, setInvalidDiscord] = useState(false);
  const seed = (value: FilterState) =>
    JSON.stringify([
      Object.entries(value.filters).sort(([a], [b]) => a.localeCompare(b)),
      value.fromUnix,
      value.toUnix,
    ]);
  const stateSeed = seed(state);
  const [seededFor, setSeededFor] = useState(stateSeed);
  // Own changes reach the router only after intermediate renders. Track the
  // applied seed so those renders do not wipe unfinished drafts or validation
  // alerts, while navigation from elsewhere still reseeds every draft.
  const [pendingOwnSeed, setPendingOwnSeed] = useState<string | null>(null);
  if (pendingOwnSeed !== null && stateSeed === pendingOwnSeed) {
    setPendingOwnSeed(null);
    if (seededFor !== stateSeed) setSeededFor(stateSeed);
  } else if (seededFor !== stateSeed) {
    setPendingOwnSeed(null);
    setSeededFor(stateSeed);
    setInvalidDiscord(false);
    setInvalidRange(false);
    setDrafts({ ...state.filters });
    setDraftFrom(createTimeDraft(state.fromUnix ?? null));
    setDraftTo(createTimeDraft(state.toUnix ?? null));
  }

  const apply = (next: FilterState) => {
    // Selects and quick ranges do not submit unfinished text searches.
    setPendingOwnSeed(seed(next));
    onApply(next);
  };
  const submit = () => {
    const filters: Record<string, string> = {};
    let badDiscord = false;
    let badRange = false;
    for (const field of fields) {
      const value = (drafts[field.name] ?? '').trim();
      if (field.name === 'discord_id' && value && !validLogDiscordID(value)) {
        badDiscord = true;
        continue;
      }
      if (field.name === 'status' && value && !/^[1-5][0-9]{2}$/.test(value)) {
        badRange = true;
        continue;
      }
      if (value) filters[field.name] = value.slice(0, field.maxLength ?? 512);
    }
    const from = timeDraftValue(draftFrom);
    const to = timeDraftValue(draftTo);
    const invalidTime =
      from === undefined || to === undefined || (from !== null && to !== null && from >= to);
    setInvalidDiscord(badDiscord);
    setInvalidRange(badRange || invalidTime);
    apply({
      filters,
      fromUnix: invalidTime ? state.fromUnix : (from ?? undefined),
      toUnix: invalidTime ? state.toUnix : (to ?? undefined),
    });
  };
  const reset = () => {
    setDrafts({});
    setDraftFrom(createTimeDraft(null));
    setDraftTo(createTimeDraft(null));
    setInvalidDiscord(false);
    setInvalidRange(false);
    apply({ filters: {}, fromUnix: undefined, toUnix: undefined });
  };
  const primaryNames = ['user_id', 'discord_id', 'model', 'charity_model'];
  const renderFields = (values: readonly LogFilterField[]) =>
    values.map((field) => (
      <Field
        key={field.name}
        label={field.label}
        size={field.options || field.inputType === 'number' ? 'short' : undefined}
      >
        {(props) => (
          <>
            {field.options ? (
              <select
                {...props}
                value={state.filters[field.name] ?? ''}
                aria-label={field.ariaLabel}
                onChange={(event) => {
                  const value = event.target.value;
                  setDrafts((old) => ({ ...old, [field.name]: value }));
                  const filters = { ...state.filters };
                  if (value) filters[field.name] = value;
                  else delete filters[field.name];
                  apply({ ...state, filters });
                }}
              >
                {field.options.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            ) : (
              <input
                {...props}
                type={field.inputType ?? 'text'}
                value={drafts[field.name] ?? ''}
                maxLength={field.maxLength ?? 512}
                inputMode={field.inputType === 'number' ? 'numeric' : undefined}
                list={field.suggestions?.length ? `log-filter-${field.name}-options` : undefined}
                onChange={(event) =>
                  setDrafts((old) => ({ ...old, [field.name]: event.target.value }))
                }
                aria-label={field.ariaLabel}
                placeholder={field.placeholder ?? field.label}
              />
            )}
            {field.suggestions?.length ? (
              <datalist id={`log-filter-${field.name}-options`}>
                {field.suggestions.map((option) => (
                  <option key={option} value={option} />
                ))}
              </datalist>
            ) : null}
          </>
        )}
      </Field>
    ));
  return (
    <form
      className="log-filters"
      data-testid="log-filters"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <div className="quick-ranges" role="group" aria-label={t('logs.quickRanges')}>
        {QUICK_RANGES.map((range) => (
          <Button
            key={range.key}
            type="button"
            variant="secondary"
            aria-pressed={
              state.fromUnix !== undefined &&
              state.toUnix !== undefined &&
              state.toUnix - state.fromUnix === range.seconds
            }
            onClick={() => {
              const now = Math.floor(Date.now() / 1000);
              setDraftFrom(createTimeDraft(now - range.seconds));
              setDraftTo(createTimeDraft(now));
              apply({ filters: state.filters, fromUnix: now - range.seconds, toUnix: now });
            }}
          >
            {t(range.key)}
          </Button>
        ))}
      </div>
      <FilterBar
        as="div"
        ariaLabel={t('common.filter')}
        activeCount={
          fields.filter((field) => !primaryNames.includes(field.name) && state.filters[field.name])
            .length + (state.fromUnix !== undefined || state.toUnix !== undefined ? 1 : 0)
        }
        onSubmit={submit}
        onReset={reset}
        search={renderFields(fields.filter((field) => primaryNames.includes(field.name)))}
        secondary={
          <>
            {renderFields(fields.filter((field) => !primaryNames.includes(field.name)))}
            <TimeInput
              station={station}
              label={t('common.from')}
              showZoneHint={false}
              draft={draftFrom}
              onChange={setDraftFrom}
              aria-label={t('common.filterFromAria')}
            />
            <TimeInput
              station={station}
              label={t('common.to')}
              showZoneHint={false}
              draft={draftTo}
              onChange={setDraftTo}
              aria-label={t('common.filterToAria')}
            />
          </>
        }
        chips={fields
          .filter((field) => state.filters[field.name])
          .map((field) => ({
            key: field.name,
            label: `${field.label}: ${field.options?.find((option) => option.value === state.filters[field.name])?.label ?? state.filters[field.name]}`,
            removeLabel: `${t('common.resetFilter')}: ${field.label}`,
            onRemove: () => {
              const filters = { ...state.filters };
              delete filters[field.name];
              setDrafts((old) => ({ ...old, [field.name]: '' }));
              apply({ ...state, filters });
            },
          }))}
      />
      <TimeContextNotice station={station} />
      {invalidDiscord ? (
        <p className="field-error" role="alert">
          {t('common.operations.logs.invalidDiscordFilter')}
        </p>
      ) : null}
      {invalidRange ? (
        <p className="field-error" role="alert">
          {t('common.operations.logs.filterInvalid')}
        </p>
      ) : null}
    </form>
  );
}
