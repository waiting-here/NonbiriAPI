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
  publishAnnouncement,
  withdrawAnnouncement,
  deleteAnnouncement,
  managedAnnouncementKeys,
  type AdminAnnouncement,
} from '@shared/operations/managedAnnouncements';
import { AnnouncementEditor } from './AnnouncementEditor';

vi.mock('@shared/operations/managedAnnouncements', async (original) => ({
  ...(await original<typeof import('@shared/operations/managedAnnouncements')>()),
  getAdminAnnouncement: vi.fn(),
  editAnnouncement: vi.fn(),
  publishAnnouncement: vi.fn(),
  withdrawAnnouncement: vi.fn(),
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
async function setup(embedded = false, ready = true) {
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
      {embedded ? <h1>Management workspace</h1> : null}
      <AnnouncementEditor
        embedded={embedded}
        role="admin"
        account="fixture-admin"
        announcementId={id}
        backTo="/announcements"
      />
      <LocationProbe />
    </>,
  );
  if (ready) await screen.findByLabelText('English title');
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
  vi.mocked(publishAnnouncement).mockReset().mockResolvedValue(undefined);
  vi.mocked(withdrawAnnouncement).mockReset().mockResolvedValue(undefined);
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
  it('merges a concurrent body edit and requires a choice for competing titles', async () => {
    const view = await setup();
    fireEvent.change(screen.getByLabelText('English title'), { target: { value: 'My title' } });
    const latest = {
      ...item,
      revision: '2',
      draft: { ...item.draft, en: { title: 'Other title', body: 'Other body' } },
    };
    vi.mocked(getAdminAnnouncement).mockResolvedValue(latest);
    vi.mocked(editAnnouncement).mockRejectedValueOnce(
      new ApiError('revision_conflict', 'Changed', 409),
    );
    const save = screen.getByRole('button', { name: 'Save private draft' });
    await view.user.click(save);
    const choice = await screen.findByRole('region', { name: 'English title' });
    expect(choice).toHaveTextContent('My title');
    expect(choice).toHaveTextContent('Other title');
    expect(screen.getByLabelText('English body')).toHaveValue('Other body');
    expect(save).toBeDisabled();
    await view.user.click(within(choice).getByRole('button', { name: 'Use my version' }));
    await view.user.click(save);
    const calls = vi.mocked(editAnnouncement).mock.calls;
    expect(calls).toHaveLength(2);
    expect(calls[1][1]).toMatchObject({
      expected_revision: '2',
      title_en: 'My title',
      body_en: 'Other body',
    });
    expect(calls[1][2]).not.toBe(calls[0][2]);
  });

  it('replays a save with the original body, revision and key after a lost response', async () => {
    const view = await setup();
    fireEvent.change(screen.getByLabelText('English title'), { target: { value: 'Saved title' } });
    const latest = {
      ...item,
      revision: '2',
      draft: { ...item.draft, en: { title: 'Saved title', body: 'Body' } },
    };
    vi.mocked(getAdminAnnouncement).mockResolvedValue(latest);
    vi.mocked(editAnnouncement).mockRejectedValueOnce(new ApiError('network_error', 'Unknown', 0));
    await view.user.click(screen.getByRole('button', { name: 'Save private draft' }));
    const retry = await screen.findByRole('button', { name: 'Retry the same save' });
    await waitFor(() => expect(retry).toBeEnabled());
    await view.user.click(retry);
    const calls = vi.mocked(editAnnouncement).mock.calls;
    expect(calls).toHaveLength(2);
    expect(calls[1]).toEqual(calls[0]);
    expect(calls[1][1]).toMatchObject({ expected_revision: '1', title_en: 'Saved title' });
  });

  it.each(['publish', 'withdraw'] as const)(
    'replays the original %s after its response is lost',
    async (action) => {
      const published: AdminAnnouncement = {
        ...item,
        state: 'published',
        revision: '2',
        published: {
          revision: '2',
          published_at: item.updated_at,
          zh: null,
          en: { title: 'Fixture notice', rendered_body: '<p>Body</p>' },
        },
      };
      if (action === 'withdraw') vi.mocked(getAdminAnnouncement).mockResolvedValue(published);
      const view = await setup();
      const write =
        action === 'publish' ? vi.mocked(publishAnnouncement) : vi.mocked(withdrawAnnouncement);
      write.mockRejectedValueOnce(new ApiError('network_error', 'Unknown', 0));
      vi.mocked(getAdminAnnouncement).mockResolvedValue(
        action === 'publish'
          ? published
          : { ...published, state: 'withdrawn', revision: '3', withdrawn_at: item.updated_at },
      );
      if (action === 'withdraw')
        fireEvent.change(screen.getByLabelText('Reason (required for withdraw/delete)'), {
          target: { value: 'End of notice' },
        });
      const label = action === 'publish' ? 'Publish' : 'Withdraw';
      await view.user.click(screen.getByRole('button', { name: label }));
      await view.user.click(
        within(screen.getByRole('alertdialog')).getByRole('button', { name: label }),
      );
      await view.user.click(await screen.findByRole('button', { name: 'Retry' }));
      expect(write.mock.calls).toHaveLength(2);
      expect(write.mock.calls[1]).toEqual(write.mock.calls[0]);
    },
  );

  it('keeps the standalone editor page heading', async () => {
    await setup();
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    expect(screen.getByRole('heading', { level: 1, name: 'Fixture notice' })).toBeVisible();
  });
  it('keeps one page heading while an embedded editor loads, renders and fails', async () => {
    const pending = deferred<AdminAnnouncement>();
    vi.mocked(getAdminAnnouncement).mockReturnValueOnce(pending.promise);
    const view = await setup(true, false);
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    expect(screen.getByRole('heading', { level: 2, name: 'Announcement' })).toBeVisible();
    expect(screen.getByRole('link', { name: 'Back to announcements' })).toHaveAttribute(
      'href',
      '/announcements',
    );
    await act(async () => {
      pending.resolve(item);
      await pending.promise;
    });
    await screen.findByLabelText('English title');
    expect(screen.getByRole('heading', { level: 2, name: 'Fixture notice' })).toBeVisible();
    vi.mocked(getAdminAnnouncement).mockRejectedValueOnce(
      new ApiError('not_found', 'Notice unavailable', 404),
    );
    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: detailKey });
    });
    await screen.findByRole('alert');
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 2, name: 'Announcement' })).toBeVisible(),
    );
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    expect(screen.getByRole('link', { name: 'Back to announcements' })).toBeVisible();
    expect(screen.queryByLabelText('English title')).toBeNull();
  });
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
