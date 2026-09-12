import { AnnouncementManagement } from '@shared/components/AnnouncementManagement';
import { useAdminSession } from '../data';

export function AnnouncementsPage() {
  const session = useAdminSession();
  const account = session.data ? `admin:${session.data.admin.username}` : undefined;
  return (
    <AnnouncementManagement
      key={account ?? 'anonymous'}
      role="admin"
      account={account}
      sessionError={session.error}
    />
  );
}
