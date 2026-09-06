import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import { KeyLimitSummary } from '@shared/components/KeyRoutingLimits';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import {
  ADMIN_CHARITY_ENDED_REASONS,
  ADMIN_CHARITY_KEY_STATES,
  ADMIN_CHARITY_STATUSES,
  adminCharityKeys,
  getAdminCharityDonations,
  groupAdminCharityDonations,
  type AdminCharityGroup,
  type AdminCharityKey,
  type AdminCharityStatus,
} from './adminCharity';
import '@shared/operations/operations.css';

const STATUS_LABEL_KEYS: Record<AdminCharityStatus, string> = {
  pending: 'admin.charity.status.pending',
  approved: 'admin.charity.status.approved',
  rejected: 'admin.charity.status.rejected',
  deleted: 'admin.charity.status.deleted',
  expired: 'admin.charity.status.expired',
};

const KEY_STATE_LABEL_KEYS: Record<(typeof ADMIN_CHARITY_KEY_STATES)[number], string> = {
  pending: 'common.operations.charity.charityState.pending',
  available: 'common.operations.charity.charityState.available',
  disabled: 'common.operations.charity.charityState.disabled',
  suspended: 'common.operations.charity.charityState.suspended',
  exhausted: 'common.operations.charity.charityState.exhausted',
  expired: 'common.operations.charity.charityState.expired',
  ended: 'common.operations.charity.charityState.ended',
};

function statusIsActive(status: AdminCharityStatus): boolean {
  return status === 'pending' || status === 'approved';
}

function keyStateIsActive(state: AdminCharityKey['charity_state']): boolean {
  return state === 'pending' || state === 'available';
}

function UsageValue({
  label,
  used,
  inflight,
  limit,
}: {
  label: string;
  used: string;
  inflight: string;
  limit: string | null;
}) {
  const { t } = useTranslation();
  return (
    <div>
      <dt>{label}</dt>
      <dd>
        {t('common.operations.charity.usageLimit', {
          used,
          inflight,
          limit: limit ?? t('common.operations.charity.unlimited'),
        })}
      </dd>
    </div>
  );
}

function blockingReason(key: AdminCharityKey, t: (key: string) => string): string | null {
  if (!key.physical_enabled) return t('admin.charity.group.blocked.physical');
  if (key.streak.failure_disabled) return t('admin.charity.group.blocked.failureDisabled');
  switch (key.charity_state) {
    case 'disabled':
      return t('admin.charity.group.blocked.disabled');
    case 'suspended':
      return t('admin.charity.group.blocked.suspended');
    case 'exhausted':
      return t('admin.charity.group.blocked.exhausted');
    case 'expired':
      return t('admin.charity.group.blocked.expired');
    case 'ended':
      return t('admin.charity.group.blocked.ended');
    default:
      return null;
  }
}

function endedReasonLabel(
  value: AdminCharityKey['ended_reason'],
  t: (key: string) => string,
): string | null {
  if (value === null) return null;
  const labels: Record<(typeof ADMIN_CHARITY_ENDED_REASONS)[number], string> = {
    withdrawn: t('admin.charity.group.endedReasonValue.withdrawn'),
    terminated: t('admin.charity.group.endedReasonValue.terminated'),
    expired: t('admin.charity.group.endedReasonValue.expired'),
    member_removed: t('admin.charity.group.endedReasonValue.memberRemoved'),
    account_deleted: t('admin.charity.group.endedReasonValue.accountDeleted'),
  };
  return labels[value];
}

function GroupSource({ group }: { group: AdminCharityGroup }) {
  const { t } = useTranslation();
  const label =
    group.source.kind === 'custom'
      ? t('admin.charity.group.custom')
      : group.source.category === 'subscription'
        ? t('admin.charity.group.mainstreamSubscription', { name: group.source.name })
        : t('admin.charity.group.mainstreamApiPlatform', { name: group.source.name });
  return (
    <div className="ops-subcard">
      <h3>{label}</h3>
      <dl className="ops-kv">
        <dt>{t('admin.charity.group.connector')}</dt>
        <dd>{group.source.connector_type}</dd>
        <dt>{t('admin.charity.group.baseUrl')}</dt>
        <dd className="ops-wrap">{group.source.base_url}</dd>
        {group.source.kind === 'mainstream' ? (
          <>
            <dt>{t('admin.charity.group.channelRevision')}</dt>
            <dd>{group.source.channel_revision}</dd>
          </>
        ) : null}
      </dl>
    </div>
  );
}

function GroupKey({
  donationID,
  donationStatus,
  keyValue,
}: {
  donationID: string;
  donationStatus: AdminCharityStatus;
  keyValue: AdminCharityKey;
}) {
  const { t } = useTranslation();
  const blocked = blockingReason(keyValue, t);
  const endedReason = endedReasonLabel(keyValue.ended_reason, t);
  return (
    <article className="ops-subcard">
      <div className="ops-toolbar">
        <h4>
          {t('admin.charity.group.keyHeading', {
            id: keyValue.id,
            head: keyValue.display_head,
            tail: keyValue.display_tail,
          })}
        </h4>
        <StatusBadge
          active={statusIsActive(donationStatus)}
          danger={!statusIsActive(donationStatus)}
          label={t(STATUS_LABEL_KEYS[donationStatus])}
        />
      </div>
      <dl className="ops-kv">
        <dt>{t('admin.charity.group.donation')}</dt>
        <dd>{donationID}</dd>
        <dt>{t('admin.charity.group.physical')}</dt>
        <dd>
          <StatusBadge
            active={keyValue.physical_enabled}
            label={t(
              keyValue.physical_enabled
                ? 'admin.charity.group.physicalEnabled'
                : 'admin.charity.group.physicalDisabled',
            )}
          />
        </dd>
        <dt>{t('admin.charity.group.keyState')}</dt>
        <dd>
          <StatusBadge
            active={keyStateIsActive(keyValue.charity_state)}
            danger={keyValue.charity_state === 'ended' || keyValue.charity_state === 'expired'}
            label={t(KEY_STATE_LABEL_KEYS[keyValue.charity_state])}
          />
        </dd>
        <dt>{t('admin.charity.group.effectiveExpiry')}</dt>
        <dd>
          {keyValue.expires_at === null
            ? t('common.operations.charity.noExpiry')
            : formatDateTime(keyValue.expires_at)}
        </dd>
        <dt>{t('admin.charity.group.authorizedExpiry')}</dt>
        <dd>
          {keyValue.authorized_expires_at === null
            ? t('common.operations.charity.noExpiry')
            : formatDateTime(keyValue.authorized_expires_at)}
        </dd>
      </dl>
      <KeyLimitSummary concurrency={keyValue.max_concurrency} rpm={keyValue.max_rpm} readOnly />
      {blocked ? <p className="inline-notice">{blocked}</p> : null}
      {endedReason ? (
        <p className="inline-notice">
          {t('admin.charity.group.endedReason')}: {endedReason}
        </p>
      ) : null}
      <dl className="ops-kv">
        <UsageValue
          label={t('common.operations.charity.priceLimitCredits')}
          used={keyValue.usage.price_used}
          inflight={keyValue.usage.price_inflight}
          limit={keyValue.limits.price}
        />
        <UsageValue
          label={t('common.operations.charity.callLimit')}
          used={keyValue.usage.calls_used}
          inflight={keyValue.usage.calls_inflight}
          limit={keyValue.limits.calls}
        />
        <UsageValue
          label={t('common.operations.charity.tokenLimit')}
          used={keyValue.usage.tokens_used}
          inflight={keyValue.usage.tokens_inflight}
          limit={keyValue.limits.tokens}
        />
        <div>
          <dt>{t('common.operations.charity.tokenReserve')}</dt>
          <dd>{keyValue.token_reserve}</dd>
        </div>
        <div>
          <dt>{t('common.operations.charity.failureStreak', { count: keyValue.streak.count })}</dt>
          <dd>
            {keyValue.streak.failure_disabled
              ? t('admin.charity.group.failureDisabled')
              : t('admin.charity.group.failureEnabled')}
          </dd>
        </div>
      </dl>
    </article>
  );
}

function GroupCard({ group }: { group: AdminCharityGroup }) {
  return (
    <Card>
      <GroupSource group={group} />
      <div className="ops-stack">
        {group.items.map((item) => (
          <GroupKey
            key={`${item.donation_id}:${item.key.id}`}
            donationID={item.donation_id}
            donationStatus={item.donation_status}
            keyValue={item.key}
          />
        ))}
      </div>
    </Card>
  );
}

export function AdminCharityGroupsPanel() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const pager = useCursorPager();
  const [status, setStatus] = useState<'' | AdminCharityStatus>('');
  const donations = useQuery({
    queryKey: adminCharityKeys.donations(status, pager.cursor),
    queryFn: ({ signal }) => getAdminCharityDonations(status, pager.cursor, signal),
    retry: false,
  });
  const authorityLost = isUnauthorized(donations.error) || isForbidden(donations.error);

  useEffect(() => {
    if (authorityLost) clearStationSession(client, 'admin');
  }, [authorityLost, client]);

  if (authorityLost) {
    return <ErrorState error={donations.error} />;
  }

  const groups = donations.data ? groupAdminCharityDonations(donations.data.data) : [];
  return (
    <Card>
      <div className="ops-toolbar">
        <label>
          <span>{t('admin.charity.statusFilter')}</span>
          <select
            value={status}
            onChange={(event) => {
              pager.reset();
              setStatus(event.target.value as '' | AdminCharityStatus);
            }}
          >
            <option value="">{t('admin.charity.allStatuses')}</option>
            {ADMIN_CHARITY_STATUSES.map((value) => (
              <option key={value} value={value}>
                {t(STATUS_LABEL_KEYS[value])}
              </option>
            ))}
          </select>
        </label>
      </div>
      {donations.isPending ? (
        <LoadingState />
      ) : donations.error ? (
        <ErrorState error={donations.error} onRetry={() => void donations.refetch()} />
      ) : groups.length === 0 ? (
        <EmptyState
          title={t('admin.charity.noDonations')}
          body={t('admin.charity.noDonationsBody')}
        />
      ) : (
        <div className="ops-stack">
          {groups.map((group) => (
            <GroupCard
              key={
                group.source.kind === 'custom'
                  ? `${group.source.kind}:${group.items[0]?.donation_id}:${group.items[0]?.key.id}`
                  : JSON.stringify([
                      group.source.channel_id,
                      group.source.channel_revision,
                      group.source.connector_type,
                      group.source.base_url,
                      group.source.name,
                      group.source.category,
                    ])
              }
              group={group}
            />
          ))}
        </div>
      )}
      {!donations.isPending && !donations.error ? (
        <CursorPagination
          page={pager.page}
          nextCursor={donations.data?.next_cursor}
          onPrevious={pager.previous}
          onNext={pager.next}
        />
      ) : null}
    </Card>
  );
}
