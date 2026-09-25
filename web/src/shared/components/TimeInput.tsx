import { useEffect, useId, useRef, useState, type InputHTMLAttributes } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  browserOffsetMinutes,
  editTimeDraft,
  fetchTimeZones,
  fetchTimeContext,
  formatOffset,
  resolveFixedLocalTime,
  resolveLocalTime,
  rezoneTimeDraft,
  rezoneSiteTimeDraft,
  timeDraftKey,
  timeDraftLocal,
  timeDraftValue,
  timeContextQueryKey,
  type TimeDraft,
  type TimeDraftUpdate,
  type TimeStation,
} from '../time';
import { checkBrowserTimeZone, useBrowserTimeZone } from './useBrowserTimeZone';

interface TimeInputProps extends Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'type' | 'value' | 'defaultValue' | 'onChange' | 'step'
> {
  label?: string;
  station: TimeStation;
  draft: TimeDraft;
  onChange: (update: TimeDraftUpdate) => void;
  showZoneHint?: boolean;
}

/** Resolve edited wall clocks before enabling submission; preserve original instants. */
export function TimeInput({
  label,
  station,
  draft,
  onChange,
  onBlur,
  showZoneHint = true,
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
  const browserZone = useBrowserTimeZone();
  const context = useQuery({
    queryKey: timeContextQueryKey(station),
    queryFn: ({ signal }) => fetchTimeContext(station as Exclude<TimeStation, 'user'>, signal),
    enabled: station !== 'user',
    staleTime: 30_000,
    retry: false,
  });
  const registry = useQuery({
    queryKey: ['time-zones', station],
    queryFn: ({ signal }) => fetchTimeZones(station, signal),
    enabled: station === 'user',
    staleTime: Infinity,
    retry: false,
  });
  const zones = registry.data?.zones;
  const timeContext = station === 'user' ? undefined : context.data;
  useEffect(() => {
    if (station === 'user' && zones)
      change.current((current) =>
        rezoneTimeDraft(current, browserZone, !!browserZone && zones.includes(browserZone)),
      );
    else if (station !== 'user')
      change.current((current) =>
        rezoneSiteTimeDraft(
          current,
          context.isSuccess && timeContext ? timeContext.offset_minutes : null,
        ),
      );
  }, [browserZone, context.isSuccess, station, timeContext, zones]);

  const key = timeDraftKey(draft);
  const local = timeDraftLocal(draft);
  const compareAt = local
    ? Date.parse(`${local}Z`)
    : draft.originalEpoch === null
      ? undefined
      : draft.originalEpoch * 1000;
  const browserOffset = browserOffsetMinutes(browserZone, compareAt);
  const browserDiffers =
    station !== 'user' &&
    draft.siteOffsetMinutes !== null &&
    browserOffset !== null &&
    draft.siteOffsetMinutes !== browserOffset;
  const contextPending = station !== 'user' && !context.isSuccess;
  const contextFailed = station !== 'user' && context.isError;
  const pendingDraft =
    contextPending || contextFailed || timeDraftValue(draft, browserZone) === undefined;
  const fixedReady = context.isSuccess && draft.mode === 'site' && draft.siteOffsetMinutes !== null;
  const needsResolve =
    pendingDraft &&
    (draft.invalidInput || (draft.text !== '' && draft.text !== draft.originalText));
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (
      !needsResolve ||
      !local ||
      station !== 'user' ||
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

  useEffect(() => {
    if (station === 'user' || !fixedReady || !needsResolve || !local) return;
    try {
      const value = resolveFixedLocalTime(local, draft.siteOffsetMinutes!);
      change.current((current) =>
        timeDraftKey(current) === key
          ? { ...current, resolved: { key, value }, errorKey: undefined }
          : current,
      );
    } catch {
      change.current((current) =>
        timeDraftKey(current) === key ? { ...current, errorKey: key } : current,
      );
    }
  }, [draft.siteOffsetMinutes, fixedReady, key, local, needsResolve, station]);

  const fieldFailed = pendingDraft && (draft.errorKey === key || registry.isError);
  const failed = fieldFailed || contextFailed;
  const invalid = needsResolve && !local;
  const adjusted =
    draft.resolved?.key === key && draft.resolved.value.adjustment !== 'none'
      ? draft.resolved.value
      : undefined;
  const resolving =
    needsResolve &&
    !invalid &&
    !failed &&
    !draft.needsUTCConfirmation &&
    (station === 'user' ? !registry.isPending : fixedReady);
  const hasHint =
    showZoneHint ||
    draft.needsUTCConfirmation ||
    draft.siteOffsetChanged ||
    invalid ||
    fieldFailed ||
    resolving ||
    Boolean(adjusted);
  return (
    <span className="time-input-field">
      {label && <label htmlFor={inputId}>{label}</label>}
      <input
        {...inputProps}
        id={inputId}
        type="datetime-local"
        disabled={
          inputProps.disabled ||
          (station !== 'user' &&
            (!context.isSuccess || timeContext?.offset_minutes === null || !timeContext))
        }
        step={draft.precision === 'second' ? 1 : 60}
        value={draft.text}
        aria-describedby={[inputProps['aria-describedby'], hasHint ? hintId : undefined]
          .filter(Boolean)
          .join(' ')}
        aria-invalid={invalid || failed || draft.siteOffsetChanged || inputProps['aria-invalid']}
        onChange={(event) => {
          const text = event.target.value;
          const invalid = event.target.validity.badInput;
          onChange((current) => editTimeDraft(current, text, invalid));
        }}
        onBlur={(event) => {
          checkBrowserTimeZone();
          setRetry((value) => value + 1);
          onBlur?.(event);
        }}
      />
      {hasHint ? (
        <span id={hintId} className="field-help time-input-hint" aria-live="polite">
          {showZoneHint && (
            <span>
              {contextFailed
                ? t('common.time.contextFailed')
                : contextPending
                  ? t('common.time.contextLoading')
                  : draft.mode === 'site' && draft.siteOffsetMinutes === null
                    ? t('common.time.siteUnavailable')
                    : station === 'user' && draft.browserZone
                      ? t('common.time.localZone', { zone: draft.zone })
                      : t('common.time.zone', { zone: draft.zone })}
            </span>
          )}
          {showZoneHint && browserDiffers && <span>{t('common.time.siteBrowserNotice')}</span>}
          {showZoneHint && draft.zoneChanged && <span>{t('common.time.zoneChanged')}</span>}
          {draft.siteOffsetChanged && (
            <span role="alert">{t('common.time.siteOffsetChanged')}</span>
          )}
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
          {fieldFailed && (
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
          {showZoneHint && contextFailed && !fieldFailed && (
            <button
              type="button"
              className="btn btn-link"
              disabled={inputProps.disabled}
              onClick={() => void context.refetch()}
            >
              {t('common.retry')}
            </button>
          )}
          {resolving && <span>{t('common.time.resolving')}</span>}
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
      ) : null}
    </span>
  );
}
