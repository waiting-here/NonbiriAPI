import { useLocation, useParams } from 'react-router';
import { AnnouncementEditor } from '@shared/components/AnnouncementEditor';
import { ErrorState } from '@shared/components/States';
import { listReturnPath } from '@shared/operations/listReturn';
import { useAdminSession } from '../data';

export function AnnouncementDetailPage() {
  const location = useLocation();
  const { announcementId = '' } = useParams();
  const session = useAdminSession();
  const account = session.data ? `admin:${session.data.admin.username}` : '';
  if (session.error)
    return (
      <div className="page ops-page">
        <ErrorState error={session.error} onRetry={() => void session.refetch()} />
      </div>
    );
  return (
    <AnnouncementEditor
      key={`${account}:${announcementId}`}
      role="admin"
      account={account}
      announcementId={announcementId}
      backTo={listReturnPath(location.state, '/announcements')}
    />
  );
}
