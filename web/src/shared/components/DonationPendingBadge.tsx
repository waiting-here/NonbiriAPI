import { useEffect, useState } from 'react';
import { Link } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { charityKeys, getDonationBadge, type CharityRole } from '@shared/operations/charity';
import { clearStationSession } from '@shared/charityManagement';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import './donationPendingBadge.css';

export function DonationPendingBadge({
  role,
  accountID,
  onNavigate,
}: {
  role: CharityRole;
  accountID: string;
  onNavigate?: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const identity = `${role}:${accountID}`;
  const [revokedIdentity, setRevokedIdentity] = useState('');
  const revoked = revokedIdentity === identity;
  const badge = useQuery({
    queryKey: charityKeys.badge(role, accountID),
    queryFn: async ({ signal }) => {
      try {
        return await getDonationBadge(role, signal);
      } catch (error) {
        if (!signal.aborted && (isUnauthorized(error) || isForbidden(error))) {
          setRevokedIdentity(identity);
        }
        throw error;
      }
    },
    enabled: !revoked && accountID !== '',
    retry: false,
    staleTime: 0,
    refetchInterval: revoked ? false : 30_000,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: 'always',
  });
  const lost = isUnauthorized(badge.error) || isForbidden(badge.error);
  useEffect(() => {
    if (!revoked) return;
    clearStationSession(client, role);
  }, [revoked, client, role]);
  if (revoked || lost) return null;
  const count = !badge.error ? badge.data?.pending_count : undefined;
  const label =
    count === undefined
      ? t('common.donationHandling.badgeUnknown')
      : t('common.donationHandling.badgeCount', { count });
  const displayed = count === undefined ? '?' : BigInt(count) > 99n ? '99+' : count;
  return (
    <span className="donation-pending-badge">
      <Link
        to={
          role === 'admin' ? '/charity?handling=pending' : '/steward?tab=charity&handling=pending'
        }
        title={label}
        aria-label={label}
        onClick={onNavigate}
      >
        <span>{t('common.donationHandling.badgeLabel')}</span>
        <span className="donation-pending-badge__count" aria-live="polite">
          {displayed}
        </span>
      </Link>
      {badge.error ? (
        <button
          type="button"
          className="btn btn-quiet"
          disabled={badge.isFetching}
          onClick={() => void badge.refetch()}
          aria-label={t('common.donationHandling.badgeRetry')}
        >
          {t('common.retry')}
        </button>
      ) : null}
    </span>
  );
}
