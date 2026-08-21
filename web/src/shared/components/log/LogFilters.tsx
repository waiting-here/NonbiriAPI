import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import type { LogUrlState } from './useLogUrlState';

// Shared filter bar for both log screens. Text fields are declared by the
// caller (the two stations expose different frozen filter sets); the time
// range offers quick presets plus explicit datetime-local inputs. Draft
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

function datetimeLocalToUnix(value: string): number | undefined {
  if (!value.trim()) return undefined;
  const millis = Date.parse(value);
  if (!Number.isFinite(millis)) return undefined;
  return Math.max(0, Math.floor(millis / 1000));
}

function unixToDatetimeLocal(unix: number | undefined): string {
  if (unix === undefined || unix <= 0) return '';
  const date = new Date(unix * 1000);
  if (Number.isNaN(date.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

interface LogFiltersProps {
  fields: readonly LogFilterField[];
  state: Pick<LogUrlState, 'filters' | 'fromUnix' | 'toUnix'>;
  onApply: (next: { filters: Record<string, string>; fromUnix?: number; toUnix?: number }) => void;
}

export function LogFilters({ fields, state, onApply }: LogFiltersProps) {
  const { t } = useTranslation();
  const [drafts, setDrafts] = useState<Record<string, string>>(() => ({ ...state.filters }));
  const [draftFrom, setDraftFrom] = useState(() => unixToDatetimeLocal(state.fromUnix));
  const [draftTo, setDraftTo] = useState(() => unixToDatetimeLocal(state.toUnix));

  // Re-seed the drafts whenever the applied state changes (including when a
  // quick range or another navigation rewrites the URL). Adjusting state
  // during render keeps the seed comparison in the same commit.
  const stateSeed = JSON.stringify([state.filters, state.fromUnix ?? 0, state.toUnix ?? 0]);
  const [seededFor, setSeededFor] = useState(stateSeed);
  if (seededFor !== stateSeed) {
    setSeededFor(stateSeed);
    setDrafts({ ...state.filters });
    setDraftFrom(unixToDatetimeLocal(state.fromUnix));
    setDraftTo(unixToDatetimeLocal(state.toUnix));
  }

  const collectFilters = (): Record<string, string> => {
    const next: Record<string, string> = {};
    for (const field of fields) {
      const value = (drafts[field.name] ?? '').trim();
      if (value) next[field.name] = value.slice(0, field.maxLength ?? 512);
    }
    return next;
  };

  const applyRange = (fromUnix: number | undefined, toUnix: number | undefined) => {
    onApply({ filters: collectFilters(), fromUnix, toUnix });
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    applyRange(datetimeLocalToUnix(draftFrom), datetimeLocalToUnix(draftTo));
  };

  const reset = () => {
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
      <label>
        <span>{t('common.from')}</span>
        <input
          type="datetime-local"
          value={draftFrom}
          onChange={(event) => setDraftFrom(event.target.value)}
          aria-label={t('common.filterFromAria')}
        />
      </label>
      <label>
        <span>{t('common.to')}</span>
        <input
          type="datetime-local"
          value={draftTo}
          onChange={(event) => setDraftTo(event.target.value)}
          aria-label={t('common.filterToAria')}
        />
      </label>
      <div className="filter-actions">
        <button type="submit" className="btn btn-quiet">
          {t('common.applyFilter')}
        </button>
        <button type="button" className="btn btn-link" onClick={reset}>
          {t('common.resetFilter')}
        </button>
      </div>
    </form>
  );
}
