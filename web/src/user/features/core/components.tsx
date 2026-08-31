import { useState, type ReactNode } from 'react';
import { AuthRequired } from '@shared/components/States';
import { Icon, type IconName } from '@shared/components/Icon';
import { copyText } from '@shared/utils/clipboard';
import { isApiError, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { useCoreCopy } from './copy';
import { callerSafeError } from './request';
import { useCoreMe, useCoreSession, type CoreSessionBoundary } from './queries';
import type { ConnectorType, DiscoveryEvidence, UserProfile } from './types';

export function CoreLoading({ compact = false }: { compact?: boolean }) {
  const { t } = useCoreCopy();
  return (
    <div
      className={`core-state core-state--loading${compact ? ' core-state--compact' : ''}`}
      role="status"
    >
      <Icon name="spark" />
      <span>{t('common.loading')}</span>
    </div>
  );
}

export function CoreErrorPanel({
  error,
  onRetry,
  compact = false,
}: {
  error: unknown;
  onRetry?: () => void;
  compact?: boolean;
}) {
  const { t } = useCoreCopy();
  let body = t('common.errorBody');
  if (isApiError(error)) {
    if (error.status === 409 || error.code === 'conflict') body = t('common.conflict');
    else if (error.code === 'maintenance' || error.status === 503) body = t('common.maintenance');
    else if (error.status === 401 || error.status === 403) body = t('common.permissionLost');
    else body = callerSafeError(error) ?? t('common.errorBody');
  }
  return (
    <div
      className={`core-state core-state--error${compact ? ' core-state--compact' : ''}`}
      role="alert"
    >
      <Icon name="error" />
      <div>
        <strong>{t('common.errorTitle')}</strong>
        <p>{body}</p>
        {onRetry ? (
          <button type="button" className="btn btn-secondary" onClick={onRetry}>
            {t('common.retry')}
          </button>
        ) : null}
      </div>
    </div>
  );
}

export function CoreUnavailable({ compact = false }: { compact?: boolean }) {
  const { t } = useCoreCopy();
  return (
    <div
      className={`core-state core-state--warning${compact ? ' core-state--compact' : ''}`}
      role="status"
    >
      <Icon name="info" />
      <div>
        <strong>{t('common.unavailableTitle')}</strong>
        <p>{t('common.unavailableBody')}</p>
      </div>
    </div>
  );
}

export function CoreEmpty({
  title,
  body,
  action,
}: {
  title: string;
  body: string;
  action?: ReactNode;
}) {
  return (
    <div className="core-state core-state--empty">
      <Icon name="spark" />
      <div>
        <strong>{title}</strong>
        <p>{body}</p>
        {action ? <div className="core-state__actions">{action}</div> : null}
      </div>
    </div>
  );
}

export function CoreProfileGate({
  session,
  children,
  signedOut,
}: {
  session: CoreSessionBoundary;
  children: (user: UserProfile) => ReactNode;
  signedOut?: ReactNode;
}) {
  const me = useCoreMe(session.accountId, session.profile === undefined);
  if (session.profile) return children(session.profile);
  if (me.isPending) return <CoreLoading />;
  if (me.error) {
    if (isUnauthorized(me.error) || isNotFoundError(me.error)) {
      return <>{signedOut ?? <AuthRequired station="user" />}</>;
    }
    return <CoreErrorPanel error={me.error} onRetry={() => void me.refetch()} />;
  }
  return children(me.data.user);
}

export function CoreUserGate({ children }: { children: (user: UserProfile) => ReactNode }) {
  const session = useCoreSession();
  if (session.isPending) return <CoreLoading />;
  if (session.error) {
    if (isUnauthorized(session.error) || isNotFoundError(session.error))
      return <AuthRequired station="user" />;
    return <CoreErrorPanel error={session.error} onRetry={() => void session.refetch()} />;
  }
  if (session.data === null) return <AuthRequired station="user" />;
  return <CoreProfileGate session={session.data}>{children}</CoreProfileGate>;
}

function groupInteger(value: string): string {
  let output = '';
  for (let index = 0; index < value.length; index += 1) {
    if (index > 0 && (value.length - index) % 3 === 0) output += ',';
    output += value[index];
  }
  return output;
}

function formatExactDecimal(value: string): string {
  const negative = value.startsWith('-');
  const unsigned = negative ? value.slice(1) : value;
  const [whole = '0', fraction] = unsigned.split('.');
  return `${negative ? '-' : ''}${groupInteger(whole)}${fraction ? `.${fraction}` : ''}`;
}

export function ExactCredits({ value }: { value: string }) {
  return <span className="core-number">{formatExactDecimal(value)}</span>;
}

export function ExactCount({ value }: { value: string }) {
  return <span className="core-number">{groupInteger(value)}</span>;
}

export function CoreTime({ value }: { value: number | null }) {
  const { language } = useCoreCopy();
  if (value === null) return <span>—</span>;
  return (
    <time dateTime={new Date(value * 1000).toISOString()}>
      {new Intl.DateTimeFormat(language === 'zh' ? 'zh-CN' : 'en', {
        dateStyle: 'medium',
        timeStyle: 'short',
      }).format(new Date(value * 1000))}
    </time>
  );
}

export function SafeCopyValue({ value, label }: { value: string; label: string }) {
  const { t } = useCoreCopy();
  const [copied, setCopied] = useState(false);
  return (
    <span className="core-copy-value">
      <code className="core-mono">{value}</code>
      <button
        type="button"
        className="btn btn-quiet core-copy-value__button"
        aria-label={`${t('common.copy')} ${label}`}
        onClick={() => {
          void copyText(value).then((ok) => setCopied(ok));
        }}
      >
        {copied ? t('common.copied') : t('common.copy')}
      </button>
    </span>
  );
}

export function StatusPill({
  tone,
  children,
  icon,
}: {
  tone: 'success' | 'neutral' | 'warning' | 'danger';
  children: ReactNode;
  icon?: IconName;
}) {
  return (
    <span className={`core-status core-status--${tone}`}>
      {icon ? <Icon name={icon} /> : null}
      {children}
    </span>
  );
}

export function ConnectorLabel({ value }: { value: ConnectorType }) {
  const { t } = useCoreCopy();
  return <>{value === 'openai-compatible' ? t('connector.openai') : t('connector.anthropic')}</>;
}

export function DiscoveryStatus({ evidence }: { evidence: DiscoveryEvidence }) {
  const { t } = useCoreCopy();
  if (evidence.state === 'unknown') {
    return (
      <StatusPill tone="neutral" icon="info">
        {t('endpoints.discoveryUnknown')}
      </StatusPill>
    );
  }
  if (evidence.state === 'checking') {
    return (
      <StatusPill tone="warning" icon="spark">
        {t('endpoints.discoveryChecking')}
      </StatusPill>
    );
  }
  if (evidence.state === 'succeeded' && evidence.result === 'empty') {
    return (
      <StatusPill tone="neutral" icon="check">
        {t('endpoints.discoveryEmpty')}
      </StatusPill>
    );
  }
  if (evidence.state === 'succeeded') {
    return (
      <StatusPill tone="success" icon="check">
        {t('endpoints.discoveryNonempty', { count: evidence.count ?? '0' })}
      </StatusPill>
    );
  }
  const reason =
    evidence.safe_class === 'auth'
      ? t('endpoints.discoveryReasonAuth')
      : evidence.safe_class === 'rate_limit'
        ? t('endpoints.discoveryReasonRateLimit')
        : evidence.safe_class === 'timeout'
          ? t('endpoints.discoveryReasonTimeout')
          : evidence.safe_class === 'protocol'
            ? t('endpoints.discoveryReasonProtocol')
            : evidence.safe_class === 'transport'
              ? t('endpoints.discoveryReasonTransport')
              : t('endpoints.discoveryReasonInterrupted');
  return (
    <StatusPill tone="danger" icon="error">
      {t('endpoints.discoveryFailed', { reason })}
    </StatusPill>
  );
}

export function MutationNotice({
  outcome,
}: {
  outcome: 'success' | 'conflict' | 'unknown' | 'error' | null;
}) {
  const { t } = useCoreCopy();
  if (!outcome || outcome === 'success') return null;
  const message =
    outcome === 'conflict'
      ? t('common.conflict')
      : outcome === 'unknown'
        ? t('common.outcomeUnknown')
        : t('common.errorBody');
  return (
    <p className="core-inline-error" role="alert">
      {message}
    </p>
  );
}
