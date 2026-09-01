import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { formatDateTime } from '@shared/utils/datetime';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { adminAnnouncementKeys, createAnnouncement, getAdminAnnouncements } from '../features/operations/announcements';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import '@shared/operations/operations.css';

function optionalUnix(value: string): number | null {
  if (!value) return null;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? Math.floor(parsed / 1_000) : -1;
}

export function AnnouncementsPage() {
  const navigate = useNavigate();
  const client = useQueryClient();
  const pager = useCursorPager();
  const [state, setState] = useState('');
  const [severity, setSeverity] = useState('');
  const [creating, setCreating] = useState(false);
  const [draft, setDraft] = useState({ title_zh: '', body_zh: '', title_en: '', body_en: '', severity: 'info', pinned: false, dismissible: true, expires: '' });
  const list = useQuery({ queryKey: adminAnnouncementKeys.list(state, severity, pager.cursor), queryFn: () => getAdminAnnouncements(state, severity, pager.cursor), retry: false });
  const create = useRetainedOperation(
    (input: typeof draft, key) => createAnnouncement({ title_zh: input.title_zh, body_zh: input.body_zh, title_en: input.title_en, body_en: input.body_en, severity: input.severity, pinned: input.pinned, dismissible: input.dismissible, expires_at: optionalUnix(input.expires) }, key),
    async () => { await list.refetch(); },
  );
  const expiry = optionalUnix(draft.expires);
  const zhBodyBytes = new TextEncoder().encode(draft.body_zh).byteLength;
  const enBodyBytes = new TextEncoder().encode(draft.body_en).byteLength;
  const languagePairsValid = Boolean(draft.title_zh.trim()) === Boolean(draft.body_zh.trim())
    && Boolean(draft.title_en.trim()) === Boolean(draft.body_en.trim());
  const draftValid = languagePairsValid && zhBodyBytes <= 65_536 && enBodyBytes <= 65_536 && expiry !== -1;
  useEffect(() => {
    if (isUnauthorized(list.error) || isForbidden(list.error)) clearStationSession(client, 'admin');
  }, [client, list.error]);
  return (
    <div className="page ops-page">
      <PageHeader title="Announcements" description="Draft content remains private until an explicit publish or publish-update mutation." actions={<button className="btn btn-primary" type="button" onClick={() => setCreating((value) => !value)}>{creating ? 'Close creator' : 'Create draft'}</button>} />
      {creating ? <Card><h2>New draft</h2><p>A draft may start empty. At least one language must have both a title and body before publication.</p><div className="ops-field-grid"><label><span>Chinese title</span><input value={draft.title_zh} maxLength={160} onChange={(event) => setDraft({ ...draft, title_zh: event.target.value })} /></label><label><span>English title</span><input value={draft.title_en} maxLength={160} onChange={(event) => setDraft({ ...draft, title_en: event.target.value })} /></label></div><div className="ops-field-grid"><label><span>Chinese Markdown · {zhBodyBytes} / 65,536 bytes</span><textarea value={draft.body_zh} onChange={(event) => setDraft({ ...draft, body_zh: event.target.value })} /></label><label><span>English Markdown · {enBodyBytes} / 65,536 bytes</span><textarea value={draft.body_en} onChange={(event) => setDraft({ ...draft, body_en: event.target.value })} /></label></div>{!draftValid ? <p className="field-error" role="alert">Each language must contain both title and body or neither; each body is limited to 65,536 UTF-8 bytes, and expiry must be valid.</p> : null}<div className="ops-field-grid"><label><span>Severity</span><select value={draft.severity} onChange={(event) => setDraft({ ...draft, severity: event.target.value })}><option value="info">Info</option><option value="warning">Warning</option><option value="important">Important</option></select></label><label><span>Expiry (optional)</span><input type="datetime-local" value={draft.expires} onChange={(event) => setDraft({ ...draft, expires: event.target.value })} /></label><label><input type="checkbox" checked={draft.pinned} onChange={(event) => setDraft({ ...draft, pinned: event.target.checked })} /> Pinned</label><label><input type="checkbox" checked={draft.dismissible} onChange={(event) => setDraft({ ...draft, dismissible: event.target.checked })} /> Dismissible</label></div>{create.error ? <ErrorState error={create.error} /> : null}<button className="btn btn-primary" type="button" disabled={create.isPending || !draftValid} onClick={() => create.mutate(draft, { onSuccess: (receipt) => navigate(`/announcements/${encodeURIComponent(receipt.id)}`) })}>Create private draft</button></Card> : null}
      <Card>
        <div className="ops-toolbar"><label><span>State</span><select value={state} onChange={(event) => { setState(event.target.value); pager.reset(); }}><option value="">All</option><option value="draft">Draft</option><option value="published">Published</option><option value="withdrawn">Withdrawn</option><option value="expired">Expired</option></select></label><label><span>Severity</span><select value={severity} onChange={(event) => { setSeverity(event.target.value); pager.reset(); }}><option value="">All</option><option value="info">Info</option><option value="warning">Warning</option><option value="important">Important</option></select></label></div>
        {list.isPending ? <LoadingState /> : list.error ? <ErrorState error={list.error} onRetry={() => void list.refetch()} /> : list.data.data.length === 0 ? <EmptyState title="No announcements" body="The selected authority view is empty." /> : <><div className="ops-table-scroll"><table className="ops-table"><thead><tr><th>State</th><th>Draft title</th><th>Severity</th><th>Publication</th><th>Updated</th><th>Open</th></tr></thead><tbody>{list.data.data.map((item) => <tr key={item.id}><td><StatusBadge active={item.state === 'published'} label={item.state} /></td><td>{item.draft.en?.title ?? item.draft.zh?.title ?? '(empty draft)'}</td><td>{item.severity}{item.pinned ? ' · pinned' : ''}</td><td>{item.published ? `${item.published.revision} · ${formatDateTime(item.published.published_at)}` : 'Never published'}</td><td>{formatDateTime(item.updated_at)}</td><td><button className="btn btn-secondary" type="button" onClick={() => navigate(`/announcements/${encodeURIComponent(item.id)}`)}>Edit</button></td></tr>)}</tbody></table></div><CursorPagination page={pager.page} nextCursor={list.data.next_cursor} onPrevious={pager.previous} onNext={pager.next} /></>}
      </Card>
    </div>
  );
}
