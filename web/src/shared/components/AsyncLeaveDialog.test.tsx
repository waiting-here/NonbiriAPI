import { act, fireEvent, render, screen } from '@testing-library/react';
import { createMemoryRouter, Link, RouterProvider } from 'react-router';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import en from '@shared/i18n/common/en.json';
import { AsyncLeaveDialog } from './AsyncLeaveDialog';

beforeAll(async () => {
  await i18n.use(initReactI18next).init({ resources: { en: { translation: en } }, lng: 'en' });
});
describe('asynchronous route leave', () => {
  it('resets cancelled navigation and proceeds after cleanup without waiting for requests', async () => {
    const onLeave = vi.fn();
    const router = createMemoryRouter([
      {
        path: '/',
        element: (
          <>
            <Link to="/next">Next page</Link>
            <AsyncLeaveDialog dirty onLeave={onLeave} />
          </>
        ),
      },
      { path: '/next', element: <h1>Destination</h1> },
    ]);
    render(<RouterProvider router={router} />);
    fireEvent.click(screen.getByRole('link', { name: 'Next page' }));
    expect(await screen.findByRole('alertdialog')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Stay here' }));
    expect(router.state.location.pathname).toBe('/');
    expect(onLeave).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('link', { name: 'Next page' }));
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Leave page' }));
    });
    expect(await screen.findByRole('heading', { name: 'Destination' })).toBeVisible();
    expect(onLeave).toHaveBeenCalledOnce();
    router.dispose();
  });
  it('preserves the native unload protection', () => {
    const router = createMemoryRouter([{ path: '/', element: <AsyncLeaveDialog dirty /> }]);
    render(<RouterProvider router={router} />);
    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
    router.dispose();
  });
});
