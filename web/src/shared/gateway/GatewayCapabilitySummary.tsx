import { useTranslation } from 'react-i18next';
import type { GatewayCapabilityPolicy } from './capabilities';
import {
  gatewayAdapterLabels,
  gatewayCacheLabels,
  gatewayEffortLabels,
  gatewayStorageLabels,
} from './copy';

/** Receives an already-authorized projection; never loads administrator data. */
export function GatewayCapabilitySummary({ entry }: { entry?: GatewayCapabilityPolicy | null }) {
  const { t } = useTranslation();
  if (!entry) return <p className="muted">{t('gatewayCapabilities.absent')}</p>;
  return (
    <dl className="ops-kv">
      <dt>{t('gatewayCapabilities.adapter')}</dt>
      <dd>{t(gatewayAdapterLabels[entry.adapter])}</dd>
      <dt>{t('gatewayCapabilities.efforts')}</dt>
      <dd>
        {entry.efforts.length
          ? entry.efforts.map((effort) => t(gatewayEffortLabels[effort])).join(' · ')
          : t('gatewayCapabilities.noEfforts')}
      </dd>
      <dt>{t('gatewayCapabilities.output')}</dt>
      <dd>
        {entry.max_output_tokens === 0
          ? t('gatewayCapabilities.noCeiling')
          : t('gatewayCapabilities.tokenCount', { count: entry.max_output_tokens })}
      </dd>
      <dt>{t('gatewayCapabilities.storage')}</dt>
      <dd>{t(gatewayStorageLabels[entry.storage])}</dd>
      <dt>{t('gatewayCapabilities.cache')}</dt>
      <dd>{t(gatewayCacheLabels[entry.cache])}</dd>
    </dl>
  );
}
