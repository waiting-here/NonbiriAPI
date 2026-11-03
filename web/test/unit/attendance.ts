import type { AttendancePage, AttendancePeriod } from '../../src/shared/attendance/api';

export const attendanceAccount = { user: { id: '7', username: 'reader', effective_level: 1 } };
export const attendanceAdmin = { admin: { username: 'operator' } };
export function attendancePeriod(patch: Partial<AttendancePeriod> = {}): AttendancePeriod {
  return {
    id: 'autumn',
    revision: 1,
    title: 'Autumn days',
    description: 'A small daily reward.',
    start_date: '2026-11-01',
    end_date: '2026-11-30',
    mode: 'cumulative',
    published: true,
    timezone_offset_minutes: 480,
    created_at: 1_790_000_000,
    status: 'active',
    site_date: '2026-11-03',
    signed_today: false,
    total_days: 6,
    current_streak: 2,
    best_streak: 2,
    eligible_days: 6,
    config_editable: false,
    can_sign_in: true,
    available: true,
    tiers: [
      { days: 7, rewards: [{ asset: 'general', quantity: '100' }], awarded: false },
      { days: 14, rewards: [{ asset: 'game', quantity: '200' }], awarded: false },
    ],
    ...patch,
  };
}
export function attendancePage(items: AttendancePeriod[] = [attendancePeriod()]): AttendancePage {
  return {
    items,
    page: 1,
    page_size: 20,
    total: items.length,
    site_date: '2026-11-03',
    timezone_offset_minutes: 480,
    available: true,
  };
}
export const attendanceAssets = [
  {
    id: 'general',
    name: '通用积分',
    name_en: 'General credits',
    group: 'credits',
    precision: 3,
    maximum: '9000000000000',
  },
  {
    id: 'game',
    name: '游戏积分',
    name_en: 'Game credits',
    group: 'credits',
    precision: 3,
    maximum: '9000000000000',
  },
];
