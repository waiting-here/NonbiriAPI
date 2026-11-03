import { apiFetch } from '@shared/query/http';
import { idempotentOptions, queryPath } from '@shared/operations/api';
import type { PageSize, PageMetadata } from '@shared/operations/pageNumbers';
import type { Item, Result } from '@shared/rewards/api';

export interface AttendanceTier {
  days: number;
  rewards: Item[];
  awarded: boolean;
  results?: Result[];
  awarded_at?: number;
}
export interface AttendanceInput {
  title: string;
  description: string;
  start_date: string;
  end_date: string;
  mode: 'cumulative' | 'consecutive';
  published: boolean;
  tiers: Pick<AttendanceTier, 'days' | 'rewards'>[];
}
export interface AttendancePeriod extends AttendanceInput {
  id: string;
  revision: number;
  timezone_offset_minutes: number;
  created_at: number;
  status: 'upcoming' | 'active' | 'ended';
  site_date: string;
  signed_today: boolean;
  total_days: number;
  current_streak: number;
  best_streak: number;
  eligible_days: number;
  config_editable: boolean;
  can_sign_in: boolean;
  available: boolean;
  tiers: AttendanceTier[];
}
export interface AttendancePage {
  items: AttendancePeriod[];
  page: number;
  page_size: PageSize;
  total: number;
  site_date: string;
  timezone_offset_minutes: number;
  available: boolean;
  unavailable_reason?: string;
}
export interface SignInResult {
  period: AttendancePeriod;
  already_signed: boolean;
  period_id: string;
  site_date: string;
  signed_at: number;
  awards: { days: number; results: Result[]; awarded_at: number }[];
}
export const attendanceKeys = {
  root: (admin: boolean, account: string) =>
    [admin ? 'admin' : 'user', 'attendance', account] as const,
};
export const listAttendance = (
  admin: boolean,
  page: string,
  size: PageSize,
  signal?: AbortSignal,
) =>
  apiFetch<AttendancePage>(
    queryPath((admin ? '/admin' : '') + '/api/attendance', { page, page_size: size }),
    { signal },
  );
export const signIn = (id: string, siteDate: string, key: string, signal?: AbortSignal) =>
  apiFetch<SignInResult>(
    '/api/attendance/' + encodeURIComponent(id) + '/sign-in',
    idempotentOptions(key, { method: 'POST', json: { site_date: siteDate }, signal }),
  );
export const saveAttendance = (
  input: AttendanceInput,
  key: string,
  period?: Pick<AttendancePeriod, 'id' | 'revision'>,
  signal?: AbortSignal,
) =>
  apiFetch<AttendancePeriod>(
    '/admin/api/attendance' + (period ? '/' + encodeURIComponent(period.id) : ''),
    idempotentOptions(key, {
      method: period ? 'PUT' : 'POST',
      json: { ...input, ...(period ? { expected_revision: period.revision } : {}) },
      signal,
    }),
  );
export function attendanceMetadata(page: AttendancePage): PageMetadata {
  return {
    page: String(page.page),
    page_size: page.page_size,
    total_items: String(page.total),
    total_pages: String(Math.max(1, Math.ceil(page.total / page.page_size))),
  };
}
