import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useLocation } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { CharityManagement } from '@shared/components/CharityManagement';
import { PageHeader } from '@shared/components/States';
import { useAdminSession } from '../data';
import '@shared/operations/operations.css';

export function CharityPage() {
  const { t } = useTranslation();
  const location = useLocation();
  const client = useQueryClient();
  const session = useAdminSession();
  const accountId = session.data ? `admin:${session.data.admin.username}` : undefined;
  const clearAuthority = useCallback(() => {
    void client.invalidateQueries({ queryKey: ['admin', 'session'] });
  }, [client]);
  return (
    <div className="page ops-page">
      <PageHeader
        title={t('admin.charity.title')}
        description={t('admin.charity.description')}
        actions={
          <Link
            className="nb-btn nb-btn--secondary"
            to="/charity/channel-presets"
            state={{ returnTo: location.pathname + location.search }}
          >
            {t('common.bindingPresets.title')}
          </Link>
        }
      />
      <CharityManagement
        key={accountId}
        frame="admin"
        accountId={accountId}
        onCapabilityLoss={clearAuthority}
      />
    </div>
  );
}
