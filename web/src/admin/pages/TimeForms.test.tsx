import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { adminAnnouncementKeys } from '../features/operations/announcements';
import { ActivitiesPage } from './ActivitiesPage';
import { AnnouncementDetailPage } from './AnnouncementDetailPage';
import { AnnouncementsPage } from './AnnouncementsPage';

const zone = 'UTC';
const announcementId = `ann_${'A'.repeat(21)}Q`;
const periodId = `thu_${'B'.repeat(21)}Q`;
const poolId = `pol_${'C'.repeat(21)}Q`;
const originalAnnouncementExpiry = 1_893_456_047;
const originalPeriodOpening = 1_893_456_047;

interface RequestRecord {
  method: string;
  path: string;
  body: unknown;
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function announcementFixture(expiresAt: number | null = originalAnnouncementExpiry) {
  return {
    id: announcementId,
    state: 'draft',
    revision: '7',
    draft: { zh: { title: 'Old title', body: 'Old body' }, en: null },
    published: null,
    severity: 'info',
    pinned: false,
    dismissible: true,
    expires_at: expiresAt,
    withdrawn_at: null,
    created_at: 1_893_456_000,
    updated_at: 1_893_456_000,
  };
}

function periodFixture(opensAt = originalPeriodOpening) {
  return {
    id: periodId,
    period_key: '2026-W01',
    state: 'configured',
    revision: '9',
    opens_at: opensAt,
    closes_at: opensAt + 86_400,
    literature: 'A configured period',
    entry: '10',
    per_user_limit: 2,
    pumps_bp: { platform: 1_000, welfare: 2_000, next_pool: 3_000 },
    current_pool_id: poolId,
    next_pool_id: poolId,
    settlement: null,
    created_at: 1_893_456_000,
    terminal_at: null,
  };
}

function installAdminFixtures(
  handler: (method: string, path: string, body: unknown) => unknown,
): RequestRecord[] {
  const requests: RequestRecord[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const rawURL = input instanceof Request ? input.url : String(input);
      const url = new URL(rawURL, window.location.origin);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined;
      requests.push({ method, path: url.pathname, body });
      if (url.pathname === '/admin/api/time-zones') {
        return json({ version: 'go1.26.6-zoneinfo', zones: [zone] });
      }
      if (url.pathname === '/admin/api/time/resolve') {
        const local = url.searchParams.get('local');
        const timeZone = url.searchParams.get('time_zone');
        if (timeZone !== zone || local === null) return json({ error: 'unexpected resolve' }, 400);
        return json({
          instant: 1_893_456_000,
          local,
          time_zone: zone,
          offset_seconds: 0,
          adjustment: 'none',
        });
      }
      const result = await handler(method, url.pathname, body);
      return result instanceof Response ? result : json(result);
    }),
  );
  return requests;
}

function dateTimeInput(): HTMLInputElement {
  const input = document.querySelector('input[type="datetime-local"]');
  if (!(input instanceof HTMLInputElement)) throw new Error('datetime-local input not rendered');
  return input;
}

describe('administrator time forms', () => {
  const nativeOptions = Intl.DateTimeFormat.prototype.resolvedOptions;

  beforeEach(() => {
    vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(function (
      this: Intl.DateTimeFormat,
    ) {
      return { ...nativeOptions.call(this), timeZone: zone };
    });
  });

  it('freezes a resolved announcement expiry as a numeric create payload', async () => {
    let created: unknown;
    installAdminFixtures((method, path, body) => {
      if (method === 'GET' && path === '/admin/api/announcements') {
        return { data: [], next_cursor: null };
      }
      if (method === 'POST' && path === '/admin/api/announcements') {
        created = body;
        return { id: announcementId, revision: '8' };
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    const view = await renderWithProviders(<AnnouncementsPage />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(screen.getByRole('button', { name: 'Create draft' }));
    fireEvent.change(dateTimeInput(), { target: { value: '2030-01-01T00:00' } });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Create private draft' })).toBeEnabled(),
    );
    await view.user.click(screen.getByRole('button', { name: 'Create private draft' }));

    await waitFor(() =>
      expect(created).toEqual(expect.objectContaining({ expires_at: 1_893_456_000 })),
    );
  });

  it('retains an announcement expiry second when another field is saved', async () => {
    let edited: unknown;
    installAdminFixtures((method, path, body) => {
      if (method === 'GET' && path === `/admin/api/announcements/${announcementId}`) {
        return announcementFixture();
      }
      if (method === 'PATCH' && path === `/admin/api/announcements/${announcementId}`) {
        edited = body;
        return { id: announcementId, revision: '8' };
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    const view = await renderWithProviders(
      <Routes>
        <Route path="/announcements/:announcementId" element={<AnnouncementDetailPage />} />
      </Routes>,
      {
        station: 'admin',
        role: 'admin',
        route: `/announcements/${announcementId}`,
      },
    );

    const title = await screen.findByLabelText('Chinese title');
    expect(dateTimeInput()).toHaveValue('2030-01-01T00:00');
    await view.user.clear(title);
    await view.user.type(title, 'Changed title');
    await view.user.click(screen.getByRole('button', { name: 'Save private draft' }));

    await waitFor(() =>
      expect(edited).toEqual(expect.objectContaining({ expires_at: originalAnnouncementExpiry })),
    );
  });

  it('retains an activity opening second when another period field is saved', async () => {
    let saved: unknown;
    installAdminFixtures((method, path, body) => {
      if (method === 'GET' && path === '/admin/api/activities/config') {
        return {
          revision: '4',
          master_enabled: true,
          welfare: { enabled: false, threshold: '1', cap: '2' },
          thursday: { enabled: true },
        };
      }
      if (method === 'GET' && path === '/admin/api/activities/thursday') {
        return { period: periodFixture() };
      }
      if (method === 'GET' && path === '/admin/api/pools') {
        return { data: [], next_cursor: null };
      }
      if (method === 'PUT' && path === '/admin/api/activities/thursday/next') {
        saved = body;
        return periodFixture();
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    const view = await renderWithProviders(<ActivitiesPage />, {
      station: 'admin',
      role: 'admin',
      route: '/activities',
    });

    const periodKey = await screen.findByLabelText('Period key');
    await waitFor(() => expect(dateTimeInput()).toHaveValue('2030-01-01T00:00'));
    await view.user.clear(periodKey);
    await view.user.type(periodKey, '2026-W02');
    await view.user.click(screen.getByRole('button', { name: 'Save next period' }));

    await waitFor(() =>
      expect(saved).toEqual(expect.objectContaining({ opens_at: originalPeriodOpening })),
    );
  });

  it('retains the edited revision through refresh and freezes fields during the confirmed retry', async () => {
    let authority = announcementFixture();
    const revisions: string[] = [];
    let finishSave!: () => void;
    const pendingSave = new Promise<void>((resolve) => {
      finishSave = resolve;
    });
    installAdminFixtures(async (method, path, raw) => {
      if (method === 'GET' && path === `/admin/api/announcements/${announcementId}`)
        return authority;
      if (method === 'PATCH' && path === `/admin/api/announcements/${announcementId}`) {
        const body = raw as { expected_revision: string; expires_at: number; title_zh: string };
        revisions.push(body.expected_revision);
        if (body.expected_revision !== authority.revision)
          return json({ error: { code: 'conflict', message: 'Revision changed' } }, 409);
        await pendingSave;
        authority = {
          ...authority,
          revision: '9',
          expires_at: body.expires_at,
          draft: { zh: { title: body.title_zh, body: 'Old body' }, en: null },
        };
        return { id: announcementId, revision: '9' };
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    const view = await renderWithProviders(
      <Routes>
        <Route path="/announcements/:announcementId" element={<AnnouncementDetailPage />} />
      </Routes>,
      { station: 'admin', role: 'admin', route: `/announcements/${announcementId}` },
    );
    const title = await screen.findByLabelText('Chinese title');
    fireEvent.change(title, { target: { value: 'My edit' } });
    authority = { ...authority, revision: '8', expires_at: originalAnnouncementExpiry + 120 };
    await act(async () => {
      await view.queryClient.refetchQueries({
        queryKey: adminAnnouncementKeys.detail(announcementId),
      });
    });
    expect(dateTimeInput()).toHaveValue('2030-01-01T00:00');
    const save = screen.getByRole('button', { name: 'Save private draft' });
    await view.user.click(save);
    await waitFor(() => expect(screen.getByText(/compare it with revision 8/)).toBeVisible());
    expect(revisions).toEqual(['7']);
    await waitFor(() => expect(save).toBeEnabled());
    await view.user.click(save);
    await waitFor(() => expect(revisions).toEqual(['7', '8']));
    expect(title).toBeDisabled();
    expect(dateTimeInput()).toBeDisabled();
    await act(async () => {
      finishSave();
      await pendingSave;
    });
    await waitFor(() => expect(title).toBeEnabled());
    expect(authority.expires_at).toBe(originalAnnouncementExpiry);
    expect(title).toHaveValue('My edit');
  });
});
