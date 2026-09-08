import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { RoleLogPanel } from '@shared/components/log';
import '@shared/operations/operations.css';
import { useUserSession } from '../data';

export function LogsPage() {
  const { t, i18n } = useTranslation();
  const session = useUserSession();
  const [params] = useSearchParams();
  const requestIDValues = params.getAll('request_id');
  const requested = requestIDValues[0] ?? null;
  const requestID =
    requestIDValues.length === 1 &&
    requested !== null &&
    /^req_[A-Za-z0-9_-]{21}[AQgw]$/.test(requested)
      ? requested
      : null;
  return (
    <div className="page ops-stack">
      <PageHeader
        eyebrow={t('user.logs.eyebrow')}
        title={t('user.logs.title')}
        description={t('user.logs.description')}
      />
      {requested && !requestID ? (
        <p role="alert">{t('common.operations.logs.requestUnavailable')}</p>
      ) : null}
      <RoleLogPanel
        role="user"
        language={i18n.resolvedLanguage}
        accountId={session.data?.user.id}
        scopeReady={!session.isPending && !session.error && Boolean(session.data?.user.id)}
        enabled={!session.isPending && !session.error}
        requestID={requestID}
      />
    </div>
  );
}
