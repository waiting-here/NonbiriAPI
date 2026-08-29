import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, test, vi } from 'vitest';
import { useRouteError } from 'react-router';
import { ApiError } from '../../src/shared/query/http';
import { RouteErrorPage } from '../../src/shared/components/RouteErrorPage';
import { installJsonFetchFixtures, renderWithProviders } from './support';

const publicConfig = {
  site_name: 'Fixture Site',
  site_logo_url: '',
  legal_privacy_override_zh: '',
  legal_privacy_override_en: '',
  legal_terms_override_zh: '',
  legal_terms_override_en: '',
  legal_authoritative_locale: '',
  maintenance_mode: false,
  registration_open: true,
  announcement_epoch: 'b1e_AAAAAAAAAAAAAAAAAAAAAA',
};

vi.mock('react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router')>();
  return { ...actual, useRouteError: vi.fn() };
});

const mockedRouteError = vi.mocked(useRouteError);

beforeEach(() => {
  mockedRouteError.mockReset();
});

describe('route error recovery shell', () => {
  test('uses a safe station home link for 403/404', async () => {
    mockedRouteError.mockReturnValue(new ApiError('forbidden', 'raw implementation detail', 403));
    installJsonFetchFixtures([{ method: 'GET', path: '/api/config', body: publicConfig }]);
    await renderWithProviders(<RouteErrorPage station="user" />, { station: 'user' });

    expect(screen.getByRole('heading', { name: 'Access not allowed' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Back to home' })).toHaveAttribute('href', '/');
    expect(screen.queryByRole('button', { name: 'Reload page' })).not.toBeInTheDocument();
    expect(screen.queryByText('raw implementation detail')).not.toBeInTheDocument();
    expect(document.querySelectorAll('.nb-public-shell')).toHaveLength(1);
  });

  test('gives 500/render/network a bounded reload action', async () => {
    mockedRouteError.mockReturnValue(new ApiError('internal', 'raw server detail', 500));
    installJsonFetchFixtures([{ method: 'GET', path: '/api/config', body: publicConfig }]);
    await renderWithProviders(<RouteErrorPage station="user" />, { station: 'user' });

    expect(screen.getByRole('heading', { name: 'Service error' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reload page' })).toBeInTheDocument();
    expect(screen.queryByText('raw server detail')).not.toBeInTheDocument();
  });

  test('renders an explicit maintenance error in the same public shell without a misleading reload', async () => {
    mockedRouteError.mockReturnValue(new ApiError('maintenance', 'raw maintenance detail', 503));
    installJsonFetchFixtures([{ method: 'GET', path: '/api/config', body: publicConfig }]);
    await renderWithProviders(<RouteErrorPage station="user" />, { station: 'user' });

    expect(screen.getByRole('heading', { name: 'Under maintenance' })).toBeInTheDocument();
    expect(screen.getByText('The site is under maintenance and temporarily unavailable. Please check back later.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reload page' })).not.toBeInTheDocument();
    expect(document.querySelectorAll('.nb-public-shell')).toHaveLength(1);
  });

  test('keeps service_unavailable 503 in the retryable server-error state', async () => {
    mockedRouteError.mockReturnValue(new ApiError('service_unavailable', 'raw unavailable detail', 503));
    installJsonFetchFixtures([{ method: 'GET', path: '/api/config', body: publicConfig }]);
    await renderWithProviders(<RouteErrorPage station="user" />, { station: 'user' });

    expect(screen.getByRole('heading', { name: 'Service error' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reload page' })).toBeInTheDocument();
    expect(screen.queryByText('raw unavailable detail')).not.toBeInTheDocument();
  });

  test('keeps anonymous admin error recovery on built-in branding without protected config', async () => {
    mockedRouteError.mockReturnValue(new ApiError('not_found', 'missing', 404));
    const fetchMock = installJsonFetchFixtures([]);
    await renderWithProviders(<RouteErrorPage station="admin" />, { station: 'admin' });

    expect(screen.getByRole('link', { name: 'Back to home' })).toHaveAttribute('href', '/');
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
