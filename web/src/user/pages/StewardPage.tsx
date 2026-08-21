import { useTranslation } from 'react-i18next';
import { Card, PageHeader } from '@shared/components/States';

/**
 * Level-5 co-management placeholder. The nav entry is display-gated by the
 * session's effective_level, but this page holds no business capability: the
 * server-side steward prefix remains the authority and no capability is
 * rendered or implied before those routes exist.
 */
export function StewardPage() {
  const { t } = useTranslation();
  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('user.steward.title')}
        description={t('user.steward.description')}
      />
      <Card>
        <h2>{t('user.steward.comingTitle')}</h2>
        <p className="muted">{t('user.steward.comingBody')}</p>
      </Card>
    </div>
  );
}
