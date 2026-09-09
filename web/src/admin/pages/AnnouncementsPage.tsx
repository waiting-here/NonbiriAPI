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
import { formatDateTime } from '@shared/utils/datetime';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { useAdminSession } from '../data';
import {
  adminAnnouncementKeys,
  createAnnouncement,
  getAdminAnnouncementsPage,
  type AnnouncementSeverity,
  type AnnouncementState,
} from '../features/operations/announcements';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
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

export function AnnouncementsPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const [params, setParams] = useSearchState();
  const client = useQueryClient();
  const session = useAdminSession();
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
  const accountID = session.data ? `admin:${session.data.admin.username}` : undefined;
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'announcements',
    scopeKey: accountID ?? 'anonymous',
    scopeReady: Boolean(accountID) && !session.error,
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
    queryKey: adminAnnouncementKeys.page(
      accountID ?? 'none',
      state,
      severity,
      pager.page,
      pager.pageSize,
    ),
    queryFn: ({ signal }) =>
      getAdminAnnouncementsPage(state, severity, pager.page, pager.pageSize, signal),
    enabled: Boolean(accountID) && !session.error,
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === accountID ? previous : undefined,
    retry: false,
  });
  const create = useRetainedOperation(
    (input: AnnouncementCreateInput, key) => createAnnouncement(input, key),
    async () => {
      await list.refetch();
    },
  );
  const expiry = timeDraftValue(draft.expires);
  const zhBodyBytes = new TextEncoder().encode(draft.body_zh).byteLength;
  const enBodyBytes = new TextEncoder().encode(draft.body_en).byteLength;
  const pageData = list.data;
  const busy = list.isFetching;
  const returnParams = new URLSearchParams(location.search);
  returnParams.set('page', pageData?.pagination.page ?? pager.page);
  returnParams.set('page_size', String(pager.pageSize));
  const returnTo = `/announcements?${returnParams.toString()}`;
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
        onSuccess: (receipt) =>
          navigate(`/announcements/${encodeURIComponent(receipt.id)}`, { state: { returnTo } }),
      },
    );
  };
  useEffect(() => {
    if (isUnauthorized(list.error) || isForbidden(list.error)) clearStationSession(client, 'admin');
  }, [client, list.error]);
  const stateLabels: Record<AnnouncementState, string> = {
    draft: t('admin.announcements.state.draft'),
    published: t('admin.announcements.state.published'),
    withdrawn: t('admin.announcements.state.withdrawn'),
    expired: t('admin.announcements.state.expired'),
  };
  const severityLabels: Record<AnnouncementSeverity, string> = {
    info: t('admin.announcements.severity.info'),
    warning: t('admin.announcements.severity.warning'),
    important: t('admin.announcements.severity.important'),
  };
  return (
    <div className="page ops-page">
      <PageHeader
        title={t('admin.announcements.title')}
        description={t('admin.announcements.description')}
        actions={
          <button
            className="btn btn-primary"
            type="button"
            onClick={() => setCreating((value) => !value)}
          >
            {creating
              ? t('admin.announcements.closeCreator')
              : t('admin.announcements.createDraft')}
          </button>
        }
      />
      {creating ? (
        <Card>
          <h2>{t('admin.announcements.newDraft')}</h2>
          <p>{t('admin.announcements.draftHint')}</p>
          <div className="ops-field-grid">
            <label>
              <span>{t('admin.announcements.chineseTitle')}</span>
              <input
                value={draft.title_zh}
                maxLength={160}
                onChange={(event) => setDraft({ ...draft, title_zh: event.target.value })}
              />
            </label>
            <label>
              <span>{t('admin.announcements.englishTitle')}</span>
              <input
                value={draft.title_en}
                maxLength={160}
                onChange={(event) => setDraft({ ...draft, title_en: event.target.value })}
              />
            </label>
          </div>
          <div className="ops-field-grid">
            <label>
              <span>{t('admin.announcements.chineseMarkdown', { bytes: zhBodyBytes })}</span>
              <textarea
                value={draft.body_zh}
                onChange={(event) => setDraft({ ...draft, body_zh: event.target.value })}
              />
            </label>
            <label>
              <span>{t('admin.announcements.englishMarkdown', { bytes: enBodyBytes })}</span>
              <textarea
                value={draft.body_en}
                onChange={(event) => setDraft({ ...draft, body_en: event.target.value })}
              />
            </label>
          </div>
          {!draftValid ? (
            <p className="field-error" role="alert">
              {t('admin.announcements.validation')}
            </p>
          ) : null}
          <div className="ops-field-grid">
            <label>
              <span>{t('admin.announcements.severityLabel')}</span>
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
              station="admin"
              label={t('admin.announcements.expiryOptional')}
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
              <span>{t('admin.announcements.pinned')}</span>
            </label>
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={draft.dismissible}
                onChange={(event) => setDraft({ ...draft, dismissible: event.target.checked })}
              />
              <span>{t('admin.announcements.dismissible')}</span>
            </label>
          </div>
          {create.error ? <ErrorState error={create.error} /> : null}
          <button
            className="btn btn-primary"
            type="button"
            disabled={create.isPending || !draftValid}
            onClick={submitCreate}
          >
            {t('admin.announcements.createPrivateDraft')}
          </button>
        </Card>
      ) : null}
      <Card>
        <div className="ops-toolbar">
          <label>
            <span>{t('admin.announcements.stateLabel')}</span>
            <select
              value={state}
              onChange={(event) => {
                setFilter('state', event.target.value);
              }}
            >
              <option value="">{t('admin.announcements.all')}</option>
              <option value="draft">{stateLabels.draft}</option>
              <option value="published">{stateLabels.published}</option>
              <option value="withdrawn">{stateLabels.withdrawn}</option>
              <option value="expired">{stateLabels.expired}</option>
            </select>
          </label>
          <label>
            <span>{t('admin.announcements.severityLabel')}</span>
            <select
              value={severity}
              onChange={(event) => {
                setFilter('severity', event.target.value);
              }}
            >
              <option value="">{t('admin.announcements.all')}</option>
              <option value="info">{severityLabels.info}</option>
              <option value="warning">{severityLabels.warning}</option>
              <option value="important">{severityLabels.important}</option>
            </select>
          </label>
        </div>
        {session.error ? (
          <ErrorState error={session.error} onRetry={() => void session.refetch()} />
        ) : list.isPending ? (
          <LoadingState />
        ) : list.error ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : pageData ? (
          <>
            {pageData.data.length === 0 ? (
              <EmptyState
                title={t('admin.announcements.emptyTitle')}
                body={t('admin.announcements.emptyBody')}
              />
            ) : (
              <div className="ops-table-scroll" aria-busy={busy}>
                <table className="ops-table ops-table--responsive">
                  <thead>
                    <tr>
                      <th>{t('admin.announcements.table.state')}</th>
                      <th>{t('admin.announcements.table.draftTitle')}</th>
                      <th>{t('admin.announcements.table.severity')}</th>
                      <th>{t('admin.announcements.table.publication')}</th>
                      <th>{t('admin.announcements.table.updated')}</th>
                      <th>{t('admin.announcements.table.open')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {pageData.data.map((item) => (
                      <tr key={item.id}>
                        <td data-label={t('admin.announcements.table.state')}>
                          <StatusBadge
                            active={item.state === 'published'}
                            label={stateLabels[item.state]}
                          />
                        </td>
                        <td
                          className="ops-cell-wide"
                          data-label={t('admin.announcements.table.draftTitle')}
                        >
                          {item.draft.en?.title ??
                            item.draft.zh?.title ??
                            t('admin.announcements.emptyDraft')}
                        </td>
                        <td data-label={t('admin.announcements.table.severity')}>
                          {severityLabels[item.severity]}
                          {item.pinned ? ` · ${t('admin.announcements.pinned')}` : ''}
                        </td>
                        <td data-label={t('admin.announcements.table.publication')}>
                          {item.published
                            ? t('admin.announcements.publicationValue', {
                                revision: item.published.revision,
                                date: formatDateTime(item.published.published_at),
                              })
                            : t('admin.announcements.neverPublished')}
                        </td>
                        <td data-label={t('admin.announcements.table.updated')}>
                          {formatDateTime(item.updated_at)}
                        </td>
                        <td
                          className="ops-cell-wide"
                          data-label={t('admin.announcements.table.open')}
                        >
                          <button
                            className="btn btn-secondary"
                            type="button"
                            onClick={() =>
                              navigate(`/announcements/${encodeURIComponent(item.id)}`, {
                                state: { returnTo },
                              })
                            }
                          >
                            {t('admin.announcements.edit')}
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
