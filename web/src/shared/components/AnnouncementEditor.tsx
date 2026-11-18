import { Field } from '@shared/components/ui/Field';
import { Fields, FormActions } from '@shared/components/ui/Fields';
import { Button } from '@shared/components/ui/Button';
import { Fold } from '@shared/components/ui/Fold';
import { managementRoot, type ManagementRole } from '@shared/operations/managedUsers';
import { useEffect, useRef, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { TimeInput } from '@shared/components/TimeInput';
import {
  Card,
  ErrorState,
  LoadingState,
  PageHeader,
  SectionHead,
  StatusBadge,
} from '@shared/components/States';
import { isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { createTimeDraft, timeDraftValue } from '@shared/time';
import {
  draftChanged,
  fromAuthority,
  mergeDraft,
  type AnnouncementSaveInput,
  type DraftField,
  type DraftState,
} from '@shared/operations/announcementDraft';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { SafeAnnouncementBody } from '../../user/features/operations/SafeAnnouncementBody';
import {
  managedAnnouncementKeys,
  deleteAnnouncement,
  editAnnouncement,
  getAdminAnnouncement,
  previewAnnouncement,
  publishAnnouncement,
  withdrawAnnouncement,
  type AnnouncementSeverity,
  type AnnouncementState,
} from '@shared/operations/managedAnnouncements';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import '@shared/operations/operations.css';

type LifecycleInput = {
  action: 'publish' | 'withdraw' | 'delete';
  revision: string;
  reason: string;
};

export function AnnouncementEditor({
  role,
  account,
  announcementId,
  backTo,
  onAuthorityLoss,
  embedded = false,
}: {
  role: ManagementRole;
  account: string;
  announcementId: string;
  backTo: string;
  onAuthorityLoss?: () => void;
  embedded?: boolean;
}) {
  const formatDateTime = useDateTimeFormatter();
  const { t, i18n } = useTranslation();
  const root = managementRoot(role);
  const navigate = useNavigate();
  const location = useLocation();
  const client = useQueryClient();
  const authority = useQuery({
    queryKey: managedAnnouncementKeys.detail(role, account, announcementId),
    queryFn: () => getAdminAnnouncement(announcementId, role),
    retry: false,
    enabled: Boolean(announcementId && account),
  });
  const [draft, setDraft] = useState<DraftState | null>(null);
  const [base, setBase] = useState<{ revision: string; draft: DraftState } | null>(null);
  const [denied, setDenied] = useState<unknown>(null);
  const [conflicts, setConflicts] = useState<DraftField[]>([]);
  const [mergedRevision, setMergedRevision] = useState('');
  const previewHeading = useRef<HTMLHeadingElement>(null);
  const [reason, setReason] = useState('');
  const [confirmation, setConfirmation] = useState<LifecycleInput | null>(null);
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
    async (_input, _error, context) => {
      await client.invalidateQueries({ queryKey: managedAnnouncementKeys.pages(role) });
      context.assertCurrent();
      await authority.refetch();
    },
    root,
  );
  const lifecycle = useRetainedOperation(
    async (input: LifecycleInput, key) => {
      if (input.action === 'publish')
        return publishAnnouncement(announcementId, input.revision, key, role);
      if (input.action === 'withdraw')
        return withdrawAnnouncement(announcementId, input.revision, input.reason, key, role);
      return deleteAnnouncement(announcementId, input.revision, input.reason, key, role);
    },
    async (input, error, context) => {
      const leaveDeletedAnnouncement = () =>
        context.commit(() => {
          client.removeQueries({
            queryKey: managedAnnouncementKeys.detail(role, account, announcementId),
            exact: true,
          });
          navigate(backTo, { replace: true });
        });
      await client.invalidateQueries({ queryKey: managedAnnouncementKeys.pages(role) });
      context.assertCurrent();
      if (!error && input.action === 'delete') {
        leaveDeletedAnnouncement();
        return;
      }
      const refreshed = await authority.refetch();
      if (input.action === 'delete' && isNotFoundError(refreshed.error)) leaveDeletedAnnouncement();
    },
    root,
  );
  const resetPreview = preview.reset;

  const saveUnknown = save.outcome === 'unknown';
  const lifecycleUnknown = lifecycle.outcome === 'unknown';
  const writing = save.isPending || lifecycle.isPending;
  const writeLocked = writing || saveUnknown || lifecycleUnknown;
  if (
    authority.data &&
    !denied &&
    !authority.error &&
    !authority.isFetching &&
    !writeLocked &&
    (!base || BigInt(authority.data.revision) > BigInt(base.revision))
  ) {
    const latest = fromAuthority(authority.data);
    if (base && draft && draftChanged(base.draft, draft)) {
      const merged = mergeDraft(base.draft, draft, latest, conflicts);
      setDraft(merged.draft);
      setConflicts(merged.conflicts);
      setMergedRevision(authority.data.revision);
    } else {
      setDraft(latest);
      setConflicts([]);
    }
    setBase({ revision: authority.data.revision, draft: latest });
  }
  useEffect(() => {
    if (preview.data) {
      previewHeading.current?.focus({ preventScroll: true });
      previewHeading.current?.scrollIntoView?.({ block: 'center', behavior: 'instant' });
    }
  }, [preview.data]);
  useEffect(() => {
    const error = authority.error ?? preview.error ?? save.error ?? lifecycle.error;
    if (error) {
      // Never leave a dangerous confirmation open over stale authority.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setConfirmation(null);
    }
    // Capability loss must synchronously discard the private draft and preview state.
    if (isUnauthorized(error) || isForbidden(error)) {
      setDenied(error);
      setBase(null);
      setConflicts([]);
      if (onAuthorityLoss) onAuthorityLoss();
      else if (isUnauthorized(error)) clearStationSession(client, role);
      setDraft(null);
      setReason('');
      setConfirmation(null);
      resetPreview();
    } else if (isNotFoundError(authority.error)) {
      setBase(null);
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
    if (key.startsWith('title_') || key.startsWith('body_')) preview.reset();
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

  const awaitingAuthority = authority.isPending || !draft || !authority.data;
  const preferZh = Boolean(i18n.resolvedLanguage?.startsWith('zh'));
  const title =
    denied || awaitingAuthority
      ? t('management.announcements.detail.loadingTitle')
      : (preferZh ? draft.title_zh || draft.title_en : draft.title_en || draft.title_zh) ||
        t('management.announcements.detail.untitled');
  const description = t(
    awaitingAuthority
      ? 'management.announcements.detail.loadingDescription'
      : 'management.announcements.detail.description',
  );
  const back = (
    <Link to={backTo} state={{ announcementSource: location.state?.announcementSource }}>
      {t('management.announcements.detail.back')}
    </Link>
  );
  const header = embedded ? (
    <div className="ops-stack">
      <div className="nb-page-header__back">{back}</div>
      <SectionHead title={title} description={description} />
    </div>
  ) : (
    <PageHeader title={title} description={description} back={back} />
  );
  const pageClassName = embedded ? 'ops-page' : 'page ops-page';
  if (denied || authority.isPending || !base || !draft || !authority.data)
    return (
      <div className={pageClassName}>
        {header}
        {denied ? (
          <ErrorState error={denied} />
        ) : authority.error ? (
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
  const draftDirty = draftChanged(base.draft, draft);
  const draftValid =
    languagePairsValid && zhBodyBytes <= 65_536 && enBodyBytes <= 65_536 && expiryValid;
  const complete =
    draftValid &&
    Boolean(
      (draft.title_zh.trim() && draft.body_zh.trim()) ||
      (draft.title_en.trim() && draft.body_en.trim()),
    );
  const submitSave = (input: AnnouncementSaveInput) => {
    save.mutate(input, {
      onSuccess: (receipt) => {
        const current = client.getQueryData<typeof item>(
          managedAnnouncementKeys.detail(role, account, announcementId),
        );
        const latest =
          current && BigInt(current.revision) >= BigInt(receipt.revision) ? current : null;
        const saved = latest
          ? fromAuthority(latest)
          : { ...draft, expires_at: createTimeDraft(input.expires_at, 'minute') };
        setDraft(saved);
        setBase({ revision: latest?.revision ?? receipt.revision, draft: saved });
        setConflicts([]);
        setMergedRevision('');
      },
    });
  };
  const fieldLabels: Record<DraftField, string> = {
    title_zh: t('management.announcements.chineseTitle'),
    body_zh: t('management.announcements.chineseMarkdown', { bytes: zhBodyBytes }),
    title_en: t('management.announcements.englishTitle'),
    body_en: t('management.announcements.englishMarkdown', { bytes: enBodyBytes }),
    severity: t('management.announcements.severityLabel'),
    pinned: t('management.announcements.pinned'),
    dismissible: t('management.announcements.dismissible'),
    expires_at: t('management.announcements.expiry'),
  };
  const displayField = (key: DraftField, values: DraftState) => {
    if (key === 'expires_at') {
      const time = timeDraftValue(values.expires_at);
      return time
        ? formatDateTime(time)
        : values.expires_at.text || t('management.announcements.detail.emptyValue');
    }
    if (key === 'severity') return severityLabels[values.severity];
    const value = values[key];
    if (typeof value === 'boolean') return value ? t('common.yes') : t('common.no');
    return value || t('management.announcements.detail.emptyValue');
  };
  const authorityBlocked =
    Boolean(authority.error) || authority.isFetching || item.revision !== base.revision;
  const requestLifecycle = (action: LifecycleInput['action']) =>
    setConfirmation({ action, revision: base.revision, reason: reason.trim() });
  const previewCurrent =
    preview.variables?.title_zh === draft.title_zh &&
    preview.variables?.title_en === draft.title_en &&
    preview.variables?.body_zh === draft.body_zh &&
    preview.variables?.body_en === draft.body_en;
  return (
    <div className={pageClassName}>
      {header}
      <Card>
        <div className="ops-toolbar">
          <h2>{t('management.announcements.detail.editorTitle')}</h2>
          <div className="nb-actions">
            <StatusBadge active={item.state === 'published'} label={stateLabels[item.state]} />
            <Fold plain title={t('common.operations.management.details')}>
              <span>
                {t('management.announcements.detail.authorityRevision', {
                  revision: item.revision,
                })}
              </span>
              {item.published ? (
                <span>
                  {t('management.announcements.detail.publishedRevision', {
                    revision: item.published.revision,
                  })}
                </span>
              ) : null}
            </Fold>
          </div>
        </div>
        {authority.error ? (
          <ErrorState error={authority.error} onRetry={() => void authority.refetch()} />
        ) : null}
        {mergedRevision ? (
          <p className="inline-notice" role="status">
            {t('management.announcements.detail.conflict', { revision: mergedRevision })}
          </p>
        ) : null}
        {conflicts.map((key) => (
          <section
            className="ops-subcard announcement-conflict"
            key={key}
            aria-label={fieldLabels[key]}
          >
            <h3>{fieldLabels[key]}</h3>
            <dl>
              <dt>{t('management.announcements.detail.yourVersion')}</dt>
              <dd className="announcement-conflict__value">{displayField(key, draft)}</dd>
              <dt>{t('management.announcements.detail.latestVersion')}</dt>
              <dd className="announcement-conflict__value">{displayField(key, base.draft)}</dd>
            </dl>
            <FormActions>
              <Button onClick={() => setConflicts(conflicts.filter((field) => field !== key))}>
                {t('management.announcements.detail.keepMine')}
              </Button>
              <Button
                onClick={() => {
                  update(key, base.draft[key]);
                  setConflicts(conflicts.filter((field) => field !== key));
                }}
              >
                {t('management.announcements.detail.useLatest')}
              </Button>
            </FormActions>
          </section>
        ))}
        <fieldset disabled={writeLocked}>
          <Fields>
            <Field label={t('management.announcements.chineseTitle')} size="medium">
              {(field) => (
                <input
                  {...field}
                  value={draft.title_zh}
                  maxLength={160}
                  onChange={(event) => update('title_zh', event.target.value)}
                />
              )}
            </Field>
            <Field label={t('management.announcements.englishTitle')} size="medium">
              {(field) => (
                <input
                  {...field}
                  value={draft.title_en}
                  maxLength={160}
                  onChange={(event) => update('title_en', event.target.value)}
                />
              )}
            </Field>
          </Fields>
        </fieldset>
        <fieldset disabled={writeLocked}>
          <Fields>
            <Field
              label={t('management.announcements.chineseMarkdown', { bytes: zhBodyBytes })}
              size="long"
            >
              {(field) => (
                <textarea
                  {...field}
                  rows={6}
                  value={draft.body_zh}
                  onChange={(event) => update('body_zh', event.target.value)}
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
                  rows={6}
                  value={draft.body_en}
                  onChange={(event) => update('body_en', event.target.value)}
                />
              )}
            </Field>
          </Fields>
        </fieldset>
        {!draftValid ? (
          <p className="field-error" role="alert">
            {t('management.announcements.validation')}
          </p>
        ) : null}
        <fieldset disabled={writeLocked}>
          <Fields>
            <Field label={t('management.announcements.severityLabel')} size="short">
              {(field) => (
                <select
                  {...field}
                  value={draft.severity}
                  onChange={(event) =>
                    update('severity', event.target.value as DraftState['severity'])
                  }
                >
                  <option value="info">{severityLabels.info}</option>
                  <option value="warning">{severityLabels.warning}</option>
                  <option value="important">{severityLabels.important}</option>
                </select>
              )}
            </Field>
            <TimeInput
              station={role === 'admin' ? 'admin' : 'steward'}
              label={t('management.announcements.expiry')}
              draft={draft.expires_at}
              onChange={(updateTime) =>
                setDraft((current) =>
                  current ? { ...current, expires_at: updateTime(current.expires_at) } : current,
                )
              }
            />
            <Field label={t('management.announcements.pinned')} size="short">
              {(field) => (
                <input
                  {...field}
                  type="checkbox"
                  checked={draft.pinned}
                  onChange={(event) => update('pinned', event.target.checked)}
                />
              )}
            </Field>
            <Field label={t('management.announcements.dismissible')} size="short">
              {(field) => (
                <input
                  {...field}
                  type="checkbox"
                  checked={draft.dismissible}
                  onChange={(event) => update('dismissible', event.target.checked)}
                />
              )}
            </Field>
          </Fields>
        </fieldset>
        {save.error && !(save.outcome === 'conflict' && mergedRevision) ? (
          <ErrorState error={save.error} />
        ) : null}
        {saveUnknown && save.variables ? (
          <Button disabled={writing} onClick={() => save.variables && submitSave(save.variables)}>
            {t('management.announcements.detail.retrySave')}
          </Button>
        ) : null}
        <FormActions>
          <Button
            variant="primary"
            type="button"
            disabled={
              authorityBlocked || !draftDirty || !draftValid || writeLocked || conflicts.length > 0
            }
            onClick={() => {
              if (expiry !== undefined)
                submitSave({ ...draft, expires_at: expiry, revision: base.revision });
            }}
          >
            {t('management.announcements.detail.saveDraft')}
          </Button>
          <Button
            type="button"
            disabled={authorityBlocked || !complete || preview.isPending || writeLocked}
            onClick={() => preview.mutate(draft)}
          >
            {t('management.announcements.detail.preview')}
          </Button>
          <Button
            variant="ghost"
            type="button"
            disabled={(!draftDirty && conflicts.length === 0) || writeLocked}
            onClick={() => {
              setDraft(base.draft);
              setMergedRevision('');
              setConflicts([]);
              save.reset();
              preview.reset();
            }}
          >
            {t('management.announcements.detail.discard')}
          </Button>
        </FormActions>
      </Card>
      {preview.error && previewCurrent ? (
        <ErrorState error={preview.error} onRetry={() => preview.mutate(draft)} />
      ) : preview.data && previewCurrent ? (
        <Card className="announcement-preview">
          <h2 tabIndex={-1} ref={previewHeading}>
            {t('management.announcements.detail.serverPreview', {
              version: preview.data.render_profile_version,
            })}
          </h2>
          <p>{t('management.announcements.detail.previewScope')}</p>
          <div className="ops-grid">
            {preview.data.rendered_zh ? (
              <section>
                <h3 lang="zh">{draft.title_zh}</h3>
                <SafeAnnouncementBody html={preview.data.rendered_zh} />
              </section>
            ) : null}
            {preview.data.rendered_en ? (
              <section>
                <h3 lang="en">{draft.title_en}</h3>
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
      <Card>
        <h2>{t('management.announcements.detail.lifecycleTitle')}</h2>
        {lifecycle.error ? (
          <ErrorState
            error={lifecycle.error}
            onRetry={
              lifecycleUnknown && !writing && lifecycle.variables
                ? () => lifecycle.variables && lifecycle.mutate(lifecycle.variables)
                : undefined
            }
          />
        ) : null}
        {lifecycle.outcome === 'conflict' ? (
          <p className="inline-notice">{t('management.announcements.detail.lifecycleConflict')}</p>
        ) : null}
        <FormActions>
          <Button
            variant="primary"
            type="button"
            disabled={
              authorityBlocked || !complete || draftDirty || conflicts.length > 0 || writeLocked
            }
            onClick={() => requestLifecycle('publish')}
          >
            {item.state === 'published'
              ? t('management.announcements.detail.publishUpdate')
              : item.published
                ? t('management.announcements.detail.republish')
                : t('management.announcements.detail.publish')}
          </Button>
        </FormActions>
      </Card>
      <Card className="ops-danger">
        <Field label={t('management.announcements.detail.reason')} size="long">
          {(field) => (
            <input
              {...field}
              value={reason}
              disabled={writeLocked}
              maxLength={1024}
              onChange={(event) => setReason(event.target.value)}
            />
          )}
        </Field>
        <FormActions>
          {item.state === 'published' ? (
            <Button
              variant="danger-outline"
              type="button"
              disabled={authorityBlocked || !reason.trim() || writeLocked}
              onClick={() => requestLifecycle('withdraw')}
            >
              {t('management.announcements.detail.withdraw')}
            </Button>
          ) : null}
          <Button
            variant="danger"
            type="button"
            disabled={authorityBlocked || !reason.trim() || writeLocked}
            onClick={() => requestLifecycle('delete')}
          >
            {t('management.announcements.detail.permanentlyDelete')}
          </Button>
        </FormActions>
      </Card>
      {confirmation ? (
        <ConfirmDialog
          open
          title={
            confirmation.action === 'delete'
              ? t('management.announcements.detail.confirmDeleteTitle')
              : confirmation.action === 'withdraw'
                ? t('management.announcements.detail.confirmWithdrawTitle')
                : item.state === 'published'
                  ? t('management.announcements.detail.confirmUpdateTitle')
                  : t('management.announcements.detail.confirmPublishTitle')
          }
          description={
            confirmation.action === 'delete'
              ? t('management.announcements.detail.confirmDeleteBody')
              : confirmation.action === 'withdraw'
                ? t('management.announcements.detail.confirmWithdrawBody')
                : t('management.announcements.detail.confirmPublishBody')
          }
          confirmLabel={
            confirmation.action === 'delete'
              ? t('management.announcements.detail.confirmDelete')
              : confirmation.action === 'withdraw'
                ? t('management.announcements.detail.withdraw')
                : t('management.announcements.detail.publish')
          }
          danger={confirmation.action !== 'publish'}
          busy={lifecycle.isPending}
          onCancel={() => setConfirmation(null)}
          onConfirm={() => {
            setConfirmation(null);
            lifecycle.mutate(confirmation);
          }}
        />
      ) : null}
    </div>
  );
}
