import { useTranslation } from 'react-i18next';
import { CharityManagement } from '@shared/components/CharityManagement';
import { Card, LoadingState, PageHeader } from '@shared/components/States';
import { useUserSession } from '../data';

/**
 * Level-5 co-management placeholder. The nav entry is display-gated by the
 * session's effective_level, but this page holds no business capability: the
 * server-side steward prefix remains the authority and no capability is
 * rendered or implied before those routes exist.
 */
export function StewardPage() {
  const { t } = useTranslation();
  const session = useUserSession();
  if (session.isPending) return <LoadingState />;
  if (session.data?.user.effective_level !== 5) {
    return (
      <div className="page">
        <PageHeader eyebrow={t('app.name')} title={t('user.steward.title')} description={t('user.steward.description')} />
        <Card><p className="field-error" role="alert">{t('user.steward.accessDenied')}</p></Card>
      </div>
    );
  }
  return <CharityManagement frame="steward" />;
}
