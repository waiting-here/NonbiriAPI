import { act, fireEvent, screen } from '@testing-library/react';
import { Route, Routes, useLocation, useNavigate } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { AnnouncementDetailPage } from './AnnouncementDetailPage';

const id = 'ann_' + 'A'.repeat(22);
const list = '/announcements?page=3&page_size=50';
function LocationProbe() {
  const location = useLocation();
  return <output aria-label="List location">{location.pathname + location.search}</output>;
}
function Harness() {
  const navigate = useNavigate();
  return (
    <>
      <button onClick={() => navigate('/announcements/' + id, { state: { returnTo: list } })}>
        Read notice
      </button>
      <Routes>
        <Route path="/announcements" element={<LocationProbe />} />
        <Route path="/announcements/:announcementId" element={<AnnouncementDetailPage />} />
      </Routes>
    </>
  );
}

describe('user announcement detail return path', () => {
  it('keeps the list return during loading and failure, retries, and returns to the same page', async () => {
    let resolve!: (value: Response) => void;
    const pending = new Promise<Response>((done) => {
      resolve = done;
    });
    const fetchMock = vi
      .fn()
      .mockReturnValueOnce(pending)
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            epoch: 'b1e_' + 'A'.repeat(22),
            id,
            revision: '1',
            title: 'Service notice',
            rendered_body: '<p>Synthetic published notice</p>',
            severity: 'info',
            pinned: false,
            dismissible: true,
            published_at: 1_800_000_000,
            expires_at: null,
            effective_language: 'en',
            fallback_from: null,
          }),
          { headers: { 'Content-Type': 'application/json' } },
        ),
      );
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<Harness />, { station: 'user', route: list });
    await view.user.click(screen.getByRole('button', { name: 'Read notice' }));
    expect(screen.getByRole('status')).toBeVisible();
    expect(screen.getByRole('link', { name: 'Back to announcements' })).toHaveAttribute(
      'href',
      list,
    );
    await act(async () => {
      resolve(
        new Response(
          JSON.stringify({ error: { code: 'not_found', message: 'Notice unavailable' } }),
          {
            status: 404,
            headers: { 'Content-Type': 'application/json' },
          },
        ),
      );
      await pending;
    });
    await screen.findByRole('alert');
    expect(screen.getByRole('link', { name: 'Back to announcements' })).toHaveAttribute(
      'href',
      list,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByRole('heading', { level: 1, name: 'Service notice' });
    expect(screen.getByText('Synthetic published notice')).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    await view.user.click(screen.getByRole('link', { name: 'Back to announcements' }));
    expect(screen.getByLabelText('List location')).toHaveTextContent(list);
  });
});
