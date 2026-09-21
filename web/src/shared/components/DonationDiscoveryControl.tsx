import { useEffect, useId, useRef, useState } from 'react';
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
  readDonationDiscovery,
  selectDonationDiscoveries,
  startDonationDiscovery,
  type DiscoveryTarget,
} from '@shared/operations/donationDiscovery';
import { DonationDiscoveryRun } from '@shared/operations/donationDiscoveryRun';
import { ErrorState } from './States';
import './DonationDiscoveryControl.css';

interface Props {
  role: CharityRole;
  target: DiscoveryTarget;
  disabled?: boolean;
  onCapabilityLoss?: () => void;
}
export function DonationDiscoveryControl({ role, target, disabled, onCapabilityLoss }: Props) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const id = useId();
  const [confirming, setConfirming] = useState(false);
  const [job, setJob] = useState<DonationDiscoveryRun | null>(null);
  const [lost, setLost] = useState(false);
  const [, render] = useState(0);
  const active = useRef<DonationDiscoveryRun | null>(null);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      active.current?.stop();
    };
  }, []);
  const begin = () => {
    let session: ReturnType<typeof captureStationSession>;
    try {
      session = captureStationSession(client, role);
    } catch {
      setLost(true);
      onCapabilityLoss?.();
      return;
    }
    const guard = () => {
      if (!mounted.current || !stationSessionMatches(client, role, session))
        throw new StationSessionChangedError();
    };
    const send = async <T,>(operation: () => Promise<T>): Promise<T> => {
      guard();
      try {
        const result = await operation();
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
    const run = new DonationDiscoveryRun(target, {
      select: (donationID, cursor) =>
        send(() => selectDonationDiscoveries(role, donationID, cursor)),
      start: (item, key) => send(() => startDonationDiscovery(role, item, key)),
      read: (item) => send(() => readDonationDiscovery(role, item)),
      guard,
      wait: () => new Promise((resolve) => setTimeout(resolve, 1500)),
      progress: () => {
        if (!mounted.current) return;
        if (isStationSessionChanged(run.error) || !stationSessionMatches(client, role, session)) {
          active.current = null;
          setJob(null);
          setLost(true);
          return;
        }
        render((value) => value + 1);
        if (run.phase === 'done')
          void client.invalidateQueries({ queryKey: charityKeys.root(role) });
      },
    });
    active.current = run;
    setJob(run);
    setConfirming(false);
    void run.resume();
  };
  if (lost)
    return (
      <p className="field-error" role="alert">
        {t('common.operations.charity.accessLost')}
      </p>
    );
  const single = 'key_id' in target;
  const working = job?.phase === 'running';
  const label = single ? 'one' : target.donation_id === null ? 'all' : 'donation';
  return (
    <section className="donation-discovery" aria-label={t('common.donationDiscovery.title')}>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={disabled || (job !== null && job.phase !== 'done')}
        aria-expanded={single ? undefined : confirming}
        aria-controls={single ? undefined : id}
        onClick={() => (single ? begin() : setConfirming((value) => !value))}
      >
        {t(`common.donationDiscovery.${label}`)}
      </button>
      {confirming ? (
        <div id={id} className="inline-notice">
          <p>{t(`common.donationDiscovery.${label}Help`)}</p>
          <p>{t('common.donationDiscovery.eligibility')}</p>
          <div className="ops-actions">
            <button type="button" className="btn btn-primary" disabled={disabled} onClick={begin}>
              {t('common.donationDiscovery.confirm')}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => setConfirming(false)}
            >
              {t('common.cancel')}
            </button>
          </div>
        </div>
      ) : null}
      {job ? (
        <div className="donation-discovery-progress">
          <p role="status" aria-live="polite">
            {t(`common.donationDiscovery.${job.phase}`, job.counts)}
          </p>
          <p>{t('common.donationDiscovery.counts', job.counts)}</p>
          {working && job.current ? (
            <p>
              {t('common.donationDiscovery.current', {
                donation: job.current.donation_id,
                key: job.current.key_id,
              })}
            </p>
          ) : null}
          {working ? (
            <button type="button" className="btn btn-secondary" onClick={() => job.stop()}>
              {t('common.donationDiscovery.stop')}
            </button>
          ) : null}
          {job.phase === 'paused' ? (
            <>
              <p>{t('common.donationDiscovery.resumeHelp')}</p>
              <button
                type="button"
                className="btn btn-secondary"
                disabled={disabled}
                onClick={() => void job.resume()}
              >
                {t('common.donationDiscovery.resume')}
              </button>
            </>
          ) : null}
          {job.error ? <ErrorState error={job.error} /> : null}
          {job.results.length > 0 ? (
            <details>
              <summary>{t('common.donationDiscovery.results')}</summary>
              <ol>
                {job.results.map((item) => (
                  <li key={item.key_id}>
                    {t('common.donationDiscovery.current', {
                      donation: item.donation_id,
                      key: item.key_id,
                    })}
                    {' · '}
                    {t(`common.donationDiscovery.status.${item.status}`, {
                      count: item.count ?? '0',
                    })}
                    {item.status === 'failed' && item.reason && item.reason !== 'none'
                      ? ` · ${t(`common.donationDiscovery.reasons.${item.reason}`)}`
                      : null}
                  </li>
                ))}
              </ol>
            </details>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
