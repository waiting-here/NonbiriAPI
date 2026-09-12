import { managementRoot, type ManagementRole } from '@shared/operations/managedUsers';
import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { TimeInput } from '@shared/components/TimeInput';
import { Card, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { createTimeDraft, timeDraftValue, type TimeDraft } from '@shared/time';
import { formatDateTime } from '@shared/utils/datetime';
import { SafeAnnouncementBody } from '../../user/features/operations/SafeAnnouncementBody';
import {
  managedAnnouncementKeys,
  deleteAnnouncement,
  editAnnouncement,
  getAdminAnnouncement,
  previewAnnouncement,
  publishAnnouncement,
  withdrawAnnouncement,
  type AdminAnnouncement,
  type AnnouncementSeverity,
  type AnnouncementState,
} from '@shared/operations/managedAnnouncements';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import '@shared/operations/operations.css';

interface DraftState {
  title_zh: string;
  body_zh: string;
  title_en: string;
  body_en: string;
  severity: 'info' | 'warning' | 'important';
  pinned: boolean;
  dismissible: boolean;
  expires_at: TimeDraft;
}

interface AnnouncementSaveInput {
  revision: string;
  title_zh: string;
  body_zh: string;
  title_en: string;
  body_en: string;
  severity: DraftState['severity'];
  pinned: boolean;
  dismissible: boolean;
  expires_at: number | null;
}

const fromAuthority = (item: AdminAnnouncement): DraftState => ({
  title_zh: item.draft.zh?.title ?? '',
  body_zh: item.draft.zh?.body ?? '',
  title_en: item.draft.en?.title ?? '',
  body_en: item.draft.en?.body ?? '',
  severity: item.severity,
  pinned: item.pinned,
  dismissible: item.dismissible,
  expires_at: createTimeDraft(item.expires_at, 'minute'),
});

export function AnnouncementEditor({
  role,
  account,
  announcementId,
  backTo,
  onAuthorityLoss,
}: {
  role: ManagementRole;
  account: string;
  announcementId: string;
  backTo: string;
  onAuthorityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const root = managementRoot(role);
  const navigate = useNavigate();
  const client = useQueryClient();
  const authority = useQuery({
    queryKey: managedAnnouncementKeys.detail(role, account, announcementId),
    queryFn: () => getAdminAnnouncement(announcementId, role),
    retry: false,
    enabled: Boolean(announcementId && account),
  });
  const [draft, setDraft] = useState<DraftState | null>(null);
  const [loadedRevision, setLoadedRevision] = useState('');
  const [dirty, setDirty] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [reason, setReason] = useState('');
  const [confirmation, setConfirmation] = useState<'publish' | 'withdraw' | 'delete' | null>(null);
  const preview = useMutation({
    retry: false,
    mutationFn: (input: DraftState) =>
      previewAnnouncement(
        announcementId,
        {
          expected_revision: authority.data?.revision,
          title_zh: input.title_zh,
          body_zh: input.body_zh,
          title_en: input.title_en,
          body_en: input.body_en,
        },
        role,
      ),
  });
  const save = useRetainedOperation(
    (input: AnnouncementSaveInput, key) =>
      editAnnouncement(
        announcementId,
        {
          expected_revision: input.revision,
          title_zh: input.title_zh,
          body_zh: input.body_zh,
          title_en: input.title_en,
          body_en: input.body_en,
          severity: input.severity,
          pinned: input.pinned,
          dismissible: input.dismissible,
          expires_at: input.expires_at,
        },
        key,
        role,
      ),
    async (_input, error) => {
      if (error) setConflict(true);
      await client.invalidateQueries({ queryKey: managedAnnouncementKeys.pages(role) });
      await authority.refetch();
    },
    root,
  );
  const lifecycle = useRetainedOperation(
    async (
      input: { action: 'publish' | 'withdraw' | 'delete'; revision: string; reason: string },
      key,
    ) => {
      if (input.action === 'publish')
        return publishAnnouncement(announcementId, input.revision, key, role);
      if (input.action === 'withdraw')
        return withdrawAnnouncement(announcementId, input.revision, input.reason, key, role);
      return deleteAnnouncement(announcementId, input.revision, input.reason, key, role);
    },
    async (input, error) => {
      await client.invalidateQueries({ queryKey: managedAnnouncementKeys.pages(role) });
      if (!error && input.action === 'delete') {
        client.removeQueries({
          queryKey: managedAnnouncementKeys.detail(role, account, announcementId),
          exact: true,
        });
        navigate(backTo, { replace: true });
        return;
      }
      const refreshed = await authority.refetch();
      if (input.action === 'delete' && isNotFoundError(refreshed.error)) {
        client.removeQueries({
          queryKey: managedAnnouncementKeys.detail(role, account, announcementId),
          exact: true,
        });
        navigate(backTo, { replace: true });
      }
    },
    root,
  );
  const resetPreview = preview.reset;

  if (
    authority.data &&
    !authority.error &&
    !authority.isFetching &&
    authority.data.revision !== loadedRevision
  ) {
    if (!dirty && (!draft || draft.expires_at.text === draft.expires_at.originalText)) {
      setLoadedRevision(authority.data.revision);
      setDraft(fromAuthority(authority.data));
    }
  }
  useEffect(() => {
    const error = authority.error ?? preview.error ?? save.error ?? lifecycle.error;
    if (error) {
      // Never leave a dangerous confirmation open over stale authority.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setConfirmation(null);
    }
    // Capability loss must synchronously discard the private draft and preview state.
    if (isUnauthorized(error) || isForbidden(error)) {
      if (onAuthorityLoss) onAuthorityLoss();
      else clearStationSession(client, role);
      setDraft(null);
      setReason('');
      setConfirmation(null);
      resetPreview();
    } else if (isNotFoundError(authority.error)) {
      setDraft(null);
      setReason('');
      setConfirmation(null);
      resetPreview();
    }
  }, [
    authority.error,
    client,
    preview.error,
    save.error,
    lifecycle.error,
    resetPreview,
    role,
    onAuthorityLoss,
  ]);
  const update = <K extends keyof DraftState>(key: K, value: DraftState[K]) => {
    if (!draft) return;
    setDraft({ ...draft, [key]: value });
    setDirty(true);
    setConflict(false);
    preview.reset();
  };
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

  if (authority.isPending || !draft || !authority.data)
    return (
      <div className="page ops-page">
        <PageHeader
          title={t('management.announcements.detail.loadingTitle')}
          description={t('management.announcements.detail.loadingDescription')}
          back={<Link to={backTo}>{t('management.announcements.detail.back')}</Link>}
        />
        {authority.error ? (
          <ErrorState error={authority.error} onRetry={() => void authority.refetch()} />
        ) : (
          <LoadingState />
        )}
      </div>
    );
  const item = authority.data;
  const zhBodyBytes = new TextEncoder().encode(draft.body_zh).byteLength;
  const enBodyBytes = new TextEncoder().encode(draft.body_en).byteLength;
  const languagePairsValid =
    Boolean(draft.title_zh.trim()) === Boolean(draft.body_zh.trim()) &&
    Boolean(draft.title_en.trim()) === Boolean(draft.body_en.trim());
  const expiry = timeDraftValue(draft.expires_at);
  const expiryValid = expiry !== undefined;
  const draftDirty = dirty || draft.expires_at.text !== draft.expires_at.originalText;
  const draftValid =
    languagePairsValid && zhBodyBytes <= 65_536 && enBodyBytes <= 65_536 && expiryValid;
  const complete =
    draftValid &&
    Boolean(
      (draft.title_zh.trim() && draft.body_zh.trim()) ||
      (draft.title_en.trim() && draft.body_en.trim()),
    );
  const submitSave = () => {
    const expiresAt = timeDraftValue(draft.expires_at);
    if (!draftDirty || expiresAt === undefined) return;
    save.mutate(
      {
        revision: conflict ? item.revision : loadedRevision,
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
          setLoadedRevision(receipt.revision);
          setDirty(false);
          setConflict(false);
          setDraft((current) =>
            current ? { ...current, expires_at: createTimeDraft(expiresAt, 'minute') } : current,
          );
        },
      },
    );
  };
  const authorityBlocked = Boolean(authority.error) || authority.isFetching;
  return (
    <div className="page ops-page">
      <PageHeader
        title={draft.title_en || draft.title_zh || t('management.announcements.detail.untitled')}
        description={t('management.announcements.detail.description')}
        back={<Link to={backTo}>{t('management.announcements.detail.back')}</Link>}
      />
      <Card>
        <div className="ops-toolbar">
          <StatusBadge active={item.state === 'published'} label={stateLabels[item.state]} />
          <span>
            {t('management.announcements.detail.authorityRevision', { revision: item.revision })}
          </span>
          {item.published ? (
            <span>
              {t('management.announcements.detail.publishedRevision', {
                revision: item.published.revision,
              })}
            </span>
          ) : null}
        </div>
        {authority.error ? (
          <ErrorState error={authority.error} onRetry={() => void authority.refetch()} />
        ) : null}
        {conflict || loadedRevision !== item.revision ? (
          <p className="inline-notice">
            {t('management.announcements.detail.conflict', { revision: item.revision })}
          </p>
        ) : null}
      </Card>
      <Card>
        <h2>{t('management.announcements.detail.editorTitle')}</h2>
        <fieldset className="ops-field-grid" disabled={save.isPending || lifecycle.isPending}>
          <label>
            <span>{t('management.announcements.chineseTitle')}</span>
            <input
              value={draft.title_zh}
              maxLength={160}
              onChange={(event) => update('title_zh', event.target.value)}
            />
          </label>
          <label>
            <span>{t('management.announcements.englishTitle')}</span>
            <input
              value={draft.title_en}
              maxLength={160}
              onChange={(event) => update('title_en', event.target.value)}
            />
          </label>
        </fieldset>
        <fieldset className="ops-field-grid" disabled={save.isPending || lifecycle.isPending}>
          <label>
            <span>{t('management.announcements.chineseMarkdown', { bytes: zhBodyBytes })}</span>
            <textarea
              value={draft.body_zh}
              onChange={(event) => update('body_zh', event.target.value)}
            />
          </label>
          <label>
            <span>{t('management.announcements.englishMarkdown', { bytes: enBodyBytes })}</span>
            <textarea
              value={draft.body_en}
              onChange={(event) => update('body_en', event.target.value)}
            />
          </label>
        </fieldset>
        {!draftValid ? (
          <p className="field-error" role="alert">
            {t('management.announcements.validation')}
          </p>
        ) : null}
        <fieldset className="ops-field-grid" disabled={save.isPending || lifecycle.isPending}>
          <label>
            <span>{t('management.announcements.severityLabel')}</span>
            <select
              value={draft.severity}
              onChange={(event) => update('severity', event.target.value as DraftState['severity'])}
            >
              <option value="info">{severityLabels.info}</option>
              <option value="warning">{severityLabels.warning}</option>
              <option value="important">{severityLabels.important}</option>
            </select>
          </label>
          <TimeInput
            station={role === 'admin' ? 'admin' : 'user'}
            label={t('management.announcements.expiry')}
            draft={draft.expires_at}
            onChange={(updateTime) =>
              setDraft((current) =>
                current ? { ...current, expires_at: updateTime(current.expires_at) } : current,
              )
            }
          />
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={draft.pinned}
              onChange={(event) => update('pinned', event.target.checked)}
            />
            <span>{t('management.announcements.pinned')}</span>
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={draft.dismissible}
              onChange={(event) => update('dismissible', event.target.checked)}
            />
            <span>{t('management.announcements.dismissible')}</span>
          </label>
        </fieldset>
        {save.error ? <ErrorState error={save.error} /> : null}
        <div className="ops-actions">
          <button
            className="btn btn-primary"
            type="button"
            disabled={
              authorityBlocked ||
              !draftDirty ||
              !draftValid ||
              save.isPending ||
              lifecycle.isPending
            }
            onClick={submitSave}
          >
            {t('management.announcements.detail.saveDraft')}
          </button>
          <button
            className="btn btn-secondary"
            type="button"
            disabled={
              authorityBlocked ||
              !complete ||
              preview.isPending ||
              save.isPending ||
              lifecycle.isPending
            }
            onClick={() => preview.mutate(draft)}
          >
            {t('management.announcements.detail.preview')}
          </button>
          <button
            className="btn btn-link"
            type="button"
            disabled={save.isPending || lifecycle.isPending}
            onClick={() => {
              setDraft(fromAuthority(item));
              setLoadedRevision(item.revision);
              setDirty(false);
              setConflict(false);
              preview.reset();
            }}
          >
            {t('management.announcements.detail.discard')}
          </button>
        </div>
      </Card>
      {preview.error ? (
        <ErrorState error={preview.error} />
      ) : preview.data ? (
        <Card>
          <h2>
            {t('management.announcements.detail.serverPreview', {
              version: preview.data.render_profile_version,
            })}
          </h2>
          <div className="ops-grid">
            {preview.data.rendered_zh ? (
              <section>
                <h3>{t('management.announcements.detail.chinese')}</h3>
                <SafeAnnouncementBody html={preview.data.rendered_zh} />
              </section>
            ) : null}
            {preview.data.rendered_en ? (
              <section>
                <h3>{t('management.announcements.detail.english')}</h3>
                <SafeAnnouncementBody html={preview.data.rendered_en} />
              </section>
            ) : null}
          </div>
        </Card>
      ) : null}
      {item.published ? (
        <Card>
          <h2>{t('management.announcements.detail.currentProjection')}</h2>
          <p>
            {t('management.announcements.detail.publishedHint', {
              date: formatDateTime(item.published.published_at),
            })}
          </p>
          <div className="ops-grid">
            {item.published.zh ? (
              <section>
                <h3>{item.published.zh.title}</h3>
                <SafeAnnouncementBody html={item.published.zh.rendered_body} />
              </section>
            ) : null}
            {item.published.en ? (
              <section>
                <h3>{item.published.en.title}</h3>
                <SafeAnnouncementBody html={item.published.en.rendered_body} />
              </section>
            ) : null}
          </div>
        </Card>
      ) : null}
      <Card className="ops-danger">
        <h2>{t('management.announcements.detail.lifecycleTitle')}</h2>
        {lifecycle.error ? <ErrorState error={lifecycle.error} /> : null}
        <label className="ops-form-field">
          <span>{t('management.announcements.detail.reason')}</span>
          <input
            value={reason}
            maxLength={1024}
            onChange={(event) => setReason(event.target.value)}
          />
        </label>
        <div className="ops-actions">
          <button
            className="btn btn-primary"
            type="button"
            disabled={
              authorityBlocked || !complete || draftDirty || lifecycle.isPending || save.isPending
            }
            onClick={() => setConfirmation('publish')}
          >
            {item.state === 'published'
              ? t('management.announcements.detail.publishUpdate')
              : item.published
                ? t('management.announcements.detail.republish')
                : t('management.announcements.detail.publish')}
          </button>
          {item.state === 'published' ? (
            <button
              className="btn btn-danger"
              type="button"
              disabled={authorityBlocked || !reason.trim() || lifecycle.isPending || save.isPending}
              onClick={() => setConfirmation('withdraw')}
            >
              {t('management.announcements.detail.withdraw')}
            </button>
          ) : null}
          <button
            className="btn btn-danger"
            type="button"
            disabled={authorityBlocked || !reason.trim() || lifecycle.isPending || save.isPending}
            onClick={() => setConfirmation('delete')}
          >
            {t('management.announcements.detail.permanentlyDelete')}
          </button>
        </div>
      </Card>
      {confirmation ? (
        <ConfirmDialog
          open
          title={
            confirmation === 'delete'
              ? t('management.announcements.detail.confirmDeleteTitle')
              : confirmation === 'withdraw'
                ? t('management.announcements.detail.confirmWithdrawTitle')
                : item.state === 'published'
                  ? t('management.announcements.detail.confirmUpdateTitle')
                  : t('management.announcements.detail.confirmPublishTitle')
          }
          description={
            confirmation === 'delete'
              ? t('management.announcements.detail.confirmDeleteBody')
              : confirmation === 'withdraw'
                ? t('management.announcements.detail.confirmWithdrawBody')
                : t('management.announcements.detail.confirmPublishBody')
          }
          confirmLabel={
            confirmation === 'delete'
              ? t('management.announcements.detail.confirmDelete')
              : confirmation === 'withdraw'
                ? t('management.announcements.detail.withdraw')
                : t('management.announcements.detail.publish')
          }
          danger={confirmation !== 'publish'}
          busy={lifecycle.isPending}
          onCancel={() => setConfirmation(null)}
          onConfirm={() => {
            const action = confirmation;
            setConfirmation(null);
            lifecycle.mutate({ action, revision: item.revision, reason: reason.trim() });
          }}
        />
      ) : null}
    </div>
  );
}
