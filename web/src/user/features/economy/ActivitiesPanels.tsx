import { useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, ErrorState, StatusBadge } from '@shared/components/States';
import { formatDateTime } from '@shared/utils/datetime';
import { isConflictError, isResponseUnknown } from './api';
import { CreditAmount, ExactCount } from './ExactValue';
import { calculateWelfareAward } from './normalize';
import { useClaimWelfare, useContributeThursday, type ActivityConnectionState } from './queries';
import type { ActivitiesSnapshot, ThursdayView, WelfareView } from './types';

function mutationNotice(error: unknown, success: ReactNode, t: (key: string) => string) {
  if (error) {
    const key = isResponseUnknown(error)
      ? 'user.activities.mutationResponseUnknown'
      : isConflictError(error)
        ? 'user.activities.mutationConflictRefreshed'
        : 'user.activities.mutationRejected';
    return (
      <p className="inline-notice economy-notice economy-notice--warning" role="alert">
        {t(key)}
      </p>
    );
  }
  return success ? (
    <div className="inline-notice economy-notice" role="status">
      {success}
    </div>
  ) : null;
}

function useServerCountdown(serverNow: number, target: number | null): number | null {
  const [receivedAt] = useState(Date.now);
  const [localNow, setLocalNow] = useState(receivedAt);
  useEffect(() => {
    const timer = window.setInterval(() => setLocalNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, []);
  if (target === null) return null;
  return Math.max(
    0,
    Math.ceil((target * 1000 - (localNow + serverNow * 1000 - receivedAt)) / 1000),
  );
}

function Countdown({
  serverNow,
  target,
  labelKey,
}: {
  serverNow: number;
  target: number | null;
  labelKey: string;
}) {
  const { t } = useTranslation();
  const remaining = useServerCountdown(serverNow, target);
  if (target === null || remaining === null) return null;
  const days = Math.floor(remaining / 86_400);
  const hours = Math.floor((remaining % 86_400) / 3_600);
  const minutes = Math.floor((remaining % 3_600) / 60);
  const seconds = remaining % 60;
  const clock =
    days > 0
      ? t('user.activities.countdownDays', { days, hours, minutes })
      : [hours, minutes, seconds].map((value) => String(value).padStart(2, '0')).join(':');
  return (
    <div className="economy-countdown" aria-live="off">
      <span>{t(labelKey)}</span>
      <strong className="mono">{clock}</strong>
      <small>{formatDateTime(target)}</small>
    </div>
  );
}

function stateDanger(state: string): boolean {
  return state === 'configuration_error' || state === 'unavailable';
}

export function ActivitiesMasterNotice({ snapshot }: { snapshot: ActivitiesSnapshot }) {
  const { t } = useTranslation();
  return (
    <Card className="economy-master-card">
      <div className="card-title-row">
        <div>
          <p className="eyebrow">{t('user.activities.masterEyebrow')}</p>
          <h2>{t('user.activities.masterTitle')}</h2>
        </div>
        <StatusBadge
          active={snapshot.master.available}
          danger={snapshot.master.reason === 'configuration_error'}
          label={t(`user.activities.masterState.${snapshot.master.reason}`)}
        />
      </div>
      <p>{t(`user.activities.masterBody.${snapshot.master.reason}`)}</p>
    </Card>
  );
}

export function ActivityConnectionNotice({
  connection,
  recoveryError,
  reconciled,
}: {
  connection: ActivityConnectionState;
  recoveryError: unknown;
  reconciled: boolean;
}) {
  const { t } = useTranslation();
  const key = recoveryError
    ? 'recoveryFailed'
    : connection === 'disconnected'
      ? 'disconnected'
      : connection === 'connecting'
        ? 'connecting'
        : reconciled
          ? 'reconciled'
          : 'connected';
  return (
    <p
      className={`inline-notice economy-stream-state economy-stream-state--${connection}`}
      role={recoveryError ? 'alert' : 'status'}
    >
      {t(`user.activities.stream.${key}`)}
    </p>
  );
}

export function WelfareCard({
  welfare,
  masterAvailable,
}: {
  welfare: WelfareView;
  masterAvailable: boolean;
}) {
  const { t } = useTranslation();
  const mutation = useClaimWelfare();
  const [blockedAuthority, setBlockedAuthority] = useState<{
    baselineGeneration: number;
  } | null>(null);
  const authorityAdvanced =
    blockedAuthority !== null && mutation.reconcileGeneration > blockedAuthority.baselineGeneration;
  const waitingForAuthority = blockedAuthority !== null && !authorityAdvanced;
  const award = welfare.state === 'available' ? calculateWelfareAward(welfare) : '0';
  const canClaim =
    masterAvailable &&
    welfare.enabled &&
    welfare.state === 'available' &&
    award !== '0' &&
    !waitingForAuthority;
  const claim = async () => {
    mutation.reset();
    setBlockedAuthority(null);
    const baselineGeneration = mutation.reconcileGeneration;
    try {
      await mutation.mutateAsync();
    } catch (error) {
      if (isConflictError(error) || isResponseUnknown(error)) {
        setBlockedAuthority({ baselineGeneration });
      }
      // The hook performs an authoritative GET after every terminal response.
    }
  };
  const success = authorityAdvanced ? (
    t('user.activities.mutationReconciled')
  ) : mutation.data ? (
    mutation.data.awarded === '0' ? (
      t('user.activities.welfare.zeroAward')
    ) : (
      <span>
        {t('user.activities.welfare.claimedAmount')} <CreditAmount value={mutation.data.awarded} />
      </span>
    )
  ) : null;
  return (
    <Card className="economy-activity-card economy-welfare-card">
      <div className="card-title-row">
        <div>
          <p className="eyebrow">{t('user.activities.welfare.eyebrow')}</p>
          <h2>{t('user.activities.welfare.title')}</h2>
        </div>
        <StatusBadge
          active={welfare.state === 'available'}
          danger={stateDanger(welfare.state)}
          label={t(`user.activities.welfare.state.${welfare.state}`)}
        />
      </div>
      <p>{t(`user.activities.welfare.body.${welfare.state}`)}</p>
      <div className="economy-stat-grid">
        <section>
          <span>{t('user.activities.poolBalance')}</span>
          <strong>
            <CreditAmount value={welfare.poolBalance} />
          </strong>
        </section>
        <section>
          <span>{t('user.activities.welfare.availableAmount')}</span>
          <strong>
            <CreditAmount value={award} />
          </strong>
        </section>
        <section>
          <span>{t('user.activities.welfare.threshold')}</span>
          <strong>
            <CreditAmount value={welfare.threshold} />
          </strong>
        </section>
      </div>
      <dl className="detail-grid economy-activity-details">
        <div className="detail-row">
          <dt>{t('user.activities.siteDay')}</dt>
          <dd>{welfare.siteDay || '—'}</dd>
        </div>
        <div className="detail-row">
          <dt>{t('user.activities.welfare.claimedToday')}</dt>
          <dd>{t(welfare.claimedToday ? 'common.yes' : 'common.no')}</dd>
        </div>
      </dl>
      {welfare.state === 'empty' ? (
        <p className="inline-notice economy-notice" role="note">
          {t('user.activities.welfare.zeroAward')}
        </p>
      ) : null}
      {mutationNotice(authorityAdvanced ? null : mutation.error, success, t)}
      {waitingForAuthority && mutation.reconcileError ? (
        <ErrorState
          error={mutation.reconcileError}
          onRetry={() => void mutation.retryReconcile()}
        />
      ) : null}
      <div className="form-actions">
        <button
          type="button"
          className="btn btn-primary"
          disabled={!canClaim || mutation.isPending || mutation.isReconciling}
          onClick={() => void claim()}
        >
          {mutation.isPending || mutation.isReconciling
            ? t('common.working')
            : t('user.activities.welfare.claim')}
        </button>
      </div>
    </Card>
  );
}

function ThursdayResult({ thursday }: { thursday: ThursdayView }) {
  const { t } = useTranslation();
  const result = thursday.lastResult;
  if (!result || thursday.current !== null) return null;
  return (
    <section className="economy-thursday-result" aria-labelledby="thursday-result-title">
      <p className="eyebrow">{t('user.activities.thursday.lastResultEyebrow')}</p>
      <h3 id="thursday-result-title">{t('user.activities.thursday.lastResultTitle')}</h3>
      <div className="economy-stat-grid">
        <section>
          <span>{t('user.activities.thursday.resultCount')}</span>
          <strong>
            <ExactCount value={result.myCount} />
          </strong>
        </section>
        <section>
          <span>{t('user.activities.thursday.resultContributed')}</span>
          <strong>
            <CreditAmount value={result.myContributed} />
          </strong>
        </section>
        <section>
          <span>{t('user.activities.thursday.payout')}</span>
          <strong>
            <CreditAmount value={result.payout} />
          </strong>
        </section>
      </div>
      {result.unpaidReason ? (
        <p className="inline-notice economy-notice--warning">
          {t(`user.activities.thursday.unpaidReason.${result.unpaidReason}`)}
        </p>
      ) : null}
    </section>
  );
}

function thursdayCountdown(thursday: ThursdayView): { target: number | null; labelKey: string } {
  if (thursday.current) {
    return {
      target:
        thursday.state === 'not_open' && thursday.current.opensAt > thursday.serverNow
          ? thursday.current.opensAt
          : thursday.current.closesAt,
      labelKey:
        thursday.state === 'open'
          ? 'user.activities.thursday.closesIn'
          : 'user.activities.thursday.currentDeadline',
    };
  }
  return {
    target: thursday.next?.opensAt ?? null,
    labelKey: 'user.activities.thursday.opensIn',
  };
}

export function ThursdayCard({
  thursday,
  masterAvailable,
}: {
  thursday: ThursdayView;
  masterAvailable: boolean;
}) {
  const { t } = useTranslation();
  const mutation = useContributeThursday();
  const current = thursday.current;
  const [blockedAuthority, setBlockedAuthority] = useState<{
    baselineGeneration: number;
  } | null>(null);
  const authorityAdvanced =
    blockedAuthority !== null && mutation.reconcileGeneration > blockedAuthority.baselineGeneration;
  const waitingForAuthority = blockedAuthority !== null && !authorityAdvanced;
  const limitReached = current ? BigInt(current.myCount) >= BigInt(current.perUserLimit) : true;
  const canContribute = Boolean(
    masterAvailable &&
    thursday.enabled &&
    thursday.state === 'open' &&
    current &&
    !limitReached &&
    !waitingForAuthority,
  );
  const countdown = thursdayCountdown(thursday);
  const contribute = async () => {
    if (!current) return;
    mutation.reset();
    setBlockedAuthority(null);
    const baselineGeneration = mutation.reconcileGeneration;
    try {
      await mutation.mutateAsync({
        periodId: current.periodId,
        expectedRevision: current.revision,
      });
    } catch (error) {
      if (isConflictError(error) || isResponseUnknown(error)) {
        setBlockedAuthority({ baselineGeneration });
      }
      // A fresh GET reconciles conflicts and response-unknown outcomes.
    }
  };
  const success = authorityAdvanced ? (
    t('user.activities.mutationReconciled')
  ) : mutation.data ? (
    <span>
      {t('user.activities.thursday.contributionAccepted')}{' '}
      <ExactCount value={mutation.data.count} />
    </span>
  ) : null;
  return (
    <Card className="economy-activity-card economy-thursday-card">
      <div className="card-title-row">
        <div>
          <p className="eyebrow">{t('user.activities.thursday.eyebrow')}</p>
          <h2>{t('user.activities.thursday.title')}</h2>
        </div>
        <StatusBadge
          active={thursday.state === 'open'}
          danger={stateDanger(thursday.state)}
          label={t(`user.activities.thursday.state.${thursday.state}`)}
        />
      </div>
      <p>{t(`user.activities.thursday.body.${thursday.state}`)}</p>
      {current?.literature ? <p className="economy-literature">{current.literature}</p> : null}
      <div className="economy-stat-grid">
        {current ? (
          <section>
            <span>{t('user.activities.thursday.currentPoolBalance')}</span>
            <strong>
              <CreditAmount value={current.poolBalance} />
            </strong>
          </section>
        ) : thursday.next ? (
          <section>
            <span>{t('user.activities.thursday.nextPoolBalance')}</span>
            <strong>
              <CreditAmount value={thursday.next.poolBalance} />
            </strong>
          </section>
        ) : (
          <section>
            <span>{t('user.activities.poolBalance')}</span>
            <strong>{t('user.activities.poolUnavailable')}</strong>
          </section>
        )}
        {current && thursday.next ? (
          <section>
            <span>{t('user.activities.thursday.nextPoolBalance')}</span>
            <strong>
              <CreditAmount value={thursday.next.poolBalance} />
            </strong>
          </section>
        ) : null}
        {current ? (
          <>
            <section>
              <span>{t('user.activities.thursday.fixedEntry')}</span>
              <strong>
                <CreditAmount value={current.entry} />
              </strong>
            </section>
            <section>
              <span>{t('user.activities.thursday.myCount')}</span>
              <strong>
                <ExactCount value={current.myCount} /> /{' '}
                <ExactCount value={String(current.perUserLimit)} />
              </strong>
            </section>
            <section>
              <span>{t('user.activities.thursday.myContributed')}</span>
              <strong>
                <CreditAmount value={current.myContributed} />
              </strong>
            </section>
          </>
        ) : null}
      </div>
      <Countdown
        key={`${thursday.serverNow}:${countdown.target ?? 'none'}`}
        serverNow={thursday.serverNow}
        target={countdown.target}
        labelKey={countdown.labelKey}
      />
      {thursday.state === 'settling' ? (
        <p className="inline-notice economy-notice" role="status">
          {t('user.activities.thursday.noPrediction')}
        </p>
      ) : null}
      <ThursdayResult thursday={thursday} />
      {mutationNotice(authorityAdvanced ? null : mutation.error, success, t)}
      {waitingForAuthority && mutation.reconcileError ? (
        <ErrorState
          error={mutation.reconcileError}
          onRetry={() => void mutation.retryReconcile()}
        />
      ) : null}
      <div className="form-actions">
        <button
          type="button"
          className="btn btn-primary"
          disabled={!canContribute || mutation.isPending || mutation.isReconciling}
          onClick={() => void contribute()}
        >
          {mutation.isPending || mutation.isReconciling
            ? t('common.working')
            : t('user.activities.thursday.contributeOnce')}
        </button>
      </div>
    </Card>
  );
}
