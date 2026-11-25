import { SuggestInput } from '@shared/components/ui/SuggestInput';
import { Button, Field, FilterBar } from '@shared/components/ui';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { TimeInput } from '@shared/components/TimeInput';
import { TimeContextNotice } from '@shared/components/TimeContext';
import { createTimeDraft, timeDraftValue, type TimeDraft, type TimeStation } from '@shared/time';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import './logs.css';
import { logRequestID, validLogDiscordID, validLogResourceID } from './data';
import type { LogUrlState } from './useLogUrlState';

export interface LogFilterField {
  name: string;
  label: string;
  ariaLabel: string;
  inputMode?: 'numeric';
  placeholder?: string;
  maxLength?: number;
  suggestions?: readonly string[];
  options?: readonly { value: string; label: string }[];
}

const QUICK_RANGES = [
  { key: 'common.logRange15m', value: '15m' },
  { key: 'logs.range1h', value: '1h' },
  { key: 'logs.range24h', value: '24h' },
  { key: 'logs.range7d', value: '7d' },
] as const;

type FilterState = Pick<LogUrlState, 'filters' | 'fromUnix' | 'toUnix' | 'range'>;
interface LogFiltersProps {
  station: TimeStation;
  fields: readonly LogFilterField[];
  state: FilterState;
  onApply: (next: FilterState, keepFilters?: readonly string[]) => void;
  linkErrors?: Record<string, string>;
  onResetLookup?: () => void;
  toolbarActions?: ReactNode;
}

export function LogFilters({
  station,
  fields,
  state,
  onApply,
  linkErrors,
  onResetLookup,
  toolbarActions,
}: LogFiltersProps) {
  const { t } = useTranslation();
  const formatDateTime = useDateTimeFormatter();
  const form = useRef<HTMLFormElement>(null);
  const keyLinkError = Boolean(linkErrors?.endpoint_key_id);
  useEffect(() => {
    if (!keyLinkError) return;
    const field = form.current?.elements.namedItem('endpoint_key_id');
    const disclosure = field instanceof HTMLElement && field.closest('details');
    if (disclosure) disclosure.open = true;
  }, [keyLinkError]);
  const [drafts, setDrafts] = useState<Record<string, string>>(() => ({ ...state.filters }));
  const [draftFrom, setDraftFrom] = useState<TimeDraft>(() =>
    createTimeDraft(state.fromUnix ?? null),
  );
  const [draftTo, setDraftTo] = useState<TimeDraft>(() => createTimeDraft(state.toUnix ?? null));
  const [submitted, setSubmitted] = useState(false);
  const seed = (value: FilterState) =>
    JSON.stringify([
      Object.entries(value.filters).sort(([a], [b]) => a.localeCompare(b)),
      value.fromUnix,
      value.toUnix,
      value.range,
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
    setSubmitted(false);
    setDrafts({ ...state.filters });
    setDraftFrom(createTimeDraft(state.fromUnix ?? null));
    setDraftTo(createTimeDraft(state.toUnix ?? null));
  }

  const apply = (next: FilterState, partial = false) => {
    // Selects and quick ranges do not submit unfinished text searches.
    setPendingOwnSeed(seed(next));
    // Keep malformed deep-link IDs on unrelated edits until the operator repairs or resets them.
    if (partial && linkErrors) onApply(next, Object.keys(linkErrors));
    else onApply(next);
  };
  const readDraft = () => {
    const filters: Record<string, string> = {};
    const errors: Record<string, string> = {};
    for (const field of fields) {
      const value = (drafts[field.name] ?? '').trim();
      if (!value) {
        if (linkErrors?.[field.name]) errors[field.name] = linkErrors[field.name];
        continue;
      }
      if (field.name === 'discord_id' && !validLogDiscordID(value))
        errors[field.name] = t('common.operations.logs.invalidDiscordFilter');
      if (
        (field.name === 'user_id' || field.name === 'endpoint_key_id') &&
        !validLogResourceID(value)
      )
        errors[field.name] = t('logs.invalidNumericID');
      if (field.name === 'status' && !/^[1-5][0-9]{2}$/.test(value))
        errors[field.name] = t('logs.invalidStatus');
      filters[field.name] = value;
    }
    const from = timeDraftValue(draftFrom);
    const to = timeDraftValue(draftTo);
    if (from === undefined) errors.from = t('logs.checkTime');
    else if (to === undefined) errors.to = t('logs.checkTime');
    else if (from !== null && to !== null && from >= to) errors.to = t('logs.invalidTimeRange');
    return { filters, from, to, errors };
  };
  const submit = () => {
    const { filters, from, to, errors } = readDraft();
    if (Object.keys(errors).length) {
      setSubmitted(true);
      const invalid = Array.from(form.current?.elements ?? []).find(
        (control): control is HTMLInputElement =>
          control instanceof HTMLInputElement && Boolean(errors[control.name]),
      );
      const disclosure = invalid?.closest('details');
      if (disclosure) disclosure.open = true;
      if (invalid?.disabled) disclosure?.querySelector('summary')?.focus();
      else invalid?.focus();
      return;
    }
    setSubmitted(false);
    setDrafts(filters);
    apply({
      filters,
      fromUnix: from ?? undefined,
      toUnix: to ?? undefined,
      range: from === null && to === null ? state.range : undefined,
    });
  };
  const reset = () => {
    onResetLookup?.();
    setDrafts({});
    setDraftFrom(createTimeDraft(null));
    setDraftTo(createTimeDraft(null));
    setSubmitted(false);
    apply({ filters: {}, fromUnix: undefined, toUnix: undefined, range: undefined });
  };
  const primaryNames = ['outcome', 'operation', 'discord_id', 'model', 'charity_model'];
  const errors: Record<string, string> = submitted ? readDraft().errors : {};
  for (const [name, error] of Object.entries(linkErrors ?? {})) {
    if (!drafts[name]?.trim()) errors[name] = error;
  }
  const pendingFields = fields
    .filter(
      (field) =>
        !field.options && (drafts[field.name] ?? '').trim() !== (state.filters[field.name] ?? ''),
    )
    .map((field) => field.label);
  const timeBounds = [
    {
      name: 'from',
      label: t('common.from'),
      ariaLabel: t('common.filterFromAria'),
      draft: draftFrom,
      setDraft: setDraftFrom,
      epoch: state.fromUnix,
      key: 'fromUnix',
    },
    {
      name: 'to',
      label: t('common.to'),
      ariaLabel: t('common.filterToAria'),
      draft: draftTo,
      setDraft: setDraftTo,
      epoch: state.toUnix,
      key: 'toUnix',
    },
  ] as const;
  for (const bound of timeBounds) {
    const value =
      !bound.draft.invalidInput && bound.draft.text === bound.draft.originalText
        ? bound.draft.originalEpoch
        : timeDraftValue(bound.draft);
    if (value !== (bound.epoch ?? null)) pendingFields.push(bound.label);
  }
  const renderFields = (values: readonly LogFilterField[]) =>
    values.map((field) => (
      <Field
        key={field.name}
        label={field.label}
        size={field.options || field.inputMode === 'numeric' ? 'short' : undefined}
        error={errors[field.name]}
      >
        {(props) => (
          <>
            {field.options ? (
              <select
                {...props}
                name={field.name}
                value={state.filters[field.name] ?? ''}
                aria-label={field.ariaLabel}
                onChange={(event) => {
                  const value = event.target.value;
                  setDrafts((old) => ({ ...old, [field.name]: value }));
                  const filters = { ...state.filters };
                  if (value) filters[field.name] = value;
                  else delete filters[field.name];
                  apply({ ...state, filters }, true);
                }}
              >
                {field.options.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            ) : field.suggestions?.length ? (
              <SuggestInput
                {...props}
                name={field.name}
                value={drafts[field.name] ?? ''}
                suggestions={field.suggestions}
                maxLength={field.maxLength ?? 512}
                onChange={(value) => setDrafts((old) => ({ ...old, [field.name]: value }))}
                aria-label={field.ariaLabel}
                placeholder={field.placeholder ?? field.label}
              />
            ) : (
              <input
                {...props}
                type="text"
                name={field.name}
                value={drafts[field.name] ?? ''}
                maxLength={field.inputMode === 'numeric' ? undefined : (field.maxLength ?? 512)}
                inputMode={field.inputMode}
                onChange={(event) =>
                  setDrafts((old) => ({ ...old, [field.name]: event.target.value }))
                }
                aria-label={field.ariaLabel}
                placeholder={field.placeholder ?? field.label}
              />
            )}
          </>
        )}
      </Field>
    ));
  return (
    <form
      ref={form}
      noValidate
      className="log-filters"
      data-testid="log-filters"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <div className="log-filters__toolbar">
        <div className="quick-ranges" role="group" aria-label={t('logs.quickRanges')}>
          {QUICK_RANGES.map((range) => (
            <Button
              key={range.key}
              type="button"
              variant="secondary"
              size="sm"
              aria-pressed={state.range === range.value}
              onClick={() => {
                setDraftFrom(createTimeDraft(null));
                setDraftTo(createTimeDraft(null));
                apply(
                  {
                    filters: state.filters,
                    fromUnix: undefined,
                    toUnix: undefined,
                    range: range.value,
                  },
                  true,
                );
              }}
            >
              {t(range.key)}
            </Button>
          ))}
        </div>
        {toolbarActions}
      </div>
      {state.range && <p className="nb-small">{t('common.logRolling')}</p>}
      <FilterBar
        onReset={reset}
        as="div"
        ariaLabel={t('common.filter')}
        activeCount={
          fields.filter((field) => !primaryNames.includes(field.name) && state.filters[field.name])
            .length + (state.fromUnix !== undefined || state.toUnix !== undefined ? 1 : 0)
        }
        onSubmit={submit}
        search={renderFields(fields.filter((field) => primaryNames.includes(field.name)))}
        secondary={
          <>
            {renderFields(fields.filter((field) => !primaryNames.includes(field.name)))}
            {timeBounds.map((bound) => (
              <Field key={bound.name} label={bound.label} error={errors[bound.name]}>
                {(props) => (
                  <TimeInput
                    {...props}
                    name={bound.name}
                    station={station}
                    showZoneHint={false}
                    draft={bound.draft}
                    onChange={bound.setDraft}
                    aria-label={bound.ariaLabel}
                  />
                )}
              </Field>
            ))}
          </>
        }
        footer={
          <div className="log-filters__actions">
            <span className="log-filters__pending" role="status">
              {pendingFields.length > 0 &&
                t('logs.applyEdits', { fields: pendingFields.join(' · ') })}
            </span>
          </div>
        }
        chipsLabel={t('logs.appliedFilters')}
        chips={[
          ...fields
            .filter((field) => state.filters[field.name])
            .map((field) => ({
              key: field.name,
              label: `${field.label}: ${field.options?.find((option) => option.value === state.filters[field.name])?.label ?? state.filters[field.name]}`,
              removeLabel: t('logs.removeFilter', { field: field.label }),
              onRemove: () => {
                const filters = { ...state.filters };
                delete filters[field.name];
                setDrafts((old) => ({ ...old, [field.name]: '' }));
                apply({ ...state, filters }, true);
              },
            })),
          ...timeBounds.flatMap((bound) =>
            bound.epoch === undefined
              ? []
              : [
                  {
                    key: bound.name,
                    label: `${bound.label}: ${formatDateTime(bound.epoch)}`,
                    removeLabel: t('logs.removeFilter', { field: bound.label }),
                    onRemove: () => {
                      bound.setDraft(createTimeDraft(null));
                      apply({ ...state, [bound.key]: undefined }, true);
                    },
                  },
                ],
          ),
        ]}
      />
      <TimeContextNotice station={station} />
    </form>
  );
}

export function LogSearch({
  onOpenRequest,
  children,
  resetKey = 0,
}: {
  onOpenRequest: (id: string) => void;
  children: ReactNode;
  resetKey?: number;
}) {
  const { t } = useTranslation();
  const [lookup, setLookup] = useState('');
  const [lookupInvalid, setLookupInvalid] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const [previous, setPrevious] = useState(resetKey);
  if (previous !== resetKey) {
    setPrevious(resetKey);
    setLookup('');
    setLookupInvalid(false);
  }
  return (
    <>
      <form
        className="log-lookup"
        data-inspect-trigger=""
        onSubmit={(event) => {
          event.preventDefault();
          const id = logRequestID(lookup.trim());
          setLookupInvalid(!id);
          if (id) onOpenRequest(id);
          else input.current?.focus();
        }}
      >
        <Field
          label={t('logs.lookupLabel')}
          error={lookupInvalid ? t('logs.invalidLookup') : undefined}
        >
          {(props) => (
            <input
              {...props}
              ref={input}
              className="mono"
              autoCapitalize="none"
              autoComplete="off"
              enterKeyHint="go"
              value={lookup}
              placeholder="req_…"
              spellCheck={false}
              onChange={(event) => {
                setLookup(event.target.value);
                setLookupInvalid(false);
              }}
            />
          )}
        </Field>
        <Button type="submit" variant="primary">
          {t('logs.openRequest')}
        </Button>
      </form>
      {children}
    </>
  );
}
