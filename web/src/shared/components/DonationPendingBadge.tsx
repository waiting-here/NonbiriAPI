import { Button } from '@shared/components/ui/Button';
import {
  hasStewardPermission,
  type StewardPermissions,
} from '@shared/operations/stewardPermissions';
import { useEffect, useState } from 'react';
import { Link } from 'react-router';
import { skipToken, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { charityKeys, getDonationBadge, type CharityRole } from '@shared/operations/charity';
import { getManagedDonationsPage } from '@shared/operations/donationPages';
import { clearStationSession } from '@shared/charityManagement';
import { isManagementForbidden, isUnauthorized } from '@shared/query/http';
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
  const session = useQuery<{
    user?: { id?: string; effective_level: number; steward_permissions?: StewardPermissions };
  }>({
    queryKey: ['user', 'session'],
    queryFn: skipToken,
  }).data;
  const currentAccount =
    (!session?.user?.id || session.user.id === accountID) &&
    session?.user?.steward_permissions?.donation_scope !== 'none';
  const handling =
    role === 'admin' || (currentAccount && hasStewardPermission(session?.user, 'donations.handle'));
  const reviewOnly =
    !handling && currentAccount && hasStewardPermission(session?.user, 'donations.review');
  const eligible = handling || reviewOnly;
  const revision =
    role === 'admin'
      ? 'admin'
      : String(session?.user?.effective_level ?? '') +
        ':' +
        (session?.user?.steward_permissions?.revision ?? '0');
  const identity = `${role}:${accountID}:${revision}`;
  const [revokedIdentity, setRevokedIdentity] = useState('');
  const revoked = revokedIdentity === identity;
  const badge = useQuery({
    queryKey:
      role === 'admin'
        ? charityKeys.badge(role, accountID)
        : [...charityKeys.badge(role, accountID), revision],
    queryFn: async ({ signal }) => {
      try {
        if (!reviewOnly) return await getDonationBadge(role, signal);
        const page = await getManagedDonationsPage(
          role,
          { review_status: 'any_pending' },
          '1',
          10,
          signal,
        );
        return { pending_count: page.pagination.total_items, server_now: 0 };
      } catch (error) {
        if (!signal.aborted && (isUnauthorized(error) || isManagementForbidden(error))) {
          setRevokedIdentity(identity);
        }
        throw error;
      }
    },
    enabled: eligible && !revoked && accountID !== '',
    retry: false,
    staleTime: 0,
    refetchInterval: revoked ? false : 30_000,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: 'always',
  });
  const lost = isUnauthorized(badge.error) || isManagementForbidden(badge.error);
  useEffect(() => {
    if (!revoked) return;
    if (isUnauthorized(badge.error)) clearStationSession(client, role);
    else {
      if (role === 'steward')
        void client.invalidateQueries({ queryKey: ['user', 'session'], exact: true });
      void client.cancelQueries({ queryKey: charityKeys.root(role) });
      client.removeQueries({ queryKey: charityKeys.root(role) });
    }
  }, [revoked, badge.error, client, role]);
  if (!eligible || revoked || lost) return null;
  const count = !badge.error ? badge.data?.pending_count : undefined;
  if (count === '0') return null;
  const label =
    count === undefined
      ? t(reviewOnly ? 'common.governance.reviewUnknown' : 'common.donationHandling.badgeUnknown')
      : t(reviewOnly ? 'common.governance.reviewCount' : 'common.donationHandling.badgeCount', {
          count,
        });
  const displayed = count === undefined ? '?' : BigInt(count) > 99n ? '99+' : count;
  return (
    <span className="donation-pending-badge">
      <Link
        to={
          role === 'admin'
            ? '/charity?handling=pending'
            : reviewOnly
              ? '/steward?tab=charity&charity_section=donations&donation_status=any_pending'
              : '/steward?tab=charity&charity_section=donations&handling=pending'
        }
        title={label}
        aria-label={label}
        onClick={onNavigate}
      >
        <span>
          {t(reviewOnly ? 'common.governance.reviewOnly' : 'common.donationHandling.badgeLabel')}
        </span>
        <span className="donation-pending-badge__count" aria-live="polite">
          {displayed}
        </span>
      </Link>
      {badge.error ? (
        <Button
          type="button"
          variant="ghost"
          disabled={badge.isFetching}
          onClick={() => void badge.refetch()}
          aria-label={t('common.donationHandling.badgeRetry')}
        >
          {t('common.retry')}
        </Button>
      ) : null}
    </span>
  );
}
