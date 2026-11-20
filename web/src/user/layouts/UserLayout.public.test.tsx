import { fireEvent, screen } from '@testing-library/react';
import { Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { NoticePage } from '@shared/components/States';
import { CredentialReportPage } from '../pages/CredentialReportPage';
import { UserLayout } from './UserLayout';

const bootstrap = vi.hoisted(() => ({
  session: undefined as { user: { id: string } } | null | undefined,
}));
vi.mock('../data', async (original) => ({
  ...(await original<typeof import('../data')>()),
  useUserSession: () => ({ data: bootstrap.session }),
}));
vi.mock('@shared/query/publicConfig', async (original) => ({
  ...(await original<typeof import('@shared/query/publicConfig')>()),
  usePublicConfig: () => ({
    data: { siteName: 'Fixture', siteLogoURL: '', maintenanceMode: false },
  }),
}));

function page(path: string) {
  return (
    <Routes>
      <Route element={<UserLayout />}>
        <Route
          path={path}
          element={
            path === '/report' ? (
              <CredentialReportPage />
            ) : (
              <NoticePage titleKey="common.maintenanceTitle" bodyKey="common.maintenanceBody" />
            )
          }
        />
      </Route>
    </Routes>
  );
}

describe('public pages during session resolution', () => {
  beforeEach(() => {
    bootstrap.session = undefined;
  });

  it.each(['/privacy', '/terms', '/maintenance', '/registration-closed'])(
    'keeps the public outlet at %s mounted when the account resolves',
    async (path) => {
      const view = await renderWithProviders(page(path), { station: 'user', route: path });
      const art = view.container.querySelector('.nb-state__art svg');
      expect(art).toBeInTheDocument();
      bootstrap.session = { user: { id: '1' } };
      view.rerender(page(path));
      expect(view.container.querySelector('.nb-state__art svg')).toBe(art);
    },
  );

  it.each(['signed in', 'anonymous'])(
    'adopts the first %s report draft and clears it across account boundaries',
    async (initial) => {
      const view = await renderWithProviders(page('/report'), {
        station: 'user',
        route: '/report',
      });
      const secret = () =>
        view.container.querySelector<HTMLTextAreaElement>('textarea[name="secret"]')!;
      const first = secret();
      fireEvent.change(first, { target: { value: 'draft before first session' } });
      bootstrap.session = initial === 'anonymous' ? null : { user: { id: '1' } };
      view.rerender(page('/report'));
      expect(secret()).toBe(first);
      expect(secret()).toHaveValue('draft before first session');
      bootstrap.session = { user: { id: '2' } };
      view.rerender(page('/report'));
      expect(secret()).not.toBe(first);
      expect(secret()).toHaveValue('');
      fireEvent.change(secret(), { target: { value: 'second account draft' } });
      bootstrap.session = null;
      view.rerender(page('/report'));
      expect(secret()).toHaveValue('');
      expect(screen.getByRole('heading', { level: 1 })).toBeVisible();
    },
  );
});
