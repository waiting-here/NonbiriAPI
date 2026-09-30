import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { useLocation } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { ApiError } from '@shared/query/http';
import {
  getAdminAnnouncement,
  editAnnouncement,
  deleteAnnouncement,
  managedAnnouncementKeys,
  type AdminAnnouncement,
} from '@shared/operations/managedAnnouncements';
import { AnnouncementEditor } from './AnnouncementEditor';

vi.mock('@shared/operations/managedAnnouncements', async (original) => ({
  ...(await original<typeof import('@shared/operations/managedAnnouncements')>()),
  getAdminAnnouncement: vi.fn(),
  editAnnouncement: vi.fn(),
  deleteAnnouncement: vi.fn(),
}));
const id = 'ann_' + 'A'.repeat(22);
const item: AdminAnnouncement = {
  id,
  state: 'draft',
  revision: '1',
  draft: { zh: null, en: { title: 'Fixture notice', body: 'Body' } },
  published: null,
  severity: 'info',
  pinned: false,
  dismissible: true,
  expires_at: null,
  withdrawn_at: null,
  created_at: 1_800_000_000,
  updated_at: 1_800_000_000,
};
const detailKey = managedAnnouncementKeys.detail('admin', 'fixture-admin', id);
function LocationProbe() {
  return <output data-testid="location">{useLocation().pathname}</output>;
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
async function setup() {
  const view = await renderWithProviders(<LocationProbe />, {
    station: 'admin',
    role: 'admin',
    route: '/announcement-detail',
  });
  act(() => {
    const session = { admin: { username: 'fixture-admin' } };
    noteManagementSessionSuccess(
      view.queryClient,
      'admin',
      session,
      beginManagementSessionRequest(view.queryClient, 'admin'),
    );
    view.queryClient.setQueryData(['admin', 'session'], session);
  });
  view.rerender(
    <>
      <AnnouncementEditor
        role="admin"
        account="fixture-admin"
        announcementId={id}
        backTo="/announcements"
      />
      <LocationProbe />
    </>,
  );
  await screen.findByLabelText('English title');
  return view;
}
type View = Awaited<ReturnType<typeof setup>>;
function changeBoundary(view: View, boundary: 'account' | 'unmount') {
  if (boundary === 'unmount') view.rerender(<LocationProbe />);
  else
    act(() => {
      const session = { admin: { username: 'next-fixture' } };
      view.queryClient.setQueryData(['admin', 'session'], session);
      view.queryClient.setQueryData(detailKey, { ...item, revision: '9' });
    });
}
async function confirmDelete(view: View) {
  fireEvent.change(screen.getByLabelText('Reason (required for withdraw/delete)'), {
    target: { value: 'Retire fixture' },
  });
  await view.user.click(screen.getByRole('button', { name: 'Permanently delete' }));
  await view.user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', { name: 'DELETE permanently' }),
  );
}
beforeEach(() => {
  vi.mocked(getAdminAnnouncement).mockReset().mockResolvedValue(item);
  vi.mocked(editAnnouncement).mockReset().mockResolvedValue({ id, revision: '2' });
  vi.mocked(deleteAnnouncement).mockReset().mockResolvedValue(undefined);
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(JSON.stringify({ mode: 'site', offset_minutes: 0 }), {
          headers: { 'Content-Type': 'application/json' },
        }),
    ),
  );
});

describe('announcement reconciliation authority', () => {
  it.each(['account', 'unmount'] as const)(
    'blocks delete cleanup and navigation after %s during list refresh',
    async (boundary) => {
      const view = await setup();
      const pending = deferred<void>();
      const invalidate = vi
        .spyOn(view.queryClient, 'invalidateQueries')
        .mockReturnValue(pending.promise);
      await confirmDelete(view);
      await waitFor(() => expect(invalidate).toHaveBeenCalledOnce());
      changeBoundary(view, boundary);
      const remove = vi.spyOn(view.queryClient, 'removeQueries');
      const reads = vi.mocked(getAdminAnnouncement).mock.calls.length;
      await act(async () => {
        pending.resolve();
        await pending.promise;
      });
      await waitFor(() => expect(view.queryClient.isMutating()).toBe(0));
      expect(remove).not.toHaveBeenCalled();
      expect(getAdminAnnouncement).toHaveBeenCalledTimes(reads);
      expect(screen.getByTestId('location')).toHaveTextContent('/announcement-detail');
      if (boundary === 'account')
        expect(view.queryClient.getQueryData(detailKey)).toMatchObject({ revision: '9' });
    },
  );

  it.each(['account', 'unmount'] as const)(
    'blocks unknown delete cleanup after %s during detail verification',
    async (boundary) => {
      const view = await setup();
      const pending = deferred<void>();
      vi.mocked(deleteAnnouncement).mockRejectedValueOnce(
        new ApiError('network_error', 'Unknown', 0),
      );
      vi.mocked(getAdminAnnouncement).mockImplementationOnce(async () => {
        await pending.promise;
        throw new ApiError('not_found', 'No longer present.', 404);
      });
      vi.spyOn(view.queryClient, 'invalidateQueries').mockResolvedValue(undefined);
      await confirmDelete(view);
      await waitFor(() => expect(getAdminAnnouncement).toHaveBeenCalledTimes(2));
      changeBoundary(view, boundary);
      const remove = vi.spyOn(view.queryClient, 'removeQueries');
      await act(async () => {
        pending.resolve();
        await pending.promise;
      });
      await waitFor(() => expect(view.queryClient.isMutating()).toBe(0));
      expect(remove).not.toHaveBeenCalled();
      expect(screen.getByTestId('location')).toHaveTextContent('/announcement-detail');
      if (boundary === 'account')
        expect(view.queryClient.getQueryData(detailKey)).toMatchObject({ revision: '9' });
    },
  );

  it.each(['account', 'unmount'] as const)(
    'does not issue a save follow-up read after %s during list refresh',
    async (boundary) => {
      const view = await setup();
      const pending = deferred<void>();
      const invalidate = vi
        .spyOn(view.queryClient, 'invalidateQueries')
        .mockReturnValue(pending.promise);
      fireEvent.change(screen.getByLabelText('English title'), {
        target: { value: 'Updated draft' },
      });
      await view.user.click(screen.getByRole('button', { name: 'Save private draft' }));
      await waitFor(() => expect(invalidate).toHaveBeenCalledOnce());
      changeBoundary(view, boundary);
      const reads = vi.mocked(getAdminAnnouncement).mock.calls.length;
      await act(async () => {
        pending.resolve();
        await pending.promise;
      });
      await waitFor(() => expect(view.queryClient.isMutating()).toBe(0));
      expect(getAdminAnnouncement).toHaveBeenCalledTimes(reads);
      if (boundary === 'account')
        expect(view.queryClient.getQueryData(detailKey)).toMatchObject({ revision: '9' });
    },
  );
});
