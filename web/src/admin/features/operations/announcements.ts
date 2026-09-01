import { apiFetch } from '@shared/query/http';
import { decoded, idempotentOptions, queryPath } from '@shared/operations/api';
import {
  boolean, decimal, invalidResponse, nullableString, nullableUnixSecond, oneOf, opaqueID,
  page, record, string, unixSecond,
} from '@shared/operations/wire';

export type AnnouncementState = 'draft' | 'published' | 'withdrawn' | 'expired';
export type AnnouncementSeverity = 'info' | 'warning' | 'important';
export interface LanguageDraft { title: string; body: string }
export interface LanguagePublished { title: string; rendered_body: string }
export interface AdminAnnouncement {
  id: string; state: AnnouncementState; revision: string;
  draft: { zh: LanguageDraft | null; en: LanguageDraft | null };
  published: { revision: string; published_at: number; zh: LanguagePublished | null; en: LanguagePublished | null } | null;
  severity: AnnouncementSeverity; pinned: boolean; dismissible: boolean;
  expires_at: number | null; withdrawn_at: number | null; created_at: number; updated_at: number;
}

function normalizeDraft(value: unknown, label: string): LanguageDraft | null {
  if (value === null) return null;
  const root = record(value, ['title', 'body'], label);
  const title = string(root.title, `${label} title`, { min: 1, max: 160, bytes: 640 });
  const body = string(root.body, `${label} body`, { min: 1, max: 65_536, bytes: 65_536, multiline: true });
  return { title, body };
}
function normalizePublishedLanguage(value: unknown, label: string): LanguagePublished | null {
  if (value === null) return null;
  const root = record(value, ['title', 'rendered_body'], label);
  return {
    title: string(root.title, `${label} title`, { min: 1, max: 160, bytes: 640 }),
    rendered_body: string(root.rendered_body, `${label} rendered body`, { min: 1, max: 65_536, bytes: 65_536, multiline: true }),
  };
}

export function normalizeAdminAnnouncement(value: unknown): AdminAnnouncement {
  const root = record(value, ['id', 'state', 'revision', 'draft', 'published', 'severity', 'pinned', 'dismissible', 'expires_at', 'withdrawn_at', 'created_at', 'updated_at'], 'administrator announcement');
  const draftRoot = record(root.draft, ['zh', 'en'], 'announcement draft languages');
  const draft = { zh: normalizeDraft(draftRoot.zh, 'Chinese announcement draft'), en: normalizeDraft(draftRoot.en, 'English announcement draft') };
  let published: AdminAnnouncement['published'] = null;
  if (root.published !== null) {
    const item = record(root.published, ['revision', 'published_at', 'zh', 'en'], 'published announcement');
    published = {
      revision: decimal(item.revision, 'published announcement revision', { positive: true }),
      published_at: unixSecond(item.published_at, 'announcement publication time'),
      zh: normalizePublishedLanguage(item.zh, 'published Chinese announcement'),
      en: normalizePublishedLanguage(item.en, 'published English announcement'),
    };
    if (published.zh === null && published.en === null) invalidResponse('published announcement languages');
  }
  const state = oneOf(root.state, ['draft', 'published', 'withdrawn', 'expired'] as const, 'announcement state');
  if ((state === 'published' || state === 'withdrawn' || state === 'expired') && published === null) invalidResponse('announcement publication state');
  const withdrawnAt = nullableUnixSecond(root.withdrawn_at, 'announcement withdrawal time');
  if ((state === 'withdrawn') !== (withdrawnAt !== null)) invalidResponse('announcement withdrawal state');
  return {
    id: opaqueID(root.id, 'ann_', 'announcement id'), state,
    revision: decimal(root.revision, 'announcement revision', { positive: true }), draft, published,
    severity: oneOf(root.severity, ['info', 'warning', 'important'] as const, 'announcement severity'),
    pinned: boolean(root.pinned, 'announcement pinned marker'), dismissible: boolean(root.dismissible, 'announcement dismissible marker'),
    expires_at: nullableUnixSecond(root.expires_at, 'announcement expiry'), withdrawn_at: withdrawnAt,
    created_at: unixSecond(root.created_at, 'announcement creation time'), updated_at: unixSecond(root.updated_at, 'announcement update time'),
  };
}

export interface AnnouncementReceipt { id: string; revision: string }
export function normalizeAnnouncementReceipt(value: unknown): AnnouncementReceipt {
  const root = record(value, ['id', 'revision'], 'announcement mutation receipt');
  return { id: opaqueID(root.id, 'ann_', 'announcement receipt id'), revision: decimal(root.revision, 'announcement receipt revision', { positive: true }) };
}

export interface AnnouncementPreview { rendered_zh: string | null; rendered_en: string | null; render_profile_version: string }
export function normalizeAnnouncementPreview(value: unknown): AnnouncementPreview {
  const root = record(value, ['rendered_zh', 'rendered_en', 'render_profile_version'], 'announcement preview');
  const result = {
    rendered_zh: nullableString(root.rendered_zh, 'Chinese announcement preview', { min: 1, max: 65_536, bytes: 65_536, multiline: true }),
    rendered_en: nullableString(root.rendered_en, 'English announcement preview', { min: 1, max: 65_536, bytes: 65_536, multiline: true }),
    render_profile_version: string(root.render_profile_version, 'announcement render profile', { min: 1, max: 64, bytes: 64, ascii: true }),
  };
  return result;
}

// datetime-local carries wall-clock components without an offset. Shift an
// authority epoch by the offset that applies at that exact instant (including
// DST) before taking the ISO components; using the raw UTC ISO value would
// silently move expiry whenever the browser is not in UTC.
export function announcementExpiryInput(epochSeconds: number, offsetMinutes?: number): string {
  const instant = new Date(epochSeconds * 1_000);
  const offset = offsetMinutes ?? instant.getTimezoneOffset();
  return new Date(instant.getTime() - offset * 60_000).toISOString().slice(0, 16);
}

export function announcementExpiryWire(value: string): number | null {
  return value ? Math.floor(new Date(value).getTime() / 1_000) : null;
}

export const adminAnnouncementKeys = {
  list: (state: string, severity: string, cursor: string | null) => ['admin', 'operations', 'announcements', state, severity, cursor] as const,
  detail: (id: string) => ['admin', 'operations', 'announcement', id] as const,
};
export const getAdminAnnouncements = (state: string, severity: string, cursor: string | null) => decoded(queryPath('/admin/api/announcements', { state: state || undefined, severity: severity || undefined, cursor, limit: 50 }), (value) => page(value, 'administrator announcement page', normalizeAdminAnnouncement));
export const getAdminAnnouncement = (id: string) => decoded(`/admin/api/announcements/${encodeURIComponent(opaqueID(id, 'ann_', 'announcement id'))}`, normalizeAdminAnnouncement);
export const createAnnouncement = (body: unknown, key: string) => decoded('/admin/api/announcements', normalizeAnnouncementReceipt, idempotentOptions(key, { method: 'POST', json: body }));
export const editAnnouncement = (id: string, body: unknown, key: string) => decoded(`/admin/api/announcements/${encodeURIComponent(opaqueID(id, 'ann_', 'announcement id'))}`, normalizeAnnouncementReceipt, idempotentOptions(key, { method: 'PATCH', json: body }));
export const previewAnnouncement = (id: string, body: unknown) => decoded(`/admin/api/announcements/${encodeURIComponent(opaqueID(id, 'ann_', 'announcement id'))}/preview`, normalizeAnnouncementPreview, { method: 'POST', json: body });
export async function publishAnnouncement(id: string, revision: string, key: string): Promise<void> { await apiFetch<void>(`/admin/api/announcements/${encodeURIComponent(opaqueID(id, 'ann_', 'announcement id'))}/publish`, idempotentOptions(key, { method: 'POST', json: { expected_revision: revision } })); }
export async function withdrawAnnouncement(id: string, revision: string, reason: string, key: string): Promise<void> { await apiFetch<void>(`/admin/api/announcements/${encodeURIComponent(opaqueID(id, 'ann_', 'announcement id'))}/withdraw`, idempotentOptions(key, { method: 'POST', json: { expected_revision: revision, reason } })); }
export async function deleteAnnouncement(id: string, revision: string, reason: string, key: string): Promise<void> { await apiFetch<void>(`/admin/api/announcements/${encodeURIComponent(opaqueID(id, 'ann_', 'announcement id'))}`, idempotentOptions(key, { method: 'DELETE', json: { expected_revision: revision, confirmation: 'DELETE', reason } })); }
