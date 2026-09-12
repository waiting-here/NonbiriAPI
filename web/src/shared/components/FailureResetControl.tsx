import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  captureStationSession,
  clearStationSession,
  isStationSessionChanged,
  stationSessionMatches,
  StationSessionChangedError,
} from '@shared/charityManagement';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { charityKeys, type CharityRole } from '@shared/operations/charity';
import {
  resetFailures,
  selectFailureResets,
  type FailureResetTarget,
  type FailureSelection,
} from '@shared/operations/failureReset';
import { FailureResetRun } from '@shared/operations/failureResetRun';
import { ErrorState } from './States';
import './FailureResetControl.css';

export interface FailureResetChoice {
  id: string;
  label: string;
  target: FailureResetTarget;
}
interface Props {
  role: CharityRole;
  selection: FailureSelection;
  choices: FailureResetChoice[];
  disabled?: boolean;
  onCapabilityLoss?: () => void;
}
const statusKey = {
  reset: 'common.failureReset.reset',
  conflict: 'common.failureReset.conflict',
  ineligible: 'common.failureReset.ineligible',
  not_found: 'common.failureReset.notFound',
} as const;
export function FailureResetControl({
  role,
  selection,
  choices,
  disabled,
  onCapabilityLoss,
}: Props) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [chosen, setChosen] = useState(new Map<string, FailureResetChoice>());
  const [job, setJob] = useState<FailureResetRun | null>(null);
  const [, render] = useState(0);
  const [lost, setLost] = useState(false);
  const active = useRef<FailureResetRun | null>(null);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      active.current?.stop();
    };
  }, []);
  const refresh = () => client.invalidateQueries({ queryKey: charityKeys.root(role) });
  const begin = (targets: FailureResetTarget[]) => {
    let snapshot: ReturnType<typeof captureStationSession>;
    try {
      snapshot = captureStationSession(client, role);
    } catch {
      setLost(true);
      onCapabilityLoss?.();
      return;
    }
    const guard = () => {
      if (!mounted.current || !stationSessionMatches(client, role, snapshot))
        throw new StationSessionChangedError();
    };
    const request = async <T,>(send: () => Promise<T>) => {
      guard();
      try {
        const result = await send();
        guard();
        return result;
      } catch (error) {
        guard();
        if (isUnauthorized(error) || isForbidden(error)) {
          clearStationSession(client, role);
          setLost(true);
          onCapabilityLoss?.();
        }
        throw error;
      }
    };
    const run = new FailureResetRun(targets, {
      select: (query, cursor) => request(() => selectFailureResets(role, query, cursor)),
      reset: (items, key) => request(() => resetFailures(role, items, key)),
      guard,
      yield: () => new Promise((resolve) => setTimeout(resolve, 0)),
      progress: () => {
        if (!mounted.current) return;
        if (isStationSessionChanged(run.error) || !stationSessionMatches(client, role, snapshot)) {
          active.current = null;
          setJob(null);
          setChosen(new Map());
          return;
        }
        render((n) => n + 1);
        if (run.phase === 'done') void refresh();
      },
    });
    active.current = run;
    setJob(run);
    void run.resume();
  };
  const working = job?.phase === 'selecting' || job?.phase === 'resetting';
  const counts = job?.counts;
  const failed = [];
  if (job && !working) {
    for (const entry of job.entries.values()) {
      if (entry.status && entry.status !== 'reset') failed.push(entry);
      if (failed.length === 100) break;
    }
  }
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <details className="failure-reset-control">
      <summary>{t('common.failureReset.title')}</summary>
      <p>{t('common.failureReset.help')}</p>
      {!job ? (
        <>
          <div className="form-actions">
            <button
              type="button"
              className="btn btn-quiet"
              disabled={disabled || !choices.length}
              onClick={() =>
                setChosen(
                  (previous) =>
                    new Map([...previous, ...choices.map((item) => [item.id, item] as const)]),
                )
              }
            >
              {t('common.failureReset.selectPage')}
            </button>
            <button
              type="button"
              className="btn btn-quiet"
              disabled={!chosen.size}
              onClick={() => setChosen(new Map())}
            >
              {t('common.failureReset.clear')}
            </button>
          </div>
          <div className="failure-reset-choices">
            {choices.map((choice) => (
              <label key={choice.id}>
                <input
                  type="checkbox"
                  disabled={disabled}
                  checked={chosen.has(choice.id)}
                  onChange={(event) =>
                    setChosen((previous) => {
                      const next = new Map(previous);
                      if (event.target.checked) next.set(choice.id, choice);
                      else next.delete(choice.id);
                      return next;
                    })
                  }
                />
                <span>{choice.label}</span>
              </label>
            ))}
          </div>
          <div className="form-actions">
            <button
              type="button"
              className="btn btn-secondary"
              disabled={disabled || !chosen.size}
              onClick={() => begin(Array.from(chosen.values(), (choice) => choice.target))}
            >
              {t('common.failureReset.selected', { count: chosen.size })}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={disabled}
              onClick={() => begin([selection])}
            >
              {t('common.failureReset.all')}
            </button>
          </div>
        </>
      ) : (
        <>
          <p role="status" aria-live="polite">
            {t(
              job.phase === 'selecting'
                ? 'common.failureReset.selecting'
                : job.phase === 'resetting'
                  ? 'common.failureReset.resetting'
                  : job.phase === 'paused'
                    ? 'common.failureReset.paused'
                    : 'common.failureReset.done',
              counts!,
            )}
          </p>
          {job.error ? <ErrorState error={job.error} /> : null}
          {job.phase === 'paused' ? <p>{t('common.failureReset.resumeHelp')}</p> : null}
          <div className="form-actions">
            {working ? (
              <button type="button" className="btn btn-quiet" onClick={() => job.stop()}>
                {t('common.cancel')}
              </button>
            ) : null}
            {job.phase === 'paused' ? (
              <button type="button" className="btn btn-secondary" onClick={() => void job.resume()}>
                {t('common.failureReset.resume')}
              </button>
            ) : null}
            {!working ? (
              <button
                type="button"
                className="btn btn-quiet"
                onClick={() => {
                  active.current = null;
                  setJob(null);
                  setChosen(new Map());
                  void refresh();
                }}
              >
                {t('common.failureReset.newSelection')}
              </button>
            ) : null}
          </div>
          {counts && counts.skipped > 0 ? (
            <p>{t('common.failureReset.skippedHelp', { count: counts.skipped })}</p>
          ) : null}
          {failed.length ? (
            <ul>
              {failed.map((item) => (
                <li key={item.key_id}>
                  {t('common.failureReset.item', { donation: item.donation_id, key: item.key_id })}{' '}
                  · {t(statusKey[item.status!])}
                </li>
              ))}
            </ul>
          ) : null}
        </>
      )}
    </details>
  );
}
