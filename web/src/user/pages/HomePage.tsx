import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Card, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { isNotFoundError, isUnauthorized } from '@shared/query/http';
import { useUserMe, useUserSession, useUserUsage } from '../data';

function number(value: number): string {
  return value.toLocaleString();
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
        </div>
      </section>
      <Card>
        <h2>{t('user.home.profileTitle')}</h2>
        <p className="muted">{t('common.userSignInBody')}</p>
      </Card>
    </div>
  );
}

function HomeContent() {
  const { t } = useTranslation();
  const session = useUserSession();
  const signedIn = Boolean(session.data?.user);
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
              <strong className="metric-value">{number(usage.data.total_requests)}</strong>
            </div>
            <div className="metric-card">
              <p>{t('user.home.promptTokens')}</p>
              <strong className="metric-value">{number(usage.data.total_prompt_tokens)}</strong>
            </div>
            <div className="metric-card">
              <p>{t('user.home.completionTokens')}</p>
              <strong className="metric-value">{number(usage.data.total_completion_tokens)}</strong>
            </div>
            <div className="metric-card">
              <p>{t('user.home.unknownUsage')}</p>
              <strong className="metric-value">{number(usage.data.total_unknown_usage_requests)}</strong>
            </div>
          </div>
        )}
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
                <dd>{me.data.created_at}</dd>
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
