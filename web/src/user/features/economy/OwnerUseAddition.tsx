import { useState } from 'react';
import { Link } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Note } from '@shared/components/ui';
import { ErrorState, LoadingState } from '@shared/components/States';
import { DonationUsesPanel } from '@shared/components/DonationUsesPanel';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import {
  addDonationUses,
  getDonationUses,
  type AddDonationUsesInput,
  type DonationUse,
} from '@shared/operations/donationUses';
import type { NativeService } from '@shared/nativeServices';
import type { EndpointKey } from '../core/types';
import { readEndpointKey } from '../core/resourceOperation';
import type { DonationKey } from './types';
import { DonationShareFields, type DonationShare } from './DonationShareFields';

type Props = {
  accountId: string;
  donationId: string;
  donationRevision: string;
  donationKey: DonationKey;
};
export function OwnerUseAddition(props: Props) {
  const { t } = useTranslation();
  const { donationKey } = props;
  if (!donationKey.endpointId || !donationKey.endpointKeyId)
    return <Note>{t('common.connections.useAdditionUnavailable')}</Note>;
  return (
    <AdditionLoader
      {...props}
      endpointId={donationKey.endpointId}
      keyId={donationKey.endpointKeyId}
    />
  );
}
function AdditionLoader(props: Props & { endpointId: string; keyId: string }) {
  const { t } = useTranslation();
  const key = useQuery({
    queryKey: ['user', 'donation-use-grant', props.accountId, props.endpointId, props.keyId],
    queryFn: ({ signal }) => readEndpointKey(props.endpointId, props.keyId, signal),
    retry: false,
  });
  const uses = useQuery({
    queryKey: [
      'user',
      'donation-use-options',
      props.accountId,
      props.donationId,
      props.donationKey.id,
    ],
    queryFn: ({ signal }) =>
      getDonationUses('owner', props.donationId, '1', 20, '', signal, props.donationKey.id),
    retry: false,
  });
  const retry = () => {
    void key.refetch();
    void uses.refetch();
  };
  return (
    <div className="nb-stack">
      <Note>{t('common.connections.useAdditionEffect')}</Note>
      {key.error || uses.error ? (
        <ErrorState error={key.error ?? uses.error} onRetry={retry} />
      ) : null}
      {key.data && uses.data ? (
        <AdditionDraft
          {...props}
          current={key.data}
          uses={uses.data.data}
          readBlocked={Boolean(key.error || uses.error || key.isFetching || uses.isFetching)}
        />
      ) : key.isPending || uses.isPending ? (
        <LoadingState />
      ) : null}
    </div>
  );
}
function AdditionDraft({
  accountId,
  donationId,
  donationRevision,
  donationKey,
  endpointId,
  current,
  uses,
  readBlocked,
}: Props & {
  endpointId: string;
  current: EndpointKey;
  uses: DonationUse[];
  readBlocked: boolean;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const byService = new Map(uses.map((row) => [row.native_service, row]));
  const available = (current.native_services ?? []).filter(
    (service) => !byService.get(service)?.pending && !byService.get(service)?.approved,
  );
  const mainstream = donationKey.source.kind === 'mainstream';
  const [snapshot, setSnapshot] = useState(current);
  const [expected, setExpected] = useState(donationRevision);
  const [share, setShare] = useState<DonationShare>(() => ({
    services: mainstream ? available.slice() : [],
  }));
  const [versions, setVersions] = useState<Partial<Record<NativeService, string | null>>>(() =>
    Object.fromEntries(
      (mainstream ? available : []).map((service) => [
        service,
        byService.get(service)?.revision ?? null,
      ]),
    ),
  );
  const [done, setDone] = useState(false);
  const snapshotChanged =
    snapshot.credential_id !== current.credential_id ||
    snapshot.credential_version !== current.credential_version ||
    snapshot.grant_revision !== current.grant_revision ||
    expected !== donationRevision ||
    share.services.some(
      (service) =>
        (versions[service] ?? null) !== (byService.get(service)?.revision ?? null) ||
        !available.includes(service),
    );
  const operation = useRetainedOperation<AddDonationUsesInput, unknown>(
    (input, key, context) => addDonationUses(donationId, input, key, context.signal),
    async (input, error, context) => {
      let confirmed = !error;
      if (error && responseOutcomeUnknown(error) && context.operationKey) {
        await addDonationUses(donationId, input, context.operationKey, context.signal);
        confirmed = true;
      }
      await client.invalidateQueries({ queryKey: ['user'] });
      if (confirmed) {
        context.commit(() => setDone(true));
        if (error) return { operationConfirmed: true };
      }
    },
    ['user'],
  );
  const locked = operation.isPending || operation.outcome === 'unknown';
  const unavailable =
    !current.enabled ||
    current.credential_available === false ||
    current.suspension_state !== 'none' ||
    !donationKey.physicalEnabled;
  const ready =
    operation.outcome !== 'conflict' &&
    share.services.length > 0 &&
    !snapshotChanged &&
    !unavailable &&
    !readBlocked &&
    Boolean(snapshot.credential_id && snapshot.credential_version && snapshot.grant_revision);
  const adopt = () => {
    const selected = share.services.filter((service) => available.includes(service));
    setSnapshot(current);
    setExpected(donationRevision);
    setShare({ services: selected });
    setVersions(
      Object.fromEntries(
        selected.map((service) => [service, byService.get(service)?.revision ?? null]),
      ),
    );
    operation.reset();
  };

  if (done)
    return (
      <div className="nb-stack">
        <Note tone="ok">{t('common.connections.useAdditionSaved')}</Note>
        <DonationUsesPanel
          role="owner"
          accountId={accountId}
          donationId={donationId}
          donationKeyId={donationKey.id}
        />
        {available.length ? (
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => {
              adopt();
              setDone(false);
              setShare({
                services: mainstream ? available.slice() : [],
              });
              setVersions(
                Object.fromEntries(
                  (mainstream ? available : []).map((service) => [
                    service,
                    byService.get(service)?.revision ?? null,
                  ]),
                ),
              );
            }}
          >
            {t('common.connections.addServices')}
          </button>
        ) : null}
      </div>
    );
  return (
    <form
      className="nb-form"
      onSubmit={(event) => {
        event.preventDefault();
        if (!ready) return;
        void operation
          .mutateAsync({
            expected_revision: expected,
            donation_key_id: donationKey.id,
            credential_id: snapshot.credential_id!,
            credential_version: snapshot.credential_version!,
            grant_revision: snapshot.grant_revision!,
            items: share.services.map((native_service) => ({
              native_service,
              expected_revision: versions[native_service] ?? null,
            })),
            discovery_source_id: null,
            discovery_source_revision: null,
          })
          .catch(() => undefined);
      }}
    >
      {unavailable ? (
        <Note tone="warn">
          {t('common.connections.useAdditionUnavailable')}{' '}
          <Link to={'/endpoints/' + encodeURIComponent(endpointId)}>
            {t('common.connections.manageEntry')}
          </Link>
        </Note>
      ) : null}
      {!available.length ? <Note>{t('common.connections.noServicesToAdd')}</Note> : null}
      <DonationShareFields
        choice={{
          key: {
            nativeServices: available,
            credentialVersion: snapshot.credential_version,
          },
        }}
        value={share}
        disabled={locked || readBlocked || snapshotChanged || unavailable}
        onChange={(value) => {
          setShare(value);
          setVersions((previous) =>
            Object.fromEntries(
              value.services.map((service) => [
                service,
                Object.hasOwn(previous, service)
                  ? previous[service]
                  : (byService.get(service)?.revision ?? null),
              ]),
            ),
          );
        }}
      />
      {operation.error ? <ErrorState error={operation.error} /> : null}
      {snapshotChanged || operation.outcome === 'conflict' ? (
        <Note tone="warn">
          <p>{t('common.connections.donationSnapshotChanged')}</p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={locked || readBlocked}
            onClick={adopt}
          >
            {t('common.connections.useCurrentRevision')}
          </button>
        </Note>
      ) : null}
      {operation.outcome === 'unknown' ? (
        <Note tone="warn">
          <p>{t('common.connections.unknownResult')}</p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={operation.isPending}
            onClick={() => void operation.check().catch(() => undefined)}
          >
            {t('common.connections.checkResult')}
          </button>
        </Note>
      ) : null}
      <button type="submit" className="nb-btn nb-btn--primary" disabled={locked || !ready}>
        {t('common.connections.submitServices')}
      </button>
    </form>
  );
}
