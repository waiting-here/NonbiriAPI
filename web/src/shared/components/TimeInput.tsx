import {
  useEffect,
  useId,
  useRef,
  useState,
  useSyncExternalStore,
  type InputHTMLAttributes,
} from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  browserTimeZone,
  editTimeDraft,
  fetchTimeZones,
  formatOffset,
  resolveLocalTime,
  rezoneTimeDraft,
  timeDraftKey,
  timeDraftLocal,
  timeDraftValue,
  type TimeDraft,
  type TimeDraftUpdate,
  type TimeStation,
} from '../time';

const listeners = new Set<() => void>();
let observedZone = browserTimeZone();
let polling: ReturnType<typeof setInterval> | undefined;
function checkZone() {
  const zone = browserTimeZone();
  if (zone === observedZone) return;
  observedZone = zone;
  listeners.forEach((listener) => listener());
}
function subscribeZone(listener: () => void) {
  listeners.add(listener);
  if (listeners.size === 1) {
    window.addEventListener('focus', checkZone);
    window.addEventListener('pageshow', checkZone);
    document.addEventListener('visibilitychange', checkZone);
    polling = setInterval(checkZone, 30_000);
    checkZone();
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size) return;
    window.removeEventListener('focus', checkZone);
    window.removeEventListener('pageshow', checkZone);
    document.removeEventListener('visibilitychange', checkZone);
    clearInterval(polling);
  };
}

interface TimeInputProps extends Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'type' | 'value' | 'defaultValue' | 'onChange' | 'step'
> {
  label?: string;
  station: TimeStation;
  draft: TimeDraft;
  onChange: (update: TimeDraftUpdate) => void;
}

/** Resolve edited wall clocks before enabling submission; preserve original instants. */
export function TimeInput({
  label,
  station,
  draft,
  onChange,
  onBlur,
  ...inputProps
}: TimeInputProps) {
  const { t } = useTranslation();
  const hintId = useId();
  const generatedInputId = useId();
  const inputId = inputProps.id ?? generatedInputId;
  const change = useRef(onChange);
  useEffect(() => {
    change.current = onChange;
  }, [onChange]);
  const browserZone = useSyncExternalStore(subscribeZone, browserTimeZone, () => null);
  const registry = useQuery({
    queryKey: ['time-zones', station],
    queryFn: ({ signal }) => fetchTimeZones(station, signal),
    staleTime: Infinity,
    retry: false,
  });
  const zones = registry.data?.zones;
  useEffect(() => {
    if (zones)
      change.current((current) =>
        rezoneTimeDraft(current, browserZone, !!browserZone && zones.includes(browserZone)),
      );
  }, [browserZone, zones]);

  const key = timeDraftKey(draft);
  const local = timeDraftLocal(draft);
  const pendingDraft = timeDraftValue(draft, browserZone) === undefined;
  const needsResolve =
    pendingDraft &&
    (draft.invalidInput || (draft.text !== '' && draft.text !== draft.originalText));
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (
      !needsResolve ||
      !local ||
      !zones?.includes(draft.zone) ||
      draft.needsUTCConfirmation ||
      draft.browserZone !== browserZone
    )
      return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      void resolveLocalTime(station, local, draft.zone, controller.signal)
        .then((value) => {
          if (controller.signal.aborted) return;
          change.current((current) =>
            timeDraftKey(current) === key && current.browserZone === browserZone
              ? { ...current, resolved: { key, value }, errorKey: undefined }
              : current,
          );
        })
        .catch(() => {
          if (controller.signal.aborted) return;
          change.current((current) =>
            timeDraftKey(current) === key && current.browserZone === browserZone
              ? { ...current, errorKey: key }
              : current,
          );
        });
    }, 150);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [
    station,
    key,
    local,
    needsResolve,
    draft.zone,
    draft.browserZone,
    draft.needsUTCConfirmation,
    browserZone,
    zones,
    retry,
  ]);

  const failed = pendingDraft && (draft.errorKey === key || registry.isError);
  const invalid = needsResolve && !local;
  const adjusted =
    draft.resolved?.key === key && draft.resolved.value.adjustment !== 'none'
      ? draft.resolved.value
      : undefined;
  return (
    <span className="time-input-field">
      {label && <label htmlFor={inputId}>{label}</label>}
      <input
        {...inputProps}
        id={inputId}
        type="datetime-local"
        step={draft.precision === 'second' ? 1 : 60}
        value={draft.text}
        aria-describedby={[inputProps['aria-describedby'], hintId].filter(Boolean).join(' ')}
        aria-invalid={invalid || failed || inputProps['aria-invalid']}
        onChange={(event) => {
          const text = event.target.value;
          const invalid = event.target.validity.badInput;
          onChange((current) => editTimeDraft(current, text, invalid));
        }}
        onBlur={(event) => {
          checkZone();
          setRetry((value) => value + 1);
          onBlur?.(event);
        }}
      />
      <span id={hintId} className="field-help time-input-hint" aria-live="polite">
        <span>{t('common.time.zone', { zone: draft.zone })}</span>
        {draft.zoneChanged && <span>{t('common.time.zoneChanged')}</span>}
        {draft.needsUTCConfirmation && (
          <span>
            {t('common.time.fallback')}{' '}
            <button
              type="button"
              className="btn btn-link"
              disabled={inputProps.disabled}
              onClick={() => onChange((current) => ({ ...current, needsUTCConfirmation: false }))}
            >
              {t('common.time.confirmUTC')}
            </button>
          </span>
        )}
        {invalid && <span role="alert">{t('common.time.invalid')}</span>}
        {failed && (
          <span role="alert">
            {t('common.time.failed')}{' '}
            <button
              type="button"
              className="btn btn-link"
              disabled={inputProps.disabled}
              onClick={() => {
                if (registry.isError) void registry.refetch();
                setRetry((value) => value + 1);
              }}
            >
              {t('common.retry')}
            </button>
          </span>
        )}
        {pendingDraft && !invalid && !failed && !draft.needsUTCConfirmation && (
          <span>{t('common.time.resolving')}</span>
        )}
        {adjusted && (
          <span>
            {t(
              adjusted.adjustment === 'gap_shifted'
                ? 'common.time.gap_shifted'
                : 'common.time.fold_later',
              {
                local: adjusted.local.replace('T', ' '),
                zone: adjusted.time_zone,
                offset: formatOffset(adjusted.offset_seconds),
              },
            )}
          </span>
        )}
      </span>
    </span>
  );
}
