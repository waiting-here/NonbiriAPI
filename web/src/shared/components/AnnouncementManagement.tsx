import { DraftLeaveGuard } from '@shared/components/charity/DraftLeaveScope';
import { useAnnouncementReturn } from '@shared/operations/useAnnouncementReturn';
import { DataTable } from '@shared/components/ui/DataTable';
import { Field } from '@shared/components/ui/Field';
import { Fields, FormActions } from '@shared/components/ui/Fields';
import { FilterBar } from '@shared/components/ui/FilterBar';
import { Button } from '@shared/components/ui/Button';
import { managementRoot, type ManagementRole } from '@shared/operations/managedUsers';
import { useEffect, useRef, useState } from 'react';
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
  SectionHead,
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
  embedded = false,
}: {
  role: ManagementRole;
  account: string | undefined;
  sessionError?: unknown;
  onAuthorityLoss?: () => void;
  /** Render a section heading when another page already owns the h1. */
  embedded?: boolean;
}) {
  const formatDateTime = useDateTimeFormatter();
  const { t, i18n } = useTranslation();
  const preferZh = Boolean(i18n.resolvedLanguage?.startsWith('zh'));
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
  const [titleSearch, setTitleSearch] = useState('');
  const [titleQuery, setTitleQuery] = useState('');
  const [reviewed, setReviewed] = useState(false);
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'announcements',
    scopeKey: accountID ?? 'anonymous',
    scopeReady: Boolean(accountID) && !sessionError,
    resetKey: `${state}\u0000${severity}\u0000${titleQuery}`,
  });
  const savedDestination = useRef<string | undefined>(undefined);
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
      titleQuery,
    ),
    queryFn: ({ signal }) =>
      getAdminAnnouncementsPage(
        state,
        severity,
        pager.page,
        pager.pageSize,
        signal,
        role,
        titleQuery,
      ),
    enabled: Boolean(accountID) && !sessionError,
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === accountID &&
      previousQuery?.queryKey[4] === state &&
      previousQuery?.queryKey[5] === severity &&
      (previousQuery?.queryKey[8] ?? '') === titleQuery
        ? previous
        : undefined,
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
  const { announcementSource, remember } = useAnnouncementReturn(Boolean(pageData) && !busy);
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
    Boolean(draft.title_zh.trim() || draft.title_en.trim()) &&
    languagePairsValid &&
    zhBodyBytes <= 65_536 &&
    enBodyBytes <= 65_536 &&
    expiry !== undefined;
  const submitCreate = () => {
    const expiresAt = timeDraftValue(draft.expires);
    if (expiresAt === undefined || !draftValid) return;
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
        onSuccess: (receipt) => {
          const destination = detailPath(receipt.id);
          savedDestination.current = destination;
          navigate(destination, { state: { returnTo } });
        },
      },
    );
  };
  const authorityError = list.error ?? create.error;
  useEffect(() => {
    if (isUnauthorized(authorityError) || isForbidden(authorityError)) {
      if (onAuthorityLoss) onAuthorityLoss();
      else if (isUnauthorized(authorityError)) clearStationSession(client, role);
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
  if (isUnauthorized(authorityError) || isForbidden(authorityError))
    return <ErrorState error={authorityError} />;
  const createAction = (
    <Button
      variant="primary"
      type="button"
      disabled={!accountID || Boolean(sessionError)}
      onClick={() => setCreating((value) => !value)}
    >
      {creating
        ? t('management.announcements.closeCreator')
        : t('management.announcements.createDraft')}
    </Button>
  );
  return (
    <div className="page ops-page">
      <DraftLeaveGuard
        dirty={Boolean(
          !create.isSuccess &&
          (draft.title_zh ||
            draft.title_en ||
            draft.body_zh ||
            draft.body_en ||
            draft.pinned ||
            !draft.dismissible ||
            draft.severity !== 'info' ||
            expiry !== null),
        )}
        allowNavigation={({ nextLocation }) =>
          nextLocation.pathname + nextLocation.search === savedDestination.current ||
          create.isSuccess ||
          (nextLocation.pathname === location.pathname && nextLocation.search === location.search)
        }
      />
      {embedded ? (
        <SectionHead
          title={t('management.announcements.title')}
          description={t('management.announcements.description')}
          actions={createAction}
        />
      ) : (
        <PageHeader
          title={t('management.announcements.title')}
          description={t('management.announcements.description')}
          actions={createAction}
        />
      )}
      {creating ? (
        <Card>
          <h2>{t('management.announcements.newDraft')}</h2>
          <p>{t('management.announcements.draftHint')}</p>
          <Fields>
            <Field label={t('management.announcements.chineseTitle')} size="medium">
              {(field) => (
                <input
                  {...field}
                  value={draft.title_zh}
                  onBlur={() => setReviewed(true)}
                  maxLength={160}
                  onChange={(event) => setDraft({ ...draft, title_zh: event.target.value })}
                />
              )}
            </Field>
            <Field label={t('management.announcements.englishTitle')} size="medium">
              {(field) => (
                <input
                  {...field}
                  value={draft.title_en}
                  onBlur={() => setReviewed(true)}
                  maxLength={160}
                  onChange={(event) => setDraft({ ...draft, title_en: event.target.value })}
                />
              )}
            </Field>
          </Fields>
          <Fields>
            <Field
              label={t('management.announcements.chineseMarkdown', { bytes: zhBodyBytes })}
              size="long"
            >
              {(field) => (
                <textarea
                  {...field}
                  rows={5}
                  value={draft.body_zh}
                  onBlur={() => setReviewed(true)}
                  onChange={(event) => setDraft({ ...draft, body_zh: event.target.value })}
                />
              )}
            </Field>
            <Field
              label={t('management.announcements.englishMarkdown', { bytes: enBodyBytes })}
              size="long"
            >
              {(field) => (
                <textarea
                  {...field}
                  rows={5}
                  value={draft.body_en}
                  onBlur={() => setReviewed(true)}
                  onChange={(event) => setDraft({ ...draft, body_en: event.target.value })}
                />
              )}
            </Field>
          </Fields>
          {reviewed && !draftValid ? (
            <p className="field-error" role="alert">
              {t('management.announcements.validation')}
            </p>
          ) : null}
          <Fields>
            <Field label={t('management.announcements.severityLabel')} size="short">
              {(field) => (
                <select
                  {...field}
                  value={draft.severity}
                  onChange={(event) => setDraft({ ...draft, severity: event.target.value })}
                >
                  <option value="info">{severityLabels.info}</option>
                  <option value="warning">{severityLabels.warning}</option>
                  <option value="important">{severityLabels.important}</option>
                </select>
              )}
            </Field>
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
            <Field label={t('management.announcements.pinned')} size="short">
              {(field) => (
                <input
                  {...field}
                  type="checkbox"
                  checked={draft.pinned}
                  onChange={(event) => setDraft({ ...draft, pinned: event.target.checked })}
                />
              )}
            </Field>
            <Field label={t('management.announcements.dismissible')} size="short">
              {(field) => (
                <input
                  {...field}
                  type="checkbox"
                  checked={draft.dismissible}
                  onChange={(event) => setDraft({ ...draft, dismissible: event.target.checked })}
                />
              )}
            </Field>
          </Fields>
          {create.error ? <ErrorState error={create.error} /> : null}
          <FormActions>
            <Button
              variant="primary"
              type="button"
              disabled={create.isPending || !draftValid || !accountID || Boolean(sessionError)}
              onClick={submitCreate}
            >
              {t('management.announcements.createPrivateDraft')}
            </Button>
          </FormActions>
        </Card>
      ) : null}
      <Card>
        <FilterBar
          query={list}
          activeCount={[state, severity].filter(Boolean).length}
          onSubmit={() => {
            setTitleQuery(titleSearch.trim());
            pager.setPage('1');
          }}
          search={
            <>
              <Field label={t('common.announcementTitleSearch')}>
                {(field) => (
                  <input
                    {...field}
                    type="search"
                    maxLength={160}
                    value={titleSearch}
                    onChange={(event) => setTitleSearch(event.target.value)}
                  />
                )}
              </Field>
              <Field label={t('management.announcements.stateLabel')} size="short">
                {(field) => (
                  <select
                    {...field}
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
                )}
              </Field>
              <Field label={t('management.announcements.severityLabel')} size="short">
                {(field) => (
                  <select
                    {...field}
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
                )}
              </Field>
              {state || severity ? (
                <Button
                  variant="ghost"
                  type="button"
                  onClick={() =>
                    setParams((previous) => {
                      const next = new URLSearchParams(previous);
                      next.delete('state');
                      next.delete('severity');
                      next.set('page', '1');
                      return next;
                    })
                  }
                >
                  {t('common.resetFilter')}
                </Button>
              ) : null}
            </>
          }
        />
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
              <div aria-busy={busy}>
                <DataTable
                  caption={t('management.announcements.title')}
                  rows={pageData.data}
                  rowKey={(item) => item.id}
                  columns={[
                    {
                      key: 'state',
                      header: t('management.announcements.table.state'),
                      cell: 'status',
                      render: (item) => (
                        <StatusBadge
                          active={item.state === 'published'}
                          label={stateLabels[item.state]}
                        />
                      ),
                    },
                    {
                      key: 'draftTitle',
                      header: t('management.announcements.table.draftTitle'),
                      cell: 'title',
                      render: (item) => {
                        const zhTitle = item.draft.zh?.title?.trim() || '';
                        const enTitle = item.draft.en?.title?.trim() || '';
                        const primary = preferZh ? zhTitle || enTitle : enTitle || zhTitle;
                        const secondary = preferZh
                          ? zhTitle && enTitle && zhTitle !== enTitle
                            ? enTitle
                            : ''
                          : enTitle && zhTitle && enTitle !== zhTitle
                            ? zhTitle
                            : '';
                        if (!primary) {
                          return (
                            <span className="nb-muted">
                              {t('management.announcements.emptyDraft')}
                            </span>
                          );
                        }
                        return secondary ? (
                          <div className="nb-stack nb-stack--xs">
                            <strong>{primary}</strong>
                            <span className="nb-small nb-muted">{secondary}</span>
                          </div>
                        ) : (
                          <strong>{primary}</strong>
                        );
                      },
                    },
                    {
                      key: 'severity',
                      header: t('management.announcements.table.severity'),
                      mobileLabel: t('management.announcements.table.severity'),
                      cell: 'meta',
                      render: (item) => (
                        <>
                          {severityLabels[item.severity]}
                          {item.pinned ? ` · ${t('management.announcements.pinned')}` : ''}
                        </>
                      ),
                    },
                    {
                      key: 'publication',
                      header: t('management.announcements.table.publication'),
                      mobileLabel: t('management.announcements.table.publication'),
                      cell: 'meta',
                      render: (item) =>
                        item.published
                          ? t('management.announcements.publicationValue', {
                              revision: item.published.revision,
                              date: formatDateTime(item.published.published_at),
                            })
                          : t('management.announcements.neverPublished'),
                    },
                    {
                      key: 'updated',
                      header: t('management.announcements.table.updated'),
                      mobileLabel: t('management.announcements.table.updated'),
                      cell: 'meta',
                      render: (item) => formatDateTime(item.updated_at),
                    },
                    {
                      key: 'open',
                      header: t('management.announcements.table.open'),
                      cell: 'action',
                      align: 'action',
                      render: (item) => (
                        <Button
                          variant="secondary"
                          size="sm"
                          type="button"
                          id={`edit-${item.id}`}
                          onClick={(event) => {
                            remember(event.currentTarget);
                            navigate(detailPath(item.id), {
                              state: { returnTo, announcementSource },
                            });
                          }}
                        >
                          {t('management.announcements.edit')}
                        </Button>
                      ),
                    },
                  ]}
                />
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
