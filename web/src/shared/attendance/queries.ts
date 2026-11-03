import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { economySessionRequest } from '../../user/features/economy/queries';
import { attendanceKeys, listAttendance } from './api';

export function useAttendancePage(admin: boolean, account: string, enabled = true) {
  const client = useQueryClient();
  const pager = useUrlPagePager({
    station: admin ? 'admin' : 'user',
    listType: 'attendance',
    scopeKey: account,
    scopeReady: enabled && !!account,
    pageParam: 'attendance_page',
    pageSizeParam: 'attendance_page_size',
  });
  const query = useQuery({
    queryKey: [...attendanceKeys.root(admin, account), pager.page, pager.pageSize],
    queryFn: ({ signal }) =>
      admin
        ? listAttendance(true, pager.page, pager.pageSize, signal)
        : economySessionRequest(
            client,
            () => listAttendance(false, pager.page, pager.pageSize, signal),
            account,
          ),
    enabled: enabled && !!account,
  });
  const date = query.data?.site_date;
  const offset = query.data?.timezone_offset_minutes;
  const refetch = query.refetch;
  useEffect(() => {
    if (!date || offset === undefined || !enabled) return;
    const midnight = Date.parse(date + 'T00:00:00Z') - offset * 60_000 + 86_400_000;
    const timer = window.setTimeout(() => void refetch(), Math.max(1000, midnight - Date.now()));
    return () => window.clearTimeout(timer);
  }, [date, offset, enabled, refetch]);
  return { query, pager };
}
