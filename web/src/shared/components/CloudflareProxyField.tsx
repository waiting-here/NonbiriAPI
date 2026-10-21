import { useTranslation } from 'react-i18next';
import type { CloudflareProxyMode } from '@shared/cloudflareProxy';
import { Toggle } from './ui';

const modeKeys = {
  worker: 'common.cloudflareProxy.worker',
  legacy: 'common.cloudflareProxy.legacy',
  direct: 'common.cloudflareProxy.direct',
} as const;

function useCloudflareStatus(enabled: boolean, mode?: CloudflareProxyMode) {
  const { t } = useTranslation();
  return enabled
    ? t(mode ? modeKeys[mode] : 'common.cloudflareProxy.new')
    : t('common.cloudflareProxy.off');
}

export function CloudflareProxyField({
  enabled,
  mode,
  disabled,
  onChange,
}: {
  enabled: boolean;
  mode?: CloudflareProxyMode;
  disabled?: boolean;
  onChange: (enabled: boolean) => void;
}) {
  const { t } = useTranslation();
  const status = useCloudflareStatus(enabled, mode);
  return (
    <Toggle
      label={t('common.cloudflareProxy.label')}
      description={status}
      checked={enabled}
      disabled={disabled}
      onChange={onChange}
    />
  );
}

export function CloudflareProxySummary({
  enabled,
  mode,
}: {
  enabled: boolean;
  mode: CloudflareProxyMode;
}) {
  const { t } = useTranslation();
  const status = useCloudflareStatus(enabled, mode);
  return (
    <div>
      <dt>{t('common.cloudflareProxy.label')}</dt>
      <dd>{status}</dd>
    </div>
  );
}
