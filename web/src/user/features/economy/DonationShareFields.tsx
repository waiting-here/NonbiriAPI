import { useTranslation } from 'react-i18next';
import { Note } from '@shared/components/ui';
import type { EndpointKeyChoice, EndpointKeySummary } from './types';
export function DonationShareFields({
  choice,
}: {
  choice: Pick<EndpointKeyChoice, 'snapshotStale'> & {
    key: Pick<EndpointKeySummary, 'nativeServices' | 'credentialVersion'>;
  };
}) {
  const { t } = useTranslation();
  return (
    <div className="nb-stack">
      {choice.snapshotStale ? (
        <Note tone="warn">{t('common.connections.donationSnapshotChanged')}</Note>
      ) : null}
      <Note>
        {t('common.connections.shareExactVersion', {
          version: choice.key.credentialVersion ?? '—',
        })}
      </Note>
    </div>
  );
}
