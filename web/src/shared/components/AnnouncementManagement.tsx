import { managementRoot, type ManagementRole } from '@shared/operations/managedUsers';
import { useEffect, useState } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { useSearchState } from '@shared/operations/useSearchState';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import { TimeInput } from '@shared/components/TimeInput';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { createTimeDraft, timeDraftValue, type TimeDraft } from '@shared/time';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import {
  managedAnnouncementKeys,
  createAnnouncement,
  getAdminAnnouncementsPage,
  type AnnouncementSeverity,
  type AnnouncementState,
} from '@shared/operations/managedAnnouncements';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import '@shared/operations/operations.css';

interface AnnouncementDraft {
  title_zh: string;
  body_zh: string;
  title_en: string;
  body_en: string;
  severity: string;
  pinned: boolean;
  dismissible: boolean;
  expires: TimeDraft;
}

interface AnnouncementCreateInput {
  title_zh: string;
  body_zh: string;
  title_en: string;
  body_en: string;
  severity: string;
  pinned: boolean;
  dismissible: boolean;
  expires_at: number | null;
}

export function AnnouncementManagement({
  role,
  account,
  sessionError,
  onAuthorityLoss,
}: {
  role: ManagementRole;
  account: string | undefined;
  sessionError?: unknown;
  onAuthorityLoss?: () => void;
}) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const [params, setParams] = useSearchState();
  const client = useQueryClient();
  const state =
    params.getAll('state').length === 1 &&
    ['draft', 'published', 'withdrawn', 'expired'].includes(params.get('state') ?? '')
      ? params.get('state')!
      : '';
  const severity =
    params.getAll('severity').length === 1 &&
    ['info', 'warning', 'important'].includes(params.get('severity') ?? '')
      ? params.get('severity')!
      : '';
  const accountID = account;
  const root = managementRoot(role);
  const detailPath = (id: string) =>
    role === 'admin'
      ? `/announcements/${encodeURIComponent(id)}`
      : `/steward?tab=announcements&announcement=${encodeURIComponent(id)}`;
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'announcements',
    scopeKey: accountID ?? 'anonymous',
    scopeReady: Boolean(accountID) && !sessionError,
    resetKey: `${state}\u0000${severity}`,
  });
  const [creating, setCreating] = useState(false);
  const [draft, setDraft] = useState<AnnouncementDraft>(() => ({
    title_zh: '',
    body_zh: '',
    title_en: '',
    body_en: '',
    severity: 'info',
    pinned: false,
    dismissible: true,
    expires: createTimeDraft(null, 'minute'),
  }));
  const list = useQuery({
    queryKey: managedAnnouncementKeys.page(
      role,
      accountID ?? 'none',
      state,
      severity,
      pager.page,
      pager.pageSize,
    ),
    queryFn: ({ signal }) =>
      getAdminAnnouncementsPage(state, severity, pager.page, pager.pageSize, signal, role),
    enabled: Boolean(accountID) && !sessionError,
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === accountID ? previous : undefined,
    retry: false,
  });
  const create = useRetainedOperation(
    (input: AnnouncementCreateInput, key) => createAnnouncement(input, key, role),
    async () => {
      await list.refetch();
    },
    root,
  );
  const expiry = timeDraftValue(draft.expires);
  const zhBodyBytes = new TextEncoder().encode(draft.body_zh).byteLength;
  const enBodyBytes = new TextEncoder().encode(draft.body_en).byteLength;
  const pageData = list.data;
  const busy = list.isFetching;
  const returnParams = new URLSearchParams(location.search);
  returnParams.set('page', pageData?.pagination.page ?? pager.page);
  returnParams.set('page_size', String(pager.pageSize));
  const returnTo = `${role === 'admin' ? '/announcements' : '/steward'}?${returnParams.toString()}`;
  const setFilter = (name: 'state' | 'severity', value: string) => {
    setParams((current) => {
      const next = new URLSearchParams(current);
      next.delete(name);
      if (value) next.set(name, value);
      next.set('page', '1');
      next.set('page_size', String(pager.pageSize));
      return next;
    });
  };
  const languagePairsValid =
    Boolean(draft.title_zh.trim()) === Boolean(draft.body_zh.trim()) &&
    Boolean(draft.title_en.trim()) === Boolean(draft.body_en.trim());
  const draftValid =
    languagePairsValid && zhBodyBytes <= 65_536 && enBodyBytes <= 65_536 && expiry !== undefined;
  const submitCreate = () => {
    const expiresAt = timeDraftValue(draft.expires);
    if (expiresAt === undefined) return;
    create.mutate(
      {
        title_zh: draft.title_zh,
        body_zh: draft.body_zh,
        title_en: draft.title_en,
        body_en: draft.body_en,
        severity: draft.severity,
        pinned: draft.pinned,
        dismissible: draft.dismissible,
        expires_at: expiresAt,
      },
      {
        onSuccess: (receipt) => navigate(detailPath(receipt.id), { state: { returnTo } }),
      },
    );
  };
  const authorityError = list.error ?? create.error;
  useEffect(() => {
    if (isUnauthorized(authorityError) || isForbidden(authorityError)) {
      if (onAuthorityLoss) onAuthorityLoss();
      else clearStationSession(client, role);
    }
  }, [client, authorityError, role, onAuthorityLoss]);
  const stateLabels: Record<AnnouncementState, string> = {
    draft: t('management.announcements.state.draft'),
    published: t('management.announcements.state.published'),
    withdrawn: t('management.announcements.state.withdrawn'),
    expired: t('management.announcements.state.expired'),
  };
  const severityLabels: Record<AnnouncementSeverity, string> = {
    info: t('management.announcements.severity.info'),
    warning: t('management.announcements.severity.warning'),
    important: t('management.announcements.severity.important'),
  };
  return (
    <div className="page ops-page">
      <PageHeader
        title={t('management.announcements.title')}
        description={t('management.announcements.description')}
        actions={
          <button
            className="btn btn-primary"
            type="button"
            disabled={!accountID || Boolean(sessionError)}
            onClick={() => setCreating((value) => !value)}
          >
            {creating
              ? t('management.announcements.closeCreator')
              : t('management.announcements.createDraft')}
          </button>
        }
      />
      {creating ? (
        <Card>
          <h2>{t('management.announcements.newDraft')}</h2>
          <p>{t('management.announcements.draftHint')}</p>
          <div className="ops-field-grid">
            <label>
              <span>{t('management.announcements.chineseTitle')}</span>
              <input
                value={draft.title_zh}
                maxLength={160}
                onChange={(event) => setDraft({ ...draft, title_zh: event.target.value })}
              />
            </label>
            <label>
              <span>{t('management.announcements.englishTitle')}</span>
              <input
                value={draft.title_en}
                maxLength={160}
                onChange={(event) => setDraft({ ...draft, title_en: event.target.value })}
              />
            </label>
          </div>
          <div className="ops-field-grid">
            <label>
              <span>{t('management.announcements.chineseMarkdown', { bytes: zhBodyBytes })}</span>
              <textarea
                value={draft.body_zh}
                onChange={(event) => setDraft({ ...draft, body_zh: event.target.value })}
              />
            </label>
            <label>
              <span>{t('management.announcements.englishMarkdown', { bytes: enBodyBytes })}</span>
              <textarea
                value={draft.body_en}
                onChange={(event) => setDraft({ ...draft, body_en: event.target.value })}
              />
            </label>
          </div>
          {!draftValid ? (
            <p className="field-error" role="alert">
              {t('management.announcements.validation')}
            </p>
          ) : null}
          <div className="ops-field-grid">
            <label>
              <span>{t('management.announcements.severityLabel')}</span>
              <select
                value={draft.severity}
                onChange={(event) => setDraft({ ...draft, severity: event.target.value })}
              >
                <option value="info">{severityLabels.info}</option>
                <option value="warning">{severityLabels.warning}</option>
                <option value="important">{severityLabels.important}</option>
              </select>
            </label>
            <TimeInput
              station={role === 'admin' ? 'admin' : 'steward'}
              label={t('management.announcements.expiryOptional')}
              draft={draft.expires}
              onChange={(update) =>
                setDraft((current) => ({
                  ...current,
                  expires: update(current.expires),
                }))
              }
            />
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={draft.pinned}
                onChange={(event) => setDraft({ ...draft, pinned: event.target.checked })}
              />
              <span>{t('management.announcements.pinned')}</span>
            </label>
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={draft.dismissible}
                onChange={(event) => setDraft({ ...draft, dismissible: event.target.checked })}
              />
              <span>{t('management.announcements.dismissible')}</span>
            </label>
          </div>
          {create.error ? <ErrorState error={create.error} /> : null}
          <button
            className="btn btn-primary"
            type="button"
            disabled={create.isPending || !draftValid || !accountID || Boolean(sessionError)}
            onClick={submitCreate}
          >
            {t('management.announcements.createPrivateDraft')}
          </button>
        </Card>
      ) : null}
      <Card>
        <div className="ops-toolbar">
          <label>
            <span>{t('management.announcements.stateLabel')}</span>
            <select
              value={state}
              onChange={(event) => {
                setFilter('state', event.target.value);
              }}
            >
              <option value="">{t('management.announcements.all')}</option>
              <option value="draft">{stateLabels.draft}</option>
              <option value="published">{stateLabels.published}</option>
              <option value="withdrawn">{stateLabels.withdrawn}</option>
              <option value="expired">{stateLabels.expired}</option>
            </select>
          </label>
          <label>
            <span>{t('management.announcements.severityLabel')}</span>
            <select
              value={severity}
              onChange={(event) => {
                setFilter('severity', event.target.value);
              }}
            >
              <option value="">{t('management.announcements.all')}</option>
              <option value="info">{severityLabels.info}</option>
              <option value="warning">{severityLabels.warning}</option>
              <option value="important">{severityLabels.important}</option>
            </select>
          </label>
        </div>
        {sessionError ? (
          <ErrorState error={sessionError} />
        ) : list.isPending ? (
          <LoadingState />
        ) : list.error ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : pageData ? (
          <>
            {pageData.data.length === 0 ? (
              <EmptyState
                title={t('management.announcements.emptyTitle')}
                body={t('management.announcements.emptyBody')}
              />
            ) : (
              <div className="ops-table-scroll" aria-busy={busy}>
                <table className="ops-table ops-table--responsive">
                  <thead>
                    <tr>
                      <th>{t('management.announcements.table.state')}</th>
                      <th>{t('management.announcements.table.draftTitle')}</th>
                      <th>{t('management.announcements.table.severity')}</th>
                      <th>{t('management.announcements.table.publication')}</th>
                      <th>{t('management.announcements.table.updated')}</th>
                      <th>{t('management.announcements.table.open')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {pageData.data.map((item) => (
                      <tr key={item.id}>
                        <td data-label={t('management.announcements.table.state')}>
                          <StatusBadge
                            active={item.state === 'published'}
                            label={stateLabels[item.state]}
                          />
                        </td>
                        <td
                          className="ops-cell-wide"
                          data-label={t('management.announcements.table.draftTitle')}
                        >
                          {item.draft.en?.title ??
                            item.draft.zh?.title ??
                            t('management.announcements.emptyDraft')}
                        </td>
                        <td data-label={t('management.announcements.table.severity')}>
                          {severityLabels[item.severity]}
                          {item.pinned ? ` · ${t('management.announcements.pinned')}` : ''}
                        </td>
                        <td data-label={t('management.announcements.table.publication')}>
                          {item.published
                            ? t('management.announcements.publicationValue', {
                                revision: item.published.revision,
                                date: formatDateTime(item.published.published_at),
                              })
                            : t('management.announcements.neverPublished')}
                        </td>
                        <td data-label={t('management.announcements.table.updated')}>
                          {formatDateTime(item.updated_at)}
                        </td>
                        <td
                          className="ops-cell-wide"
                          data-label={t('management.announcements.table.open')}
                        >
                          <button
                            className="btn btn-secondary"
                            type="button"
                            onClick={() =>
                              navigate(detailPath(item.id), {
                                state: { returnTo },
                              })
                            }
                          >
                            {t('management.announcements.edit')}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <PagePagination
              metadata={pageData.pagination}
              requestedPage={pager.page}
              busy={busy}
              onPageChange={pager.setPage}
              onPageSizeChange={pager.setPageSize}
            />
          </>
        ) : (
          <LoadingState />
        )}
      </Card>
    </div>
  );
}
