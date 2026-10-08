import { Button } from '@shared/components/ui/Button';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { isApiError, isForbidden, isUnauthorized } from '@shared/query/http';
import { usePublicConfig } from '@shared/query/publicConfig';
import { Icon, type IconName } from './Icon';
import '../operations/pagePagination.css';
import emptyStateURL from '@shared/assets/state-empty.svg';
import maintenanceStateURL from '@shared/assets/state-maintenance.svg';

interface PageHeaderProps {
  eyebrow?: string;
  title: string;
  description?: string;
  actions?: ReactNode;
  icon?: IconName;
  back?: ReactNode;
  className?: string;
}

export function PageHeader({ eyebrow, title, description, actions, icon, back, className = '' }: PageHeaderProps) {
  return (
    <div className={`page-header nb-page-header${className ? ` ${className}` : ''}`} data-component="page-header">
      <div className="nb-page-header__lead">
        {icon ? <span className="nb-page-header__icon"><Icon name={icon} /></span> : null}
        <div className="nb-page-header__copy">
          {back ? <div className="nb-page-header__back">{back}</div> : null}
          {eyebrow ? <p className="eyebrow nb-page-header__eyebrow">{eyebrow}</p> : null}
        <h1>{title}</h1>
          {description ? <p className="page-description nb-page-header__description">{description}</p> : null}
        </div>
      </div>
      {actions ? <div className="page-header-actions nb-page-header__actions">{actions}</div> : null}
    </div>
  );
}

export function LoadingState({ label }: { label?: string }) {
  const { t } = useTranslation();
  return (
    <div className="state-panel nb-state" role="status" aria-live="polite">
      <span className="nb-state__icon"><Icon name="spark" /></span>
      <span className="nb-state__body"><span className="loading-dot" aria-hidden="true" /><span>{label ?? t('common.loading')}</span></span>
    </div>
  );
}

interface EmptyStateProps {
  title: string;
  body: string;
  action?: ReactNode;
}

export function EmptyState({ title, body, action }: EmptyStateProps) {
  return (
    <div className="state-panel empty-state nb-state">
      <span className="state-icon nb-state__icon" aria-hidden="true"><img className="nb-state__illustration" src={emptyStateURL} alt="" /></span>
      <div>
        <h2>{title}</h2>
        <p>{body}</p>
        {action ? <div className="state-actions">{action}</div> : null}
      </div>
    </div>
  );
}

function errorMessage(error: unknown, translate: (key: string) => string): string {
  if (isUnauthorized(error)) return translate('common.authRequired');
  if (isApiError(error) && error.code === 'elevated_required') return translate('common.elevationRequired');
  if (isForbidden(error)) return translate('common.forbidden');
  if (isApiError(error)) {
    switch (error.code) {
      case 'network_error':
        return translate('common.networkError');
      case 'rate_limited':
        return translate('common.rateLimited');
      case 'invalid_response':
        return translate('common.invalidResponse');
      case 'not_found':
        return translate('common.resourceNotFound');
      default:
        // The server message is already bounded and control-character cleaned
        // by the HTTP boundary. It remains useful for a stable, human-safe API
        // validation message without exposing diagnostic text by default.
        return error.message || translate('common.errorBody');
    }
  }
  return translate('common.errorBody');
}

interface ErrorDetail {
  label: string;
  text: string;
}

export function ErrorState({
  error,
  onRetry,
  detail,
}: {
  error: unknown;
  onRetry?: () => void;
  /** Explicitly supplied, already-cleaned detail for an authorized surface. */
  detail?: ErrorDetail;
}) {
  const { t } = useTranslation();
  const unauthorized = isUnauthorized(error);
  return (
    <div className="state-panel error-state nb-state nb-state--error" role="alert">
      <span className="state-icon nb-state__icon" aria-hidden="true"><Icon name="error" /></span>
      <div>
        <h2>{unauthorized ? t('common.authRequiredTitle') : t('common.error')}</h2>
        <p>{errorMessage(error, t)}</p>
        {detail ? (
          <details className="diagnostic">
            <summary>{detail.label}</summary>
            <p>{detail.text}</p>
          </details>
        ) : null}
        {onRetry ? (
          <Button type="button"  onClick={onRetry}>
            {t('common.retry')}
          </Button>
        ) : null}
      </div>
    </div>
  );
}

export function AuthRequired({ station }: { station: 'user' | 'admin' }) {
  const { t } = useTranslation();
  const config = usePublicConfig(station === 'user');
  const href = station === 'user' ? '/api/auth/discord/start' : undefined;
  const registrationClosed = station === 'user' && config.data?.registrationOpen === false;
  return (
    <div className="auth-panel nb-state" role="status">
      <span className="auth-mark nb-state__icon" aria-hidden="true"><Icon name="account" /></span>
      <div>
        <h2>{t('common.authRequiredTitle')}</h2>
        <p>{station === 'user' ? t('common.userSignInBody') : t('common.adminSignInBody')}</p>
        {registrationClosed ? <p className="inline-notice">{t('common.registrationClosed')}</p> : null}
        {href ? (
          <a className="nb-btn nb-btn--primary" href={href}>
            {t('common.signIn')}
          </a>
        ) : null}
      </div>
    </div>
  );
}

export function StatusBadge({
  active,
  label,
  danger = false,
}: {
  active: boolean;
  label?: string;
  danger?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <span className={`status-badge ${danger ? 'is-danger' : active ? 'is-active' : 'is-inactive'}`}>
      <span className="status-dot" aria-hidden="true" />
      {label ?? (active ? t('common.enabled') : t('common.disabled'))}
    </span>
  );
}

export function Card({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <section className={`card ${className}`}>{children}</section>;
}

export function ReadOnlyValue({ value }: { value: string }) {
  return <span className="read-only-value">{value || '—'}</span>;
}

export function ApiNotice({ children }: { children: ReactNode }) {
  return (
    <p className="inline-notice" role="status">
      {children}
    </p>
  );
}

// NoticePage is a full-page, single-message page used for site-wide states
// (maintenance, registration closed) that replace normal navigation.
export function NoticePage({
  titleKey,
  bodyKey,
  icon = 'maintenance',
}: {
  titleKey: string;
  bodyKey: string;
  icon?: IconName;
}) {
  const { t } = useTranslation();
  return (
    <div className="state-panel notice-page nb-state nb-state--warning" role="status">
      <span className="state-icon nb-state__icon" aria-hidden="true">
        {icon === 'maintenance' ? <img className="nb-state__illustration" src={maintenanceStateURL} alt="" /> : <Icon name={icon} />}
      </span>
      <div>
        <h1>{t(titleKey)}</h1>
        <p>{t(bodyKey)}</p>
      </div>
    </div>
  );
}

export function MutationState({ label }: { label: string }) {
  return <div className="nb-state" role="status" aria-live="polite"><span className="nb-state__icon"><Icon name="spark" /></span><p>{label}</p></div>;
}

export function PageFooter({
  copyright,
  links,
  className = '',
}: {
  copyright?: ReactNode;
  links?: ReactNode;
  className?: string;
}) {
  return (
    <footer className={`nb-page-footer${className ? ` ${className}` : ''}`}>
      <span>{copyright}</span>
      {links ? <span className="nb-page-footer__links">{links}</span> : null}
    </footer>
  );
}

export function SectionNav({ children, label }: { children: ReactNode; label?: string }) {
  return <nav className="nb-section-nav" aria-label={label}>{children}</nav>;
}

export function MetricCard({ label, value, hint }: { label: string; value: ReactNode; hint?: ReactNode }) {
  return (
    <section className="nb-metric-card">
      <span className="nb-metric-card__label">{label}</span>
      <strong className="nb-metric-card__value">{value}</strong>
      {hint ? <p className="nb-metric-card__hint">{hint}</p> : null}
    </section>
  );
}

export function Skeleton({ className = '', style }: { className?: string; style?: React.CSSProperties }) {
  return <span className={`nb-skeleton${className ? ` ${className}` : ''}`} aria-hidden="true" style={style} />;
}
