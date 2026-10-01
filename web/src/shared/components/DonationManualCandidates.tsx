import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { charityKeys, type CharityRole } from '@shared/operations/charity';
import {
  addDonationManualModels,
  getDonationKeyModels,
  removeDonationManualModel,
  type ManualCatalog,
} from '@shared/operations/donationKeyModels';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { useSearchState } from '@shared/operations/useSearchState';
import { PagePagination } from '@shared/operations/PagePagination';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { ErrorState, LoadingState } from './States';
import { useCharityModelScope } from './charityModelScopeContext';

interface Props {
  role: CharityRole;
  accountId: string;
  donationId: string;
  keyId: string;
  editable?: boolean;
  onCapabilityLoss?: () => void;
}
type Intent = { revision: string; entries: string[] } | { revision: string; remove: string };
const sourceCopy = {
  automatic: 'common.manualCandidates.source.automatic',
  manual: 'common.manualCandidates.source.manual',
  both: 'common.manualCandidates.source.both',
} as const;

export function DonationManualCandidates({
  role,
  accountId,
  donationId,
  keyId,
  editable = true,
  onCapabilityLoss,
}: Props) {
  const { t } = useTranslation();
  const scope = useCharityModelScope();
  const [params, setParams] = useSearchState();
  const prefix = `key_candidates_${keyId}`;
  const q = params.get(`${prefix}_q`) ?? '';
  const [searchDraft, setSearchDraft] = useState({ query: q, value: q });
  const search = searchDraft.query === q ? searchDraft.value : q;
  const [names, setNames] = useState('');
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'donation-candidates',
    scopeKey: `${accountId}:${donationId}:${keyId}:${scope ?? ''}`,
    pageParam: `${prefix}_page`,
    pageSizeParam: `${prefix}_page_size`,
  });
  const query = useQuery({
    queryKey: [
      ...charityKeys.root(role),
      'manual-candidates',
      accountId,
      donationId,
      keyId,
      scope,
      q,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getDonationKeyModels(role, donationId, keyId, pager.page, pager.pageSize, signal, scope, q),
    retry: false,
  });
  const mutation = useRetainedOperation<Intent, ManualCatalog>(
    (intent, operationKey, context) =>
      'remove' in intent
        ? removeDonationManualModel(
            role,
            donationId,
            keyId,
            intent.remove,
            intent.revision,
            operationKey,
            context.signal,
            scope,
          )
        : addDonationManualModels(
            role,
            donationId,
            keyId,
            intent.entries,
            intent.revision,
            operationKey,
            context.signal,
            scope,
          ),
    async (_intent, _error, context) => {
      context.assertCurrent();
      const result = await query.refetch();
      context.assertCurrent();
      if (result.error) throw result.error;
    },
    charityKeys.root(role),
  );
  const lost =
    isUnauthorized(query.error) ||
    isForbidden(query.error) ||
    isUnauthorized(mutation.error) ||
    isForbidden(mutation.error);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const entries = names
    .replaceAll('\r\n', '\n')
    .split('\n')
    .map((name) => name.trim())
    .filter(Boolean);
  const invalidNames =
    entries.length > 100 ||
    entries.some((name) => Array.from(name).length > 512 || /\p{Cc}/u.test(name));
  const revision = query.data?.manual_catalog_revision;
  const busy =
    mutation.isPending ||
    mutation.outcome === 'unknown' ||
    query.isFetching ||
    Boolean(query.error);
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <section className="ops-subcard ops-stack">
      <h4>{t('common.manualCandidates.title')}</h4>
      <p>{t('common.manualCandidates.help')}</p>
      <form
        className="ops-toolbar"
        onSubmit={(event) => {
          event.preventDefault();
          setParams((previous) => {
            const next = new URLSearchParams(previous);
            if (search.trim()) next.set(`${prefix}_q`, search.trim());
            else next.delete(`${prefix}_q`);
            next.delete(`${prefix}_page`);
            return next;
          });
        }}
      >
        <label>
          <span>{t('common.manualCandidates.search')}</span>
          <input
            value={search}
            maxLength={512}
            onChange={(event) => setSearchDraft({ query: q, value: event.target.value })}
          />
        </label>
        <button type="submit" className="btn btn-secondary" disabled={mutation.isPending}>
          {t('common.manualCandidates.searchAction')}
        </button>
      </form>
      {query.isPending ? <LoadingState /> : null}
      {query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : null}
      {query.data?.candidates ? (
        <>
          {query.data.candidates.data.length === 0 ? (
            <p>{t('common.manualCandidates.empty')}</p>
          ) : (
            <ul className="ops-stack">
              {query.data.candidates.data.map((candidate) => (
                <li key={candidate.upstream_model_id} className="ops-subcard">
                  <strong className="ops-break">{candidate.display_name}</strong>
                  <p>
                    {t(sourceCopy[candidate.source])} ·{' '}
                    {t(
                      candidate.verified
                        ? 'common.manualCandidates.discovered'
                        : 'common.manualCandidates.unverified',
                    )}
                  </p>
                  {editable && candidate.manual_entry_id && revision ? (
                    <button
                      type="button"
                      className="btn btn-quiet"
                      disabled={busy}
                      onClick={() =>
                        mutation.mutate({ remove: candidate.manual_entry_id!, revision })
                      }
                    >
                      {t('common.manualCandidates.remove', { name: candidate.display_name })}
                    </button>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
          <PagePagination
            metadata={query.data.candidates.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={busy}
          />
        </>
      ) : null}
      {editable ? (
        <fieldset className="ops-stack ops-unframed" disabled={busy || !revision}>
          <label>
            <span>{t('common.manualCandidates.names')}</span>
            <textarea
              rows={4}
              value={names}
              onChange={(event) => {
                if (busy) return;
                setNames(event.target.value);
                mutation.reset();
              }}
            />
          </label>
          <p>{t('common.manualCandidates.addHelp')}</p>
          {invalidNames ? (
            <p role="alert" className="field-error">
              {t('common.manualCandidates.invalidNames')}
            </p>
          ) : null}
          <button
            type="button"
            className="btn btn-primary"
            disabled={entries.length === 0 || invalidNames}
            onClick={() =>
              revision && mutation.mutate({ entries, revision }, { onSuccess: () => setNames('') })
            }
          >
            {t('common.manualCandidates.add')}
          </button>
        </fieldset>
      ) : (
        <p>{t('common.operations.charity.terminalKeyImmutable')}</p>
      )}
      {mutation.error ? <ErrorState error={mutation.error} /> : null}
      {mutation.outcome === 'conflict' ? <p>{t('common.manualCandidates.conflictHelp')}</p> : null}
      {mutation.outcome === 'unknown' && mutation.variables ? (
        <>
          <p>{t('common.donationReview.unknown')}</p>
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => mutation.variables && mutation.mutate(mutation.variables)}
          >
            {t('common.donationReview.retrySame')}
          </button>
        </>
      ) : null}
      {mutation.isSuccess ? <p role="status">{t('common.manualCandidates.saved')}</p> : null}
      {mutation.refreshError ? (
        <ErrorState error={mutation.refreshError} onRetry={() => void mutation.refresh()} />
      ) : null}
    </section>
  );
}
