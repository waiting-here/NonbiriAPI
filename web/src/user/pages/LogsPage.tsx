import { useTranslation } from 'react-i18next';
import { PageHeader } from '@shared/components/States';
import { RoleLogPanel } from '@shared/components/log';
import '@shared/operations/operations.css';

export function LogsPage() {
  const { t, i18n } = useTranslation();
  return (
    <div className="page ops-stack">
      <PageHeader
        eyebrow={t('user.logs.eyebrow')}
        title={t('user.logs.title')}
        description={t('user.logs.description')}
      />
      <RoleLogPanel role="user" language={i18n.resolvedLanguage} />
    </div>
  );
}
