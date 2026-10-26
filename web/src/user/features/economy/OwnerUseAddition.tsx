import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Note } from '@shared/components/ui';
import { ErrorState, LoadingState } from '@shared/components/States';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import {
  addDonationUses,
  getDonationUses,
  type AddDonationUsesInput,
} from '@shared/operations/donationUses';
import { useEndpointCreateOptions } from '../core/queries';
import { readEndpointKey } from '../core/resourceOperation';
import type { DonationKey } from './types';

export function OwnerUseAddition(props: {
  accountId: string;
  donationId: string;
  donationRevision: string;
  donationKey: DonationKey;
}) {
  const { t } = useTranslation();
  return props.donationKey.endpointId && props.donationKey.endpointKeyId ? (
    <ResubmitKey
      {...props}
      endpointId={props.donationKey.endpointId}
      keyId={props.donationKey.endpointKeyId}
    />
  ) : (
    <Note>{t('common.connections.useAdditionUnavailable')}</Note>
  );
}

function ResubmitKey({
  accountId,
  donationId,
  donationRevision,
  donationKey,
  endpointId,
  keyId,
}: {
  accountId: string;
  donationId: string;
  donationRevision: string;
  donationKey: DonationKey;
  endpointId: string;
  keyId: string;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const options = useEndpointCreateOptions(accountId);
  const services =
    options.data?.native_service_options?.find(
      (row) => row.connector_type === donationKey.source.connectorType,
    )?.native_services ?? [];
  const keyQuery = ['user', 'donation-use-grant', accountId, endpointId, keyId];
  const usesQuery = ['user', 'donation-use-options', accountId, donationId, donationKey.id];
  const key = useQuery({
    queryKey: keyQuery,
    queryFn: ({ signal }) => readEndpointKey(endpointId, keyId, signal),
    retry: false,
  });
  const uses = useQuery({
    queryKey: usesQuery,
    queryFn: ({ signal }) =>
      getDonationUses('owner', donationId, '1', 20, '', signal, donationKey.id),
    retry: false,
  });
  const [done, setDone] = useState(false);
  const refresh = () =>
    Promise.all([
      client.invalidateQueries({ queryKey: keyQuery }, { throwOnError: true }),
      client.invalidateQueries({ queryKey: usesQuery }, { throwOnError: true }),
      client.invalidateQueries(
        { queryKey: ['user', 'donation-uses', accountId, donationId] },
        { throwOnError: true },
      ),
      client.invalidateQueries(
        { queryKey: ['user', 'economy', 'donations'] },
        { throwOnError: true },
      ),
    ]);
  const operation = useRetainedOperation<AddDonationUsesInput, unknown>(
    (input, operationKey, context) =>
      addDonationUses(donationId, input, operationKey, context.signal),
    async (input, error, context) => {
      if (error && responseOutcomeUnknown(error) && context.operationKey)
        await addDonationUses(donationId, input, context.operationKey, context.signal);
      if (!error || responseOutcomeUnknown(error)) context.commit(() => setDone(true));
      await refresh();
      if (error && responseOutcomeUnknown(error)) return { operationConfirmed: true };
    },
    ['user'],
  );
  const locked =
    operation.isPending || operation.outcome === 'unknown' || Boolean(operation.refreshError);
  const currentUses = uses.data;
  const input =
    key.data && currentUses
      ? ({
          expected_revision: donationRevision,
          donation_key_id: donationKey.id,
          credential_id: key.data.credential_id!,
          credential_version: key.data.credential_version!,
          grant_revision: key.data.grant_revision!,
          items: services.map((service) => ({
            native_service: service,
            expected_revision:
              currentUses.data.find((row) => row.native_service === service)?.revision ?? null,
          })),
          discovery_source_id: null,
          discovery_source_revision: null,
        } satisfies AddDonationUsesInput)
      : null;
  const error = key.error ?? uses.error ?? options.error;
  return (
    <div className="nb-stack">
      {error ? (
        <ErrorState
          error={error}
          onRetry={() => {
            void key.refetch();
            void uses.refetch();
            void options.refetch();
          }}
        />
      ) : null}
      {key.isPending || uses.isPending || options.isPending ? <LoadingState /> : null}
      {done ? (
        <Note tone="ok">{t('common.connections.useAdditionSaved')}</Note>
      ) : (
        <>
          <p className="nb-small nb-muted">{t('common.connections.useAdditionEffect')}</p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={
              locked ||
              !input ||
              !services.length ||
              Boolean(error) ||
              key.isFetching ||
              uses.isFetching ||
              key.data?.credential_available === false
            }
            onClick={() => input && operation.mutate(input)}
          >
            {t('common.connections.submitServices')}
          </button>
        </>
      )}
      {operation.error ? <ErrorState error={operation.error} /> : null}
      {operation.outcome === 'unknown' ? (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          disabled={operation.isPending}
          onClick={() => void operation.check()}
        >
          {t('common.connections.checkResult')}
        </button>
      ) : null}
      {operation.refreshError ? (
        <ErrorState
          error={operation.refreshError}
          onRetry={operation.isSuccess ? () => void operation.refresh() : undefined}
        />
      ) : null}
    </div>
  );
}
