import { useTranslation } from 'react-i18next';
import { Note } from '@shared/components/ui';
import { NativeServicesField } from '@shared/components/NativeServicesField';
import type { NativeService } from '@shared/nativeServices';
import type { EndpointKeyChoice, EndpointKeySummary } from './types';
export interface DonationShare {
  services: NativeService[];
}
export function DonationShareFields({
  choice,
  value,
  onChange,
  disabled,
}: {
  choice: Pick<EndpointKeyChoice, 'snapshotStale'> & {
    key: Pick<EndpointKeySummary, 'nativeServices' | 'credentialVersion'>;
  };
  value: DonationShare;
  onChange: (value: DonationShare) => void;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className="nb-stack">
      {choice.snapshotStale ? (
        <Note tone="warn">{t('common.connections.donationSnapshotChanged')}</Note>
      ) : null}
      <NativeServicesField
        options={choice.key.nativeServices ?? []}
        value={value.services}
        onChange={(services) => onChange({ services })}
        disabled={disabled}
      />
      <Note>
        {t('common.connections.shareExactVersion', {
          version: choice.key.credentialVersion ?? '—',
        })}
      </Note>
    </div>
  );
}
