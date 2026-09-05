import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { RoleLogPanel } from '@shared/components/log';
import '@shared/operations/operations.css';

export function LogsPage() {
  const { t, i18n } = useTranslation();
  const [params, setParams] = useSearchParams();
  const requested = params.get('request_id');
  const requestID = requested && /^req_[A-Za-z0-9_-]{21}[AQgw]$/.test(requested) ? requested : null;
  return (
    <div className="page ops-stack">
      <PageHeader
        eyebrow={t('user.logs.eyebrow')}
        title={t('user.logs.title')}
        description={t('user.logs.description')}
      />
      {requested && !requestID ? <p role="alert">{t('common.operations.logs.requestUnavailable')}</p> : null}
      <RoleLogPanel role="user" language={i18n.resolvedLanguage} requestID={requestID} onRequestClose={() => {
        setParams((previous) => { const next = new URLSearchParams(previous); next.delete('request_id'); return next; }, { replace: true });
      }} />
    </div>
  );
}
