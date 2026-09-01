import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import { SafeAnnouncementBody } from '../../user/features/operations/SafeAnnouncementBody';
import { adminAnnouncementKeys, announcementExpiryInput, announcementExpiryWire, deleteAnnouncement, editAnnouncement, getAdminAnnouncement, previewAnnouncement, publishAnnouncement, withdrawAnnouncement, type AdminAnnouncement } from '../features/operations/announcements';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import '@shared/operations/operations.css';

interface DraftState { title_zh: string; body_zh: string; title_en: string; body_en: string; severity: 'info' | 'warning' | 'important'; pinned: boolean; dismissible: boolean; expires_at: string }
const fromAuthority = (item: AdminAnnouncement): DraftState => ({ title_zh: item.draft.zh?.title ?? '', body_zh: item.draft.zh?.body ?? '', title_en: item.draft.en?.title ?? '', body_en: item.draft.en?.body ?? '', severity: item.severity, pinned: item.pinned, dismissible: item.dismissible, expires_at: item.expires_at === null ? '' : announcementExpiryInput(item.expires_at) });

export function AnnouncementDetailPage() {
  const { announcementId = '' } = useParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const authority = useQuery({ queryKey: adminAnnouncementKeys.detail(announcementId), queryFn: () => getAdminAnnouncement(announcementId), retry: false, enabled: Boolean(announcementId) });
  const [draft, setDraft] = useState<DraftState | null>(null);
  const [loadedRevision, setLoadedRevision] = useState('');
  const [dirty, setDirty] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [reason, setReason] = useState('');
  const [confirmation, setConfirmation] = useState<'publish' | 'withdraw' | 'delete' | null>(null);
  const preview = useMutation({ retry: false, mutationFn: (input: DraftState) => previewAnnouncement(announcementId, { expected_revision: authority.data?.revision, title_zh: input.title_zh, body_zh: input.body_zh, title_en: input.title_en, body_en: input.body_en }) });
  const save = useRetainedOperation((input: { revision: string; draft: DraftState }, key) => editAnnouncement(announcementId, { expected_revision: input.revision, title_zh: input.draft.title_zh, body_zh: input.draft.body_zh, title_en: input.draft.title_en, body_en: input.draft.body_en, severity: input.draft.severity, pinned: input.draft.pinned, dismissible: input.draft.dismissible, expires_at: announcementExpiryWire(input.draft.expires_at) }, key), async (_input, error) => { if (error) setConflict(true); await authority.refetch(); });
  const lifecycle = useRetainedOperation(async (input: { action: 'publish' | 'withdraw' | 'delete'; revision: string; reason: string }, key) => { if (input.action === 'publish') return publishAnnouncement(announcementId, input.revision, key); if (input.action === 'withdraw') return withdrawAnnouncement(announcementId, input.revision, input.reason, key); return deleteAnnouncement(announcementId, input.revision, input.reason, key); }, async (input, error) => {
    if (!error && input.action === 'delete') {
      client.removeQueries({ queryKey: adminAnnouncementKeys.detail(announcementId), exact: true });
      navigate('/announcements', { replace: true });
      return;
    }
    const refreshed = await authority.refetch();
    if (input.action === 'delete' && isNotFoundError(refreshed.error)) {
      client.removeQueries({ queryKey: adminAnnouncementKeys.detail(announcementId), exact: true });
      navigate('/announcements', { replace: true });
    }
  });
  const resetPreview = preview.reset;

  if (authority.data && !authority.error && !authority.isFetching && authority.data.revision !== loadedRevision) {
    setLoadedRevision(authority.data.revision);
    if (!dirty) setDraft(fromAuthority(authority.data));
  }
  useEffect(() => {
    const error = authority.error ?? preview.error;
    if (error) {
      // Never leave a dangerous confirmation open over stale authority.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setConfirmation(null);
    }
    // Capability loss must synchronously discard the private draft and preview state.
    if (isUnauthorized(error) || isForbidden(error)) {
      clearStationSession(client, 'admin');
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
  }, [authority.error, client, preview.error, resetPreview]);
  const update = <K extends keyof DraftState>(key: K, value: DraftState[K]) => { if (!draft) return; setDraft({ ...draft, [key]: value }); setDirty(true); setConflict(false); preview.reset(); };

  if (authority.isPending || !draft || !authority.data) return <div className="page ops-page"><PageHeader title="Announcement" description="Edit and publish an announcement." back={<Link to="/announcements">Back to announcements</Link>} />{authority.error ? <ErrorState error={authority.error} onRetry={() => void authority.refetch()} /> : <LoadingState />}</div>;
  const item = authority.data;
  const zhBodyBytes = new TextEncoder().encode(draft.body_zh).byteLength;
  const enBodyBytes = new TextEncoder().encode(draft.body_en).byteLength;
  const languagePairsValid = Boolean(draft.title_zh.trim()) === Boolean(draft.body_zh.trim())
    && Boolean(draft.title_en.trim()) === Boolean(draft.body_en.trim());
  const expiryValid = !draft.expires_at || Number.isFinite(Date.parse(draft.expires_at));
  const draftValid = languagePairsValid && zhBodyBytes <= 65_536 && enBodyBytes <= 65_536 && expiryValid;
  const complete = draftValid && Boolean((draft.title_zh.trim() && draft.body_zh.trim()) || (draft.title_en.trim() && draft.body_en.trim()));
  const authorityBlocked = Boolean(authority.error) || authority.isFetching;
  return (
    <div className="page ops-page">
      <PageHeader title={draft.title_en || draft.title_zh || 'Untitled announcement'} description="Preview uses the same bounded server renderer as publication." back={<Link to="/announcements">Back to announcements</Link>} />
      <Card><div className="ops-toolbar"><StatusBadge active={item.state === 'published'} label={item.state} /><span>authority revision {item.revision}</span>{item.published ? <span>published revision {item.published.revision}</span> : null}</div>{authority.error ? <ErrorState error={authority.error} onRetry={() => void authority.refetch()} /> : null}{conflict ? <p className="inline-notice">Authority changed or the result is unknown. Your non-sensitive local draft was retained; compare it with revision {item.revision} before confirming again.</p> : null}</Card>
      <Card><h2>Draft editor</h2><div className="ops-field-grid"><label><span>Chinese title</span><input value={draft.title_zh} maxLength={160} onChange={(event) => update('title_zh', event.target.value)} /></label><label><span>English title</span><input value={draft.title_en} maxLength={160} onChange={(event) => update('title_en', event.target.value)} /></label></div><div className="ops-field-grid"><label><span>Chinese Markdown · {zhBodyBytes} / 65,536 bytes</span><textarea value={draft.body_zh} onChange={(event) => update('body_zh', event.target.value)} /></label><label><span>English Markdown · {enBodyBytes} / 65,536 bytes</span><textarea value={draft.body_en} onChange={(event) => update('body_en', event.target.value)} /></label></div>{!draftValid ? <p className="field-error" role="alert">Each language must contain both title and body or neither; each body is limited to 65,536 UTF-8 bytes, and expiry must be valid.</p> : null}<div className="ops-field-grid"><label><span>Severity</span><select value={draft.severity} onChange={(event) => update('severity', event.target.value as DraftState['severity'])}><option value="info">Info</option><option value="warning">Warning</option><option value="important">Important</option></select></label><label><span>Expiry</span><input type="datetime-local" value={draft.expires_at} onChange={(event) => update('expires_at', event.target.value)} /></label><label><input type="checkbox" checked={draft.pinned} onChange={(event) => update('pinned', event.target.checked)} /> Pinned</label><label><input type="checkbox" checked={draft.dismissible} onChange={(event) => update('dismissible', event.target.checked)} /> Dismissible</label></div>{save.error ? <ErrorState error={save.error} /> : null}<div className="ops-actions"><button className="btn btn-primary" type="button" disabled={authorityBlocked || !dirty || !draftValid || save.isPending || lifecycle.isPending} onClick={() => save.mutate({ revision: item.revision, draft }, { onSuccess: () => { setDirty(false); setConflict(false); } })}>Save private draft</button><button className="btn btn-secondary" type="button" disabled={authorityBlocked || !complete || preview.isPending || save.isPending || lifecycle.isPending} onClick={() => preview.mutate(draft)}>Preview</button><button className="btn btn-link" type="button" disabled={save.isPending || lifecycle.isPending} onClick={() => { setDraft(fromAuthority(item)); setDirty(false); setConflict(false); preview.reset(); }}>Discard local edits</button></div></Card>
      {preview.error ? <ErrorState error={preview.error} /> : preview.data ? <Card><h2>Server preview · {preview.data.render_profile_version}</h2><div className="ops-grid">{preview.data.rendered_zh ? <section><h3>Chinese</h3><SafeAnnouncementBody html={preview.data.rendered_zh} /></section> : null}{preview.data.rendered_en ? <section><h3>English</h3><SafeAnnouncementBody html={preview.data.rendered_en} /></section> : null}</div></Card> : null}
      {item.published ? <Card><h2>Current user projection</h2><p>Published {formatDateTime(item.published.published_at)}. Editing the draft does not change this projection until Publish update.</p><div className="ops-grid">{item.published.zh ? <section><h3>{item.published.zh.title}</h3><SafeAnnouncementBody html={item.published.zh.rendered_body} /></section> : null}{item.published.en ? <section><h3>{item.published.en.title}</h3><SafeAnnouncementBody html={item.published.en.rendered_body} /></section> : null}</div></Card> : null}
      <Card className="ops-danger"><h2>Publication lifecycle</h2>{lifecycle.error ? <ErrorState error={lifecycle.error} /> : null}<label className="ops-form-field"><span>Reason (required for withdraw/delete)</span><input value={reason} maxLength={1024} onChange={(event) => setReason(event.target.value)} /></label><div className="ops-actions"><button className="btn btn-primary" type="button" disabled={authorityBlocked || !complete || dirty || lifecycle.isPending || save.isPending} onClick={() => setConfirmation('publish')}>{item.state === 'published' ? 'Publish update' : item.published ? 'Republish' : 'Publish'}</button>{item.state === 'published' ? <button className="btn btn-danger" type="button" disabled={authorityBlocked || !reason.trim() || lifecycle.isPending || save.isPending} onClick={() => setConfirmation('withdraw')}>Withdraw</button> : null}<button className="btn btn-danger" type="button" disabled={authorityBlocked || !reason.trim() || lifecycle.isPending || save.isPending} onClick={() => setConfirmation('delete')}>Permanently delete</button></div></Card>
      {confirmation ? <ConfirmDialog open title={confirmation === 'delete' ? 'Permanently delete announcement?' : confirmation === 'withdraw' ? 'Withdraw announcement?' : item.state === 'published' ? 'Publish this update?' : 'Publish announcement?'} description={confirmation === 'delete' ? 'Content is permanently removed with no recycle bin; only a minimal content-free audit remains.' : confirmation === 'withdraw' ? 'The current user projection disappears immediately.' : 'The current saved draft becomes the atomic user projection.'} confirmLabel={confirmation === 'delete' ? 'DELETE permanently' : confirmation === 'withdraw' ? 'Withdraw' : 'Publish'} danger={confirmation !== 'publish'} busy={lifecycle.isPending} onCancel={() => setConfirmation(null)} onConfirm={() => { const action = confirmation; setConfirmation(null); lifecycle.mutate({ action, revision: item.revision, reason: reason.trim() }); }} /> : null}
    </div>
  );
}
