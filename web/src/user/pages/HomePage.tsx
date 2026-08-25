import { useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import { formatDateTime } from '@shared/utils/datetime';
import { useTranslation } from 'react-i18next';
import { Card, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { isApiError, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { isStationSessionChanged } from '@shared/charityManagement';
import { CompactNumber } from '@shared/components/CompactNumber';
import {
  formatCompact,
  formatCreditsFromMilli,
  formatCount,
  type FormattedNumber,
} from '@shared/utils/formatNumber';
import {
  useCheckin,
  useCheckinStatus,
  useUserMe,
  useUserSession,
  useUserUsage,
  type CheckinResult,
  type UserSummary,
} from '../data';

// Renders a balance with its exact milli-credit figure available on hover and
// keyboard focus (and to assistive tech), so a rounded display value never
// hides the precise amount. The display form is never a Number() conversion.
function ExactMilliValue({ formatted, label }: { formatted: FormattedNumber; label: string }) {
  const { t } = useTranslation();
  const exact = t('user.home.exactMilli', { value: formatted.exact });
  return (
    <span className="compact-number" tabIndex={0} title={exact} aria-label={`${label} · ${exact}`}>
      {formatted.display}
    </span>
  );
}

function SignedOutHome() {
  const { t } = useTranslation();
  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('user.home.signedOutTitle')}
        description={t('user.home.description')}
      />
      <section className="auth-panel" aria-labelledby="signed-out-title">
        <span className="auth-mark" aria-hidden="true">
          ◌
        </span>
        <div>
          <h2 id="signed-out-title">{t('user.home.signedOutTitle')}</h2>
          <p>{t('user.home.signedOutBody')}</p>
          <a className="btn btn-primary" href="/api/auth/discord/start">
            {t('common.signIn')}
          </a>
          <p className="legal-reminder">
            {t('user.home.legalReminderPre')}
            <Link to="/terms">{t('user.home.legalReminderTerms')}</Link>
            {t('user.home.legalReminderAnd')}
            <Link to="/privacy">{t('user.home.legalReminderPrivacy')}</Link>
            {t('user.home.legalReminderPost')}
          </p>
        </div>
      </section>
      <Card>
        <h2>{t('user.home.profileTitle')}</h2>
        <p className="muted">{t('common.userSignInBody')}</p>
      </Card>
    </div>
  );
}

// Server-projected economy state: both balances and the resolved level are
// rendered exactly as the session reports them. The page computes nothing —
// no eligibility, no thresholds, no post-delta balances.
function EconomyCard({ user }: { user: UserSummary }) {
  const { t } = useTranslation();
  const credits = formatCreditsFromMilli(user.credits);
  const donation = formatCreditsFromMilli(user.donation_credit);
  const isManual = user.manual_level !== undefined;
  return (
    <Card>
      <div className="card-title-row">
        <h2>{t('user.home.economyTitle')}</h2>
      </div>
      <div className="metric-grid">
        <div className="metric-card">
          <p>{t('user.home.creditsBalance')}</p>
          <strong className="metric-value">
            <ExactMilliValue formatted={credits} label={t('user.home.creditsBalance')} />
          </strong>
        </div>
        <div className="metric-card">
          <p>{t('user.home.donationCredit')}</p>
          <strong className="metric-value">
            <ExactMilliValue formatted={donation} label={t('user.home.donationCredit')} />
          </strong>
        </div>
        <div className="metric-card">
          <p>{t('user.home.level')}</p>
          <strong className="metric-value">
            {t('user.home.levelValue', { level: user.effective_level })}
            {isManual ? <span className="muted">{t('user.home.levelManualSuffix')}</span> : null}
          </strong>
        </div>
      </div>
    </Card>
  );
}

interface CheckinNotice {
  text: string;
  kind: 'status' | 'error';
}

// The daily check-in card. Availability, today's status, the award range and
// the threshold all come from GET /api/checkin; the award is drawn and applied
// server-side, and every refusal message is shown verbatim.
function CheckinCard({ signedIn }: { signedIn: boolean }) {
  const { t } = useTranslation();
  const status = useCheckinStatus(signedIn);
  const checkin = useCheckin();
  const [result, setResult] = useState<CheckinResult | null>(null);
  const [notice, setNotice] = useState<CheckinNotice | null>(null);

  const submit = () => {
    setNotice(null);
    checkin.mutate(undefined, {
      onSuccess: (data) => setResult(data),
      onError: (error) => {
        if (isStationSessionChanged(error)) return;
        if (isApiError(error) && error.code === 'already_checked_in') {
          // The day was already consumed: the status query refetch settles the
          // card into the checked-in state.
          setResult(null);
          setNotice({ text: error.message, kind: 'status' });
          return;
        }
        // feature_disabled / checkin_cap_reached / transport failures render
        // as text; the server message is already bounded and cleaned.
        setNotice({
          text: isApiError(error) ? error.message : t('common.errorBody'),
          kind: 'error',
        });
      },
    });
  };

  let body: ReactNode;
  if (status.isPending) {
    body = <LoadingState />;
  } else if (status.error) {
    body = <ErrorState error={status.error} onRetry={() => void status.refetch()} />;
  } else if (!status.data.enabled) {
    // Neutral, speculation-free state: the server never reveals why.
    body = <p className="inline-notice">{t('user.checkin.unavailable')}</p>;
  } else {
    const min = formatCreditsFromMilli(status.data.award_min_milli);
    const max = formatCreditsFromMilli(status.data.award_max_milli);
    const cap = formatCreditsFromMilli(status.data.credits_cap_milli);
    const noThreshold = status.data.credits_cap_milli === '0';
    body = (
      <>
        <dl className="detail-grid">
          <div className="detail-row">
            <dt>{t('user.checkin.today')}</dt>
            <dd>
              {status.data.checked_in_today ? (
                <StatusBadge active label={t('user.checkin.checkedIn')} />
              ) : (
                <StatusBadge active={false} label={t('user.checkin.notCheckedIn')} />
              )}
            </dd>
          </div>
          <div className="detail-row">
            <dt>{t('user.checkin.awardRange')}</dt>
            <dd>
              {min.display} – {max.display}
            </dd>
          </div>
          <div className="detail-row">
            <dt>{t('user.checkin.threshold')}</dt>
            <dd>{noThreshold ? t('user.checkin.thresholdNone') : cap.display}</dd>
          </div>
        </dl>
        <p className="muted item-note">{t('user.checkin.thresholdHint')}</p>
        {!status.data.checked_in_today ? (
          <div className="form-actions">
            <button
              type="button"
              className="btn btn-primary"
              onClick={submit}
              disabled={checkin.isPending}
            >
              {checkin.isPending ? t('common.working') : t('user.checkin.submit')}
            </button>
          </div>
        ) : null}
        {result ? (
          <p className="inline-success" role="status">
            {t('user.checkin.done', {
              award: formatCreditsFromMilli(result.award_milli).display,
              credits: formatCreditsFromMilli(result.credits).display,
            })}
          </p>
        ) : null}
        {notice ? (
          <p
            className={notice.kind === 'error' ? 'field-error' : 'inline-notice'}
            role={notice.kind === 'error' ? 'alert' : 'status'}
          >
            {notice.text}
          </p>
        ) : null}
      </>
    );
  }

  return (
    <Card>
      <div className="card-title-row">
        <h2>{t('user.checkin.title')}</h2>
      </div>
      {body}
    </Card>
  );
}

function HomeContent() {
  const { t } = useTranslation();
  const session = useUserSession();
  const signedIn = !session.error && Boolean(session.data?.user);
  const me = useUserMe(signedIn);
  const usage = useUserUsage(signedIn);
  const user = session.data?.user;

  if (session.isPending) return <LoadingState />;
  if (session.error && !isUnauthorized(session.error) && !isNotFoundError(session.error)) {
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  }
  if (!user) return <SignedOutHome />;

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('user.home.signedInTitle', { name: user.username })}
        description={t('user.home.description')}
      />

      <section aria-labelledby="user-usage-title">
        <h2 id="user-usage-title" className="section-title">
          {t('user.home.usageTitle')}
        </h2>
        {usage.isPending ? (
          <LoadingState />
        ) : usage.error ? (
          <ErrorState error={usage.error} onRetry={() => void usage.refetch()} />
        ) : (
          <div className="metric-grid">
            <div className="metric-card">
              <p>{t('user.home.requests')}</p>
              <strong className="metric-value">{formatCount(usage.data.total_requests).display}</strong>
            </div>
            <div className="metric-card">
              <p>{t('common.tokens.input')}</p>
              <strong className="metric-value">
                <CompactNumber value={formatCompact(usage.data.total_prompt_tokens)} />
              </strong>
            </div>
            <div className="metric-card">
              <p>{t('common.tokens.output')}</p>
              <strong className="metric-value">
                <CompactNumber value={formatCompact(usage.data.total_completion_tokens)} />
              </strong>
            </div>
            <div className="metric-card">
              <p>{t('user.home.unknownUsage')}</p>
              <strong className="metric-value">
                {formatCount(usage.data.total_unknown_usage_requests).display}
              </strong>
            </div>
          </div>
        )}
      </section>

      <section aria-labelledby="user-economy-title">
        <h2 id="user-economy-title" className="section-title">
          {t('user.home.economySectionTitle')}
        </h2>
        <div className="split-grid">
          <EconomyCard user={user} />
          <CheckinCard signedIn />
        </div>
      </section>

      <div className="split-grid">
        <Card>
          <div className="card-title-row">
            <h2>{t('user.home.quickLinks')}</h2>
          </div>
          <div className="form-actions">
            <Link className="btn btn-primary" to="/endpoints">
              {t('user.home.endpointsLink')}
            </Link>
            <Link className="btn btn-secondary" to="/models">
              {t('user.home.modelsLink')}
            </Link>
            <Link className="btn btn-secondary" to="/keys">
              {t('user.home.callerKeyLink')}
            </Link>
          </div>
        </Card>

        <Card>
          <div className="card-title-row">
            <h2>{t('user.home.profileTitle')}</h2>
          </div>
          {me.isPending ? (
            <LoadingState />
          ) : me.error ? (
            <ErrorState error={me.error} onRetry={() => void me.refetch()} />
          ) : (
            <dl className="detail-grid">
              <div className="detail-row">
                <dt>{t('user.home.username')}</dt>
                <dd>{me.data.username}</dd>
              </div>
              <div className="detail-row">
                <dt>{t('user.home.accountCreated')}</dt>
                <dd>{formatDateTime(me.data.created_at)}</dd>
              </div>
              <div className="detail-row">
                <dt>{t('user.home.accountStatus')}</dt>
                <dd>
                  <StatusBadge
                    active={!me.data.is_banned}
                    label={me.data.is_banned ? t('user.home.banned') : t('user.home.active')}
                  />
                </dd>
              </div>
            </dl>
          )}
        </Card>
      </div>
    </div>
  );
}

export function HomePage() {
  return <HomeContent />;
}
