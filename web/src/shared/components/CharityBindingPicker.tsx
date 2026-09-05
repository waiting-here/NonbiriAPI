import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import {
  charityKeys,
  getManagedBindingCandidates,
  getBindingDonations,
  getBindingSourceKeys,
  type CharityBindingCandidate,
  type CharityRole,
} from '@shared/operations/charity';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { EmptyState, ErrorState, LoadingState } from './States';

export type CharitySelection = CharityBindingCandidate & { note: string };
const charitySelectionKey = (entry: CharityBindingCandidate) =>
  `${entry.donation_key_id}:${entry.upstream_model_id}`;
const sourceTypeKey = (source: 'automatic' | 'manual') =>
  `common.operations.charity.sourceType.${source}`;

export function CharityBindingPicker({
  role,
  modelId,
  selected,
  onChange,
  locked,
  onCapabilityLoss,
}: {
  role: CharityRole;
  modelId: string;
  selected: Record<string, CharitySelection>;
  onChange: (next: Record<string, CharitySelection>) => void;
  locked: boolean;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const donationPager = useCursorPager();
  const keyPager = useCursorPager();
  const modelPager = useCursorPager();
  const [donationId, setDonationId] = useState('');
  const [description, setDescription] = useState('');
  const [keyId, setKeyId] = useState('');
  const [queryDraft, setQueryDraft] = useState('');
  const [query, setQuery] = useState('');
  const donations = useQuery({
    queryKey: charityKeys.bindingDonations(role, modelId, donationPager.cursor),
    queryFn: () => getBindingDonations(role, modelId, donationPager.cursor),
    retry: false,
  });
  const donation = useQuery({
    queryKey: charityKeys.bindingSourceKeys(role, modelId, donationId, keyPager.cursor),
    queryFn: () => getBindingSourceKeys(role, modelId, donationId, keyPager.cursor),
    enabled: Boolean(donationId),
    retry: false,
  });
  const filters = { donation_id: donationId, donation_key_id: keyId };
  const candidates = useQuery({
    queryKey: charityKeys.candidates(role, modelId, query, modelPager.cursor, filters),
    queryFn: () => getManagedBindingCandidates(role, modelId, query, modelPager.cursor, filters),
    enabled: Boolean(donationId && keyId),
    retry: false,
  });
  const selectedKey = donation.data?.data.find((entry) => entry.donation_key_id === keyId);
  const accessLost = [donations.error, donation.error, candidates.error].some(
    (error) => isUnauthorized(error) || isForbidden(error),
  );
  useEffect(() => {
    if (accessLost) onCapabilityLoss?.();
  }, [accessLost, onCapabilityLoss]);
  const chooseKey = (id: string) => {
    setKeyId(id);
    setQuery('');
    setQueryDraft('');
    modelPager.reset();
  };
  const chooseDonation = (id: string) => {
    setDonationId(id);
    setDescription(donations.data?.data.find((entry) => entry.id === id)?.description ?? '');
    keyPager.reset();
    chooseKey('');
  };
  if (accessLost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <div className="ops-binding-picker">
      <nav className="ops-actions" aria-label={t('common.operations.charity.bindingCandidates')}>
        <button
          type="button"
          className="btn btn-quiet"
          aria-current={!donationId ? 'step' : undefined}
          onClick={() => chooseDonation('')}
        >
          {t('common.operations.charity.chooseDonation')}
        </button>
        {donationId ? (
          <>
            <span aria-hidden="true">/</span>
            <button
              type="button"
              className="btn btn-quiet"
              aria-current={!keyId ? 'step' : undefined}
              onClick={() => chooseKey('')}
            >
              {t('common.operations.charity.chooseKey')}
            </button>
          </>
        ) : null}
        {keyId ? (
          <>
            <span aria-hidden="true">/</span>
            <span aria-current="step">{t('common.operations.charity.chooseModels')}</span>
          </>
        ) : null}
      </nav>
      {!donationId ? (
        <>
          {donations.isPending ? (
            <LoadingState />
          ) : donations.error ? (
            <ErrorState error={donations.error} onRetry={() => void donations.refetch()} />
          ) : (
            <>
              {donations.data.data.length === 0 ? (
                <EmptyState
                  title={t('common.operations.charity.noApprovedDonations')}
                  body={t('common.operations.charity.noApprovedDonationsBody')}
                />
              ) : (
                <div className="ops-picker-grid">
                  {donations.data.data.map((entry) => (
                    <button
                      type="button"
                      className="ops-picker-choice"
                      key={entry.id}
                      onClick={() => chooseDonation(entry.id)}
                    >
                      <strong>
                        {t('common.operations.charity.donationNumber', { id: entry.id })}
                      </strong>
                      <span>
                        {entry.description || t('common.operations.charity.noDescription')}
                      </span>
                      <small>
                        {t('common.operations.charity.keyCount', { count: entry.key_count })}
                      </small>
                    </button>
                  ))}
                </div>
              )}
              <CursorPagination
                page={donationPager.page}
                nextCursor={donations.data.next_cursor}
                onPrevious={donationPager.previous}
                onNext={donationPager.next}
              />
            </>
          )}
        </>
      ) : donation.isPending ? (
        <LoadingState />
      ) : donation.error ? (
        <ErrorState error={donation.error} onRetry={() => void donation.refetch()} />
      ) : (
        <>
          <div className="ops-picker-context">
            <strong>{t('common.operations.charity.donationNumber', { id: donationId })}</strong>
            <p>{description || t('common.operations.charity.noDescription')}</p>
          </div>
          {!keyId ? (
            <>
              {donation.data.data.length === 0 ? (
                <EmptyState
                  title={t('common.operations.charity.noCandidates')}
                  body={t('common.operations.charity.noCandidatesBody')}
                />
              ) : (
                <div className="ops-picker-grid">
                  {donation.data.data.map((entry) => (
                    <button
                      key={entry.donation_key_id}
                      type="button"
                      className="ops-picker-choice"
                      onClick={() => chooseKey(entry.donation_key_id)}
                    >
                      <strong>
                        {entry.note || `${entry.source.display_head}…${entry.source.display_tail}`}
                      </strong>
                      <code>
                        {entry.source.display_head}…{entry.source.display_tail}
                      </code>
                      <span>{entry.source.canonical_base_url}</span>
                    </button>
                  ))}
                </div>
              )}
              <CursorPagination
                page={keyPager.page}
                nextCursor={donation.data.next_cursor}
                onPrevious={keyPager.previous}
                onNext={keyPager.next}
              />
            </>
          ) : (
            <>
              <p className="ops-picker-context">
                {selectedKey?.note} · {selectedKey?.source.display_head}…
                {selectedKey?.source.display_tail} · {selectedKey?.source.canonical_base_url}
              </p>
              <form
                className="ops-toolbar"
                onSubmit={(event) => {
                  event.preventDefault();
                  modelPager.reset();
                  setQuery(queryDraft.trim());
                }}
              >
                <label>
                  <span>{t('common.operations.charity.searchModels')}</span>
                  <input
                    value={queryDraft}
                    maxLength={256}
                    onChange={(event) => setQueryDraft(event.target.value)}
                  />
                </label>
                <button type="submit" className="btn btn-secondary">
                  {t('common.search')}
                </button>
              </form>
              {candidates.isPending ? (
                <LoadingState />
              ) : candidates.error ? (
                <ErrorState error={candidates.error} onRetry={() => void candidates.refetch()} />
              ) : (
                <>
                  {candidates.data.data.length === 0 ? (
                    <EmptyState
                      title={t('common.operations.charity.noCandidates')}
                      body={t('common.operations.charity.noCandidatesBody')}
                    />
                  ) : (
                    <div className="ops-picker-grid">
                      {candidates.data.data.map((entry) => {
                        const key = charitySelectionKey(entry);
                        return (
                          <label key={key} className="ops-picker-choice checkbox-label">
                            <input
                              type="checkbox"
                              disabled={
                                locked || (!selected[key] && Object.keys(selected).length >= 100)
                              }
                              checked={Boolean(selected[key])}
                              onChange={(event) => {
                                const next = { ...selected };
                                if (event.target.checked)
                                  next[key] = { ...entry, note: selectedKey?.note ?? '' };
                                else delete next[key];
                                onChange(next);
                              }}
                            />
                            <span>
                              {entry.upstream_model_id}
                              <small className="ops-picker-source-types">
                                {entry.source_types
                                  .map((source) => t(sourceTypeKey(source)))
                                  .join(' / ')}
                              </small>
                            </span>
                          </label>
                        );
                      })}
                    </div>
                  )}
                  <CursorPagination
                    page={modelPager.page}
                    nextCursor={candidates.data.next_cursor}
                    onPrevious={modelPager.previous}
                    onNext={modelPager.next}
                  />
                </>
              )}
            </>
          )}
        </>
      )}
      <section
        className="ops-picker-selection"
        aria-label={t('common.operations.charity.selectedModels')}
      >
        <h4>
          {t('common.operations.charity.selectedCount', { count: Object.keys(selected).length })}
        </h4>
        <p>{t('common.operations.charity.crossSelectionHelp')}</p>
        <ul>
          {Object.entries(selected).map(([key, entry]) => (
            <li key={key}>
              <div>
                <strong>{entry.upstream_model_id}</strong>
                <span>
                  {t('common.operations.charity.donationNumber', { id: entry.donation_id })} ·{' '}
                  {entry.note} · {entry.source.display_head}…{entry.source.display_tail}
                </span>
              </div>
              <button
                type="button"
                className="btn btn-quiet"
                disabled={locked}
                onClick={() => {
                  const next = { ...selected };
                  delete next[key];
                  onChange(next);
                }}
              >
                {t('common.operations.charity.remove')}
              </button>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
