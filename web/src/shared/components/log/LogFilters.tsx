import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { TimeInput } from '@shared/components/TimeInput';
import { createTimeDraft, timeDraftValue, type TimeDraft, type TimeStation } from '@shared/time';
import type { LogUrlState } from './useLogUrlState';

// Shared filter bar for both log screens. Text fields are declared by the
// caller (the two stations expose different frozen filter sets); the time
// range offers quick presets plus explicit time inputs. Draft
// inputs only reach the URL/API through Apply, and every applied change is
// expected to restart paging at page 1 (callers own that rule).

export interface LogFilterField {
  /** Query parameter name; also the key inside LogUrlState.filters. */
  name: string;
  label: string;
  ariaLabel: string;
  inputType?: 'text' | 'number';
  placeholder?: string;
  maxLength?: number;
  /** Optional bounded candidate list rendered as a datalist. */
  suggestions?: readonly string[];
}

/** One quick range preset: the label key plus its window in seconds. */
const QUICK_RANGES = [
  { key: 'logs.range1h', seconds: 3600 },
  { key: 'logs.range24h', seconds: 86_400 },
  { key: 'logs.range7d', seconds: 7 * 86_400 },
] as const;

interface LogFiltersProps {
  station: TimeStation;
  fields: readonly LogFilterField[];
  state: Pick<LogUrlState, 'filters' | 'fromUnix' | 'toUnix'>;
  onApply: (next: { filters: Record<string, string>; fromUnix?: number; toUnix?: number }) => void;
}

export function LogFilters({ station, fields, state, onApply }: LogFiltersProps) {
  const { t } = useTranslation();
  const [drafts, setDrafts] = useState<Record<string, string>>(() => ({ ...state.filters }));
  const [draftFrom, setDraftFrom] = useState<TimeDraft>(() =>
    createTimeDraft(state.fromUnix ?? null),
  );
  const [draftTo, setDraftTo] = useState<TimeDraft>(() => createTimeDraft(state.toUnix ?? null));
  const [invalidRange, setInvalidRange] = useState(false);

  // Re-seed the drafts whenever the applied state changes (including when a
  // quick range or another navigation rewrites the URL). Adjusting state
  // during render keeps the seed comparison in the same commit.
  const stateSeed = JSON.stringify([
    state.filters,
    state.fromUnix === undefined ? 'unset' : state.fromUnix,
    state.toUnix === undefined ? 'unset' : state.toUnix,
  ]);
  const [seededFor, setSeededFor] = useState(stateSeed);
  if (seededFor !== stateSeed) {
    setSeededFor(stateSeed);
    setInvalidRange(false);
    setDrafts({ ...state.filters });
    setDraftFrom(createTimeDraft(state.fromUnix ?? null));
    setDraftTo(createTimeDraft(state.toUnix ?? null));
  }
  const fromValue = timeDraftValue(draftFrom);
  const toValue = timeDraftValue(draftTo);
  const timeReady = fromValue !== undefined && toValue !== undefined;

  const collectFilters = (): Record<string, string> => {
    const next: Record<string, string> = {};
    for (const field of fields) {
      const value = (drafts[field.name] ?? '').trim();
      if (value) next[field.name] = value.slice(0, field.maxLength ?? 512);
    }
    return next;
  };

  const applyRange = (fromUnix: number | undefined, toUnix: number | undefined) => {
    setInvalidRange(false);
    onApply({ filters: collectFilters(), fromUnix, toUnix });
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const from = timeDraftValue(draftFrom);
    const to = timeDraftValue(draftTo);
    if (from === undefined || to === undefined) {
      setInvalidRange(true);
      return;
    }
    if (from !== null && to !== null && from >= to) {
      setInvalidRange(true);
      return;
    }
    const status = drafts.status?.trim();
    if (status && !/^[1-5][0-9]{2}$/.test(status)) {
      setInvalidRange(true);
      return;
    }
    applyRange(from === null ? undefined : from, to === null ? undefined : to);
  };

  const reset = () => {
    setInvalidRange(false);
    onApply({ filters: {}, fromUnix: undefined, toUnix: undefined });
  };

  return (
    <form
      className="filter-bar"
      onSubmit={submit}
      aria-label={t('common.filter')}
      data-testid="log-filters"
    >
      {fields.map((field) => (
        <label key={field.name}>
          <span>{field.label}</span>
          <input
            type={field.inputType ?? 'text'}
            value={drafts[field.name] ?? ''}
            maxLength={field.maxLength ?? 512}
            inputMode={field.inputType === 'number' ? 'numeric' : undefined}
            list={field.suggestions?.length ? `log-filter-${field.name}-options` : undefined}
            onChange={(event) =>
              setDrafts((prev) => ({ ...prev, [field.name]: event.target.value }))
            }
            aria-label={field.ariaLabel}
            placeholder={field.placeholder}
          />
          {field.suggestions?.length ? (
            <datalist id={`log-filter-${field.name}-options`}>
              {field.suggestions.map((option) => (
                <option key={option} value={option} />
              ))}
            </datalist>
          ) : null}
        </label>
      ))}
      <div className="quick-ranges" role="group" aria-label={t('logs.quickRanges')}>
        {QUICK_RANGES.map((range) => (
          <button
            key={range.key}
            type="button"
            className="btn btn-secondary"
            onClick={() => {
              const now = Math.floor(Date.now() / 1000);
              applyRange(now - range.seconds, now);
            }}
          >
            {t(range.key)}
          </button>
        ))}
      </div>
      <TimeInput
        station={station}
        label={t('common.from')}
        draft={draftFrom}
        onChange={setDraftFrom}
        aria-label={t('common.filterFromAria')}
      />
      <TimeInput
        station={station}
        label={t('common.to')}
        draft={draftTo}
        onChange={setDraftTo}
        aria-label={t('common.filterToAria')}
      />
      {invalidRange ? (
        <p className="field-error" role="alert">
          {t('common.operations.logs.filterInvalid')}
        </p>
      ) : null}
      <div className="filter-actions">
        <button type="submit" className="btn btn-quiet" disabled={!timeReady}>
          {t('common.applyFilter')}
        </button>
        <button type="button" className="btn btn-link" onClick={reset}>
          {t('common.resetFilter')}
        </button>
      </div>
    </form>
  );
}
