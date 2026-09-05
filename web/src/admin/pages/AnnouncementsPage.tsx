import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { formatDateTime } from '@shared/utils/datetime';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import {
  adminAnnouncementKeys,
  createAnnouncement,
  getAdminAnnouncements,
  type AnnouncementSeverity,
  type AnnouncementState,
} from '../features/operations/announcements';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import '@shared/operations/operations.css';

function optionalUnix(value: string): number | null {
  if (!value) return null;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? Math.floor(parsed / 1_000) : -1;
}

export function AnnouncementsPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const client = useQueryClient();
  const pager = useCursorPager();
  const [state, setState] = useState('');
  const [severity, setSeverity] = useState('');
  const [creating, setCreating] = useState(false);
  const [draft, setDraft] = useState({
    title_zh: '',
    body_zh: '',
    title_en: '',
    body_en: '',
    severity: 'info',
    pinned: false,
    dismissible: true,
    expires: '',
  });
  const list = useQuery({
    queryKey: adminAnnouncementKeys.list(state, severity, pager.cursor),
    queryFn: () => getAdminAnnouncements(state, severity, pager.cursor),
    retry: false,
  });
  const create = useRetainedOperation(
    (input: typeof draft, key) =>
      createAnnouncement(
        {
          title_zh: input.title_zh,
          body_zh: input.body_zh,
          title_en: input.title_en,
          body_en: input.body_en,
          severity: input.severity,
          pinned: input.pinned,
          dismissible: input.dismissible,
          expires_at: optionalUnix(input.expires),
        },
        key,
      ),
    async () => {
      await list.refetch();
    },
  );
  const expiry = optionalUnix(draft.expires);
  const zhBodyBytes = new TextEncoder().encode(draft.body_zh).byteLength;
  const enBodyBytes = new TextEncoder().encode(draft.body_en).byteLength;
  const languagePairsValid =
    Boolean(draft.title_zh.trim()) === Boolean(draft.body_zh.trim()) &&
    Boolean(draft.title_en.trim()) === Boolean(draft.body_en.trim());
  const draftValid =
    languagePairsValid && zhBodyBytes <= 65_536 && enBodyBytes <= 65_536 && expiry !== -1;
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
            <label>
              <span>{t('admin.announcements.expiryOptional')}</span>
              <input
                type="datetime-local"
                value={draft.expires}
                onChange={(event) => setDraft({ ...draft, expires: event.target.value })}
              />
            </label>
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
            onClick={() =>
              create.mutate(draft, {
                onSuccess: (receipt) =>
                  navigate(`/announcements/${encodeURIComponent(receipt.id)}`),
              })
            }
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
                setState(event.target.value);
                pager.reset();
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
                setSeverity(event.target.value);
                pager.reset();
              }}
            >
              <option value="">{t('admin.announcements.all')}</option>
              <option value="info">{severityLabels.info}</option>
              <option value="warning">{severityLabels.warning}</option>
              <option value="important">{severityLabels.important}</option>
            </select>
          </label>
        </div>
        {list.isPending ? (
          <LoadingState />
        ) : list.error ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : list.data.data.length === 0 ? (
          <EmptyState
            title={t('admin.announcements.emptyTitle')}
            body={t('admin.announcements.emptyBody')}
          />
        ) : (
          <>
            <div className="ops-table-scroll">
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
                  {list.data.data.map((item) => (
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
                          onClick={() => navigate(`/announcements/${encodeURIComponent(item.id)}`)}
                        >
                          {t('admin.announcements.edit')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <CursorPagination
              page={pager.page}
              nextCursor={list.data.next_cursor}
              onPrevious={pager.previous}
              onNext={pager.next}
            />
          </>
        )}
      </Card>
    </div>
  );
}
