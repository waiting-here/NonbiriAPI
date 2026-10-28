import { useTranslation } from 'react-i18next';
import type { ManagedSafeSource } from '@shared/operations/charity';
import { endpointTagBadgeClass, endpointTagKeys } from './endpointTagLabels';

export function DonationSourceContext({
  source,
  showAddress = false,
}: {
  source: ManagedSafeSource;
  showAddress?: boolean;
}) {
  const { t } = useTranslation();
  const label =
    source.kind === 'custom'
      ? t('common.operations.charity.customSource', {
          connector: source.connector_type,
          baseUrl: source.base_url,
        })
      : t(
          source.category === 'subscription'
            ? 'common.operations.charity.mainstreamSubscription'
            : source.category === 'api_platform'
              ? 'common.operations.charity.mainstreamApiPlatform'
              : 'common.operations.charity.mainstreamSource',
          { name: source.name },
        );
  return (
    <div className="nb-stack nb-stack--sm">
      <span>{label}</span>
      {showAddress ? <small className="mono">{source.base_url}</small> : null}
      {source.endpoint_tags?.length ? (
        <div className="nb-actions" aria-label={t('common.operations.charity.currentEndpointTags')}>
          <span className="nb-small nb-muted">
            {t('common.operations.charity.currentEndpointTags')}
          </span>
          {source.endpoint_tags.map((tag) => (
            <span key={tag} className={endpointTagBadgeClass(tag)}>
              {t(endpointTagKeys[tag as keyof typeof endpointTagKeys] ?? 'common.unknown')}
            </span>
          ))}
        </div>
      ) : null}
    </div>
  );
}
