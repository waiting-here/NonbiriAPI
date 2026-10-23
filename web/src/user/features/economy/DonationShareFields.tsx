import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Field, Note } from '@shared/components/ui';
import { ErrorState } from '@shared/components/States';
import { NativeServicesField } from '@shared/components/NativeServicesField';
import type { NativeService } from '@shared/nativeServices';
import { connectionAPI } from '../core/connections/api';
import { connectionKeys } from '../core/connections/ResourceList';
import type { EndpointKeyChoice, EndpointKeySummary } from './types';
export interface DonationShare {
  services: NativeService[];
  sourceId?: string;
  sourceRevision?: string;
  initializeSource?: boolean;
}
export function DonationShareFields({
  accountId,
  choice,
  value,
  onChange,
  disabled,
}: {
  accountId: string;
  choice: Pick<EndpointKeyChoice, 'snapshotStale'> & {
    key: Pick<
      EndpointKeySummary,
      | 'nativeServices'
      | 'credentialId'
      | 'credentialVersion'
      | 'grantRevision'
      | 'discoverySourceId'
    >;
  };
  value: DonationShare;
  onChange: (value: DonationShare) => void;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const sourceId = choice.key.discoverySourceId;
  const source = useQuery({
    queryKey: [...connectionKeys.root(accountId), 'source', sourceId ?? 'none'],
    queryFn: ({ signal }) => connectionAPI.source(sourceId!, signal),
    enabled: Boolean(sourceId),
  });
  const compatible =
    source.data?.enabled &&
    source.data.credential_available !== false &&
    source.data.credential_id === choice.key.credentialId &&
    source.data.credential_version === choice.key.credentialVersion;
  useEffect(() => {
    if (!value.initializeSource || disabled || choice.snapshotStale) return;
    if (sourceId && !source.isSuccess && !source.isError) return;
    onChange({
      ...value,
      initializeSource: false,
      ...(compatible && source.data
        ? { sourceId: source.data.id, sourceRevision: source.data.discovery_source_revision }
        : {}),
    });
  }, [
    choice.snapshotStale,
    compatible,
    disabled,
    onChange,
    source.data,
    source.isSuccess,
    source.isError,
    sourceId,
    value,
  ]);
  return (
    <div className="nb-stack">
      {choice.snapshotStale ? (
        <Note tone="warn">{t('common.connections.donationSnapshotChanged')}</Note>
      ) : null}
      <NativeServicesField
        options={choice.key.nativeServices ?? []}
        value={value.services}
        onChange={(services) => onChange({ ...value, services })}
        disabled={disabled}
      />
      <Note>
        {t('common.connections.shareExactVersion', {
          version: choice.key.credentialVersion ?? '—',
        })}
      </Note>
      {sourceId ? (
        <div className="donation-share-fields__source">
          <Field
            label={t('common.connections.shareDiscovery')}
            help={compatible ? source.data!.models_url : t('common.connections.sourceNeedsRefresh')}
          >
            {(props) => (
              <input
                {...props}
                type="checkbox"
                checked={Boolean(value.sourceId)}
                disabled={disabled || (!compatible && !value.sourceId)}
                onChange={(event) =>
                  onChange({
                    services: value.services,
                    initializeSource: false,
                    ...(event.target.checked && source.data
                      ? {
                          sourceId: source.data.id,
                          sourceRevision: source.data.discovery_source_revision,
                        }
                      : {}),
                  })
                }
              />
            )}
          </Field>
          {source.isError ? (
            <ErrorState error={source.error} onRetry={() => void source.refetch()} />
          ) : null}
        </div>
      ) : (
        <p className="nb-sub">{t('common.connections.noDiscoveryShared')}</p>
      )}
    </div>
  );
}
