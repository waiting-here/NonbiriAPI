import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { isApiError, isForbidden, isUnauthorized } from '@shared/query/http';
import { usePublicConfig } from '@shared/query/publicConfig';

interface PageHeaderProps {
  eyebrow?: string;
  title: string;
  description?: string;
  actions?: ReactNode;
}

export function PageHeader({ eyebrow, title, description, actions }: PageHeaderProps) {
  return (
    <div className="page-header">
      <div>
        {eyebrow ? <p className="eyebrow">{eyebrow}</p> : null}
        <h1>{title}</h1>
        {description ? <p className="page-description">{description}</p> : null}
      </div>
      {actions ? <div className="page-header-actions">{actions}</div> : null}
    </div>
  );
}

export function LoadingState({ label }: { label?: string }) {
  const { t } = useTranslation();
  return (
    <div className="state-panel" role="status" aria-live="polite">
      <span className="loading-dot" aria-hidden="true" />
      <span>{label ?? t('common.loading')}</span>
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
    <div className="state-panel empty-state">
      <span className="state-icon" aria-hidden="true">
        —
      </span>
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

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useTranslation();
  const unauthorized = isUnauthorized(error);
  const apiError = isApiError(error) ? error : null;
  return (
    <div className="state-panel error-state" role="alert">
      <span className="state-icon" aria-hidden="true">
        !
      </span>
      <div>
        <h2>{unauthorized ? t('common.authRequiredTitle') : t('common.error')}</h2>
        <p>{errorMessage(error, t)}</p>
        {apiError?.diag ? (
          <details className="diagnostic">
            <summary>{t('common.showDetails')}</summary>
            <p>{apiError.diag}</p>
          </details>
        ) : null}
        {onRetry ? (
          <button type="button" className="btn btn-secondary" onClick={onRetry}>
            {t('common.retry')}
          </button>
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
    <div className="auth-panel" role="status">
      <span className="auth-mark" aria-hidden="true">
        ◌
      </span>
      <div>
        <h2>{t('common.authRequiredTitle')}</h2>
        <p>{station === 'user' ? t('common.userSignInBody') : t('common.adminSignInBody')}</p>
        {registrationClosed ? <p className="inline-notice">{t('common.registrationClosed')}</p> : null}
        {href ? (
          <a className="btn btn-primary" href={href}>
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

export function Pagination({
  page,
  hasNext,
  onChange,
}: {
  page: number;
  hasNext: boolean;
  onChange: (nextPage: number) => void;
}) {
  const { t } = useTranslation();
  return (
    <nav className="pagination" aria-label={t('common.pagination')}>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={page <= 1}
        onClick={() => onChange(Math.max(1, page - 1))}
      >
        {t('common.previous')}
      </button>
      <span aria-live="polite">{t('common.page', { page })}</span>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={!hasNext}
        onClick={() => onChange(page + 1)}
      >
        {t('common.next')}
      </button>
    </nav>
  );
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
export function NoticePage({ titleKey, bodyKey }: { titleKey: string; bodyKey: string }) {
  const { t } = useTranslation();
  return (
    <div className="state-panel notice-page" role="status">
      <span className="state-icon" aria-hidden="true">
        ⚠
      </span>
      <div>
        <h1>{t(titleKey)}</h1>
        <p>{t(bodyKey)}</p>
      </div>
    </div>
  );
}
