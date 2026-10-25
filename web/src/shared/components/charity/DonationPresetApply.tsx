import { useEffect, useState } from 'react';
import {
  hasStewardPermission,
  type StewardPermissions,
} from '@shared/operations/stewardPermissions';
import { StewardPermissionNotice } from '../StewardPermissionNotice';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  charityKeys,
  getManagedDonation,
  type AdminDonation,
  type CharityRole,
  type StewardDonation,
} from '@shared/operations/charity';
import {
  applyBindingPreset,
  getBindingPreset,
  getPresetChannels,
  type BindingMatrixReceipt,
  type PresetChannel,
  type PresetApplyInput,
} from '@shared/operations/charityBindingBatch';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { usePagePager } from '@shared/operations/usePagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import { formatCount } from '@shared/utils/formatNumber';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { EmptyState, ErrorState, LoadingState, StatusBadge } from '../States';
import { Button, DataTable, Field, FilterBar, Fold, Note, OutcomeNote } from '../ui';
import { BindingBatchResults, type BindingResultLabel } from './BindingBatchResults';
import { DraftLeaveGuard } from './DraftLeaveScope';

interface Submission {
  body: PresetApplyInput;
  labels: BindingResultLabel[];
  keyNames: Record<string, string>;
}
export function DonationPresetApply({
  role,
  accountId,
  item,
  busy,
  onCapabilityLoss,
}: {
  role: CharityRole;
  accountId: string;
  item: AdminDonation | StewardDonation;
  busy?: boolean;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const session = useQuery<{
    user?: { effective_level: number; steward_permissions?: StewardPermissions };
  }>({
    queryKey: ['user', 'session'],
    enabled: false,
  });
  const source = item.keys.find((key) => key.safe_source.kind === 'mainstream')?.safe_source;
  const custom = item.keys.some((key) => key.safe_source.kind === 'custom');
  if (item.status !== 'approved') return null;
  if (
    role === 'steward' &&
    (!hasStewardPermission(session.data?.user, 'charity.presets') ||
      !hasStewardPermission(session.data?.user, 'charity.manage'))
  )
    return <StewardPermissionNotice />;
  if (custom)
    return (
      <Fold key={`${accountId}:${item.id}`} title={t('common.bindingPresets.applyTitle')} lazy>
        <CustomPresetApply
          role={role}
          accountId={accountId}
          item={item}
          busy={busy}
          onCapabilityLoss={onCapabilityLoss}
        />
      </Fold>
    );
  if (source?.kind !== 'mainstream') return null;
  return (
    <ApplyBody
      key={`${accountId}:${item.id}:${source.channel_id}`}
      role={role}
      accountId={accountId}
      item={item}
      channelId={source.channel_id}
      name={source.name}
      busy={busy}
      onCapabilityLoss={onCapabilityLoss}
    />
  );
}

function CustomPresetApply(props: {
  role: CharityRole;
  accountId: string;
  item: AdminDonation | StewardDonation;
  busy?: boolean;
  onCapabilityLoss?: () => void;
}) {
  const { role, accountId, item, busy, onCapabilityLoss } = props;
  const { t } = useTranslation();
  const [search, setSearch] = useState('');
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState<PresetChannel | null>(null);
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'donation-preset-candidates',
    scopeKey: `${accountId}:${item.id}`,
    resetKey: query,
  });
  const list = useQuery({
    queryKey: [
      ...charityKeys.root(role),
      'preset-candidates',
      accountId,
      item.id,
      query,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getPresetChannels(role, query, 'active', pager.page, pager.pageSize, signal, item.id),
    enabled: !selected,
    retry: false,
  });
  const lost = isForbidden(list.error) || isUnauthorized(list.error);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  if (selected)
    return (
      <ApplyBody
        {...props}
        key={selected.channel_id}
        channelId={selected.channel_id}
        name={selected.name}
        onChoosePreset={() => setSelected(null)}
      />
    );
  return (
    <section className="nb-form" aria-label={t('common.bindingPresets.choosePreset')}>
      <h3>{t('common.bindingPresets.choosePreset')}</h3>
      <p className="nb-small nb-muted">{t('common.bindingPresets.customHelp')}</p>
      <FilterBar
        activeCount={query ? 1 : 0}
        secondaryLabel={t('common.filter')}
        onSubmit={() => {
          setQuery(search.trim());
          pager.reset();
        }}
        search={
          <>
            <Field label={t('common.bindingPresets.searchChannels')}>
              {(field) => (
                <input
                  {...field}
                  type="search"
                  placeholder={t('common.bindingPresets.candidateSearchPlaceholder')}
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                />
              )}
            </Field>
            <Button type="submit">{t('common.search')}</Button>
          </>
        }
      />
      {list.isPending ? (
        <LoadingState />
      ) : list.error ? (
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      ) : (
        <>
          {list.data.data.length ? (
            <DataTable
              caption={t('common.bindingPresets.availablePresets')}
              rows={list.data.data}
              rowKey={(row) => row.channel_id}
              columns={[
                {
                  key: 'name',
                  header: t('common.bindingPresets.channel'),
                  mobileLabel: t('common.bindingPresets.channel'),
                  cell: 'title',
                  render: (row) => (
                    <span className="nb-stack nb-stack--sm">
                      <strong>{row.name}</strong>
                      <small className="nb-workflow-id mono">{row.base_url}</small>
                      {row.recommended ? (
                        <StatusBadge active label={t('common.bindingPresets.recommended')} />
                      ) : null}
                    </span>
                  ),
                },
                {
                  key: 'count',
                  header: t('common.bindingPresets.mappings'),
                  mobileLabel: t('common.bindingPresets.mappings'),
                  cell: 'meta',
                  align: 'num',
                  render: (row) => formatCount(row.mapping_count).display,
                },
                {
                  key: 'action',
                  header: t('common.workflow.actions'),
                  mobileLabel: t('common.workflow.actions'),
                  cell: 'action',
                  render: (row) => (
                    <Button type="button" disabled={busy} onClick={() => setSelected(row)}>
                      {t('common.bindingPresets.selectPreset')}
                    </Button>
                  ),
                },
              ]}
            />
          ) : (
            <EmptyState
              title={t('common.bindingPresets.noCandidates')}
              body={t('common.bindingPresets.noCandidatesHelp')}
            />
          )}
          <PagePagination
            metadata={list.data.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={list.isFetching}
          />
        </>
      )}
    </section>
  );
}

function ApplyBody({
  role,
  accountId,
  item,
  channelId,
  name,
  busy,
  onCapabilityLoss,
  onChoosePreset,
}: {
  role: CharityRole;
  accountId: string;
  item: AdminDonation | StewardDonation;
  channelId: string;
  name: string;
  busy?: boolean;
  onCapabilityLoss?: () => void;
  onChoosePreset?: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [revision, setRevision] = useState(item.revision);
  const [readError, setReadError] = useState<unknown>(null);
  const [reading, setReading] = useState(false);
  const preset = useQuery({
    queryKey: [...charityKeys.root(role), 'binding-preset', accountId, channelId],
    queryFn: ({ signal }) => getBindingPreset(role, channelId, signal),
    retry: false,
  });
  const apply = useRetainedOperation<Submission, BindingMatrixReceipt>(
    (input, key, context) =>
      applyBindingPreset(role, item.id, input.body, input.labels.length, key, context.signal),
    () => client.invalidateQueries({ queryKey: charityKeys.root(role) }, { throwOnError: true }),
    charityKeys.root(role),
  );
  const lost = [preset.error, apply.error, readError].some(
    (error) => isForbidden(error) || isUnauthorized(error),
  );
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const locked =
    busy ||
    reading ||
    preset.isFetching ||
    apply.isPending ||
    apply.outcome === 'unknown' ||
    apply.outcome === 'refresh-failed';
  const reread = async () => {
    setReading(true);
    setReadError(null);
    try {
      const donation = await getManagedDonation(role, item.id);
      setRevision(donation.revision);
      await preset.refetch({ throwOnError: true });
      apply.reset();
    } catch (error) {
      setReadError(error);
    } finally {
      setReading(false);
    }
  };
  const submit = () => {
    if (!preset.data) return;
    const modelRevisions = new Map(
      preset.data.mappings
        .filter((mapping) => mapping.binding_revision !== null)
        .map((mapping) => [mapping.charity_model_id, mapping.binding_revision!]),
    );
    apply.mutate({
      body: {
        channel_id: channelId,
        expected_channel_revision: preset.data.channel_revision,
        expected_preset_revision: preset.data.preset_revision,
        expected_donation_revision: revision,
        models: [...modelRevisions].map(([charity_model_id, expected_binding_revision]) => ({
          charity_model_id,
          expected_binding_revision,
        })),
      },
      labels: preset.data.mappings.map((mapping) => ({
        full_name: mapping.full_name ?? t('common.bindingPresets.deletedTarget'),
        upstream_model_id: mapping.upstream_model_id,
      })),
      keyNames: Object.fromEntries(item.keys.map((key) => [key.id, key.safe_note])),
    });
  };
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <section className="nb-form" aria-label={t('common.bindingPresets.applyTitle')}>
      <DraftLeaveGuard
        dirty={apply.isPending || apply.outcome === 'unknown'}
        allowNavigation={({ currentLocation, nextLocation }) =>
          currentLocation.pathname === nextLocation.pathname &&
          new URLSearchParams(nextLocation.search).get('donation_id') === item.id
        }
      />
      <h3>{t('common.bindingPresets.applyTitle')}</h3>
      {onChoosePreset ? (
        <Button type="button" disabled={locked} onClick={onChoosePreset}>
          {t('common.bindingPresets.changePreset')}
        </Button>
      ) : null}
      {preset.isPending ? (
        <LoadingState />
      ) : preset.error ? (
        <ErrorState error={preset.error} onRetry={() => void preset.refetch()} />
      ) : (
        <>
          <p>
            {t('common.bindingPresets.applyHelp', { name, count: preset.data.mappings.length })}
          </p>
          <Button
            type="button"
            variant="primary"
            disabled={locked || preset.data.mappings.length === 0 || apply.outcome === 'conflict'}
            onClick={submit}
          >
            {t('common.bindingPresets.apply')}
          </Button>
          {preset.data.mappings.length === 0 ? (
            <Note>{t('common.bindingPresets.noPreset')}</Note>
          ) : null}
        </>
      )}
      {apply.error && apply.outcome !== 'conflict' ? <ErrorState error={apply.error} /> : null}
      {readError ? <ErrorState error={readError} onRetry={() => void reread()} /> : null}
      <OutcomeNote
        outcome={
          apply.outcome === 'conflict'
            ? { kind: 'conflict', reload: () => void reread() }
            : apply.outcome === 'unknown'
              ? { kind: 'unknown' }
              : apply.outcome === 'refresh-failed'
                ? { kind: 'savedRefreshFailed', recheck: () => void apply.refresh() }
                : { kind: 'idle' }
        }
        busy={locked}
      />
      {apply.outcome === 'unknown' && apply.variables ? (
        <Button
          type="button"
          disabled={apply.isPending}
          onClick={() => apply.mutate(apply.variables!)}
        >
          {t('common.donationReview.retrySame')}
        </Button>
      ) : null}
      {apply.data && apply.variables ? (
        <BindingBatchResults
          role={role}
          receipt={apply.data}
          labels={apply.variables.labels}
          keyNames={apply.variables.keyNames}
          scopeKey={`${accountId}:${item.id}:preset`}
        />
      ) : null}
    </section>
  );
}
