import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Card, PageHeader } from '@shared/components/States';
import { UserPageGate } from '../components/UserPageGate';

function IssuesContent() {
  const { t } = useTranslation();
  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('user.issues.title')}
        description={t('user.issues.description')}
      />
      <Card>
        <h2>{t('user.issues.unavailableTitle')}</h2>
        <p>{t('user.issues.unavailableBody')}</p>
      </Card>
      <Card>
        <h2>{t('user.issues.safeTitle')}</h2>
        <p>{t('user.issues.safeBody')}</p>
        <Link className="btn btn-secondary" to="/endpoints">
          {t('user.home.endpointsLink')}
        </Link>
      </Card>
    </div>
  );
}

export function IssuesPage() {
  return (
    <UserPageGate>
      <IssuesContent />
    </UserPageGate>
  );
}
