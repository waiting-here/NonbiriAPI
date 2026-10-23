import { useTranslation } from 'react-i18next';

const reasons = {
  ended: 'common.connections.unavailableReasons.ended',
  not_approved: 'common.connections.unavailableReasons.notApproved',
  disabled: 'common.connections.unavailableReasons.disabled',
  review_required: 'common.connections.unavailableReasons.reviewRequired',
  failure_disabled: 'common.connections.unavailableReasons.failureDisabled',
  rate_limited: 'common.connections.unavailableReasons.rateLimited',
  no_service_route: 'common.connections.unavailableReasons.noServiceRoute',
} as const;

export function ConnectionUnavailable({ reason }: { reason: string }) {
  const { t } = useTranslation();
  const key = Object.hasOwn(reasons, reason)
    ? reasons[reason as keyof typeof reasons]
    : 'common.connections.unavailableReasons.unavailable';
  return <p className="nb-sub">{t(key)}</p>;
}
