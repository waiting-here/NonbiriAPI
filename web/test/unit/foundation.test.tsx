import { useState } from 'react';
import { screen, waitFor } from '@testing-library/react';
import { useTranslation } from 'react-i18next';
import { useLocation } from 'react-router';
import { describe, expect, test, vi } from 'vitest';
import { apiFetch, isApiError } from '@shared/query/http';
import { useTheme } from '@shared/theme/useTheme';
import {
  assertNoSensitiveQueryCache,
  disposeTestProviders,
  installJsonFetchFixtures,
  renderWithProviders,
  scanQueryClientForTokens,
  useTestRole,
  type TestLocale,
  type TestRole,
  type TestStation,
} from './support';

function FoundationProbe({ station }: { station: TestStation }) {
  const { t } = useTranslation();
  const location = useLocation();
  const { theme } = useTheme();
  const role = useTestRole();
  const [apiState, setApiState] = useState('idle');

  const load = async () => {
    try {
      await apiFetch('/fixture/status');
      setApiState(t('fixture.apiReady'));
    } catch (error) {
      setApiState(isApiError(error) ? `${error.code}:${error.status}` : 'unexpected_error');
    }
  };

  return (
    <main data-station={station} data-theme={theme}>
      <h1>{t('fixture.title')}</h1>
      <p data-testid="route">{location.pathname}</p>
      <p data-testid="role">{role}</p>
      <p data-testid="shared-catalog">{t('common.save')}</p>
      <p data-testid="station-catalog">
        {t(station === 'admin' ? 'admin.dashboard.title' : 'user.home.title')}
      </p>
      <button type="button" onClick={() => void load()}>
        Load fixture API
      </button>
      <p role="status">{apiState}</p>
    </main>
  );
}

describe('React test foundation', () => {
  test.each<{
    station: TestStation;
    locale: TestLocale;
    theme: 'light' | 'dark';
    role: TestRole;
    route: string;
    title: string;
    sharedCatalog: string;
    stationCatalog: string;
  }>([
    {
      station: 'user',
      locale: 'zh',
      theme: 'dark',
      role: 'level4',
      route: '/models',
      title: 'user 测试站',
      sharedCatalog: '保存',
      stationCatalog: '你的工作区',
    },
    {
      station: 'admin',
      locale: 'en',
      theme: 'light',
      role: 'admin',
      route: '/settings',
      title: 'admin test station',
      sharedCatalog: 'Save',
      stationCatalog: 'Operations overview',
    },
  ])('renders the $station station providers without production singletons', async (fixture) => {
    await renderWithProviders(<FoundationProbe station={fixture.station} />, fixture);

    expect(screen.getByRole('heading', { name: fixture.title })).toBeInTheDocument();
    expect(screen.getByTestId('route')).toHaveTextContent(fixture.route);
    expect(screen.getByTestId('role')).toHaveTextContent(fixture.role);
    expect(screen.getByTestId('shared-catalog')).toHaveTextContent(fixture.sharedCatalog);
    expect(screen.getByTestId('station-catalog')).toHaveTextContent(fixture.stationCatalog);
    expect(screen.getByRole('main')).toHaveAttribute('data-theme', fixture.theme);
    expect(document.documentElement.lang).toBe(fixture.locale === 'zh' ? 'zh-CN' : 'en');
  });

  test('exercises keyboard input and a bounded API error fixture', async () => {
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/fixture/status',
        status: 503,
        body: { error: { code: 'fixture_unavailable', message: 'Synthetic failure.' } },
      },
    ]);
    const { user } = await renderWithProviders(<FoundationProbe station="user" />, {
      station: 'user',
    });

    await user.tab();
    expect(screen.getByRole('button', { name: 'Load fixture API' })).toHaveFocus();
    await user.keyboard('{Enter}');
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent('fixture_unavailable:503'),
    );
  });

  test('matches unit API fixtures by same-origin method and exact pathname/search', async () => {
    const unregisteredQuery = 'mode=two-sensitive-query-marker';
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/fixture/exact?mode=one',
        body: { result: 'exact' },
      },
    ]);

    await expect(
      fetch('/fixture/exact?mode=one').then((response) => response.json()),
    ).resolves.toEqual({
      result: 'exact',
    });
    const wrongQueryError = await fetch(`/fixture/exact?${unregisteredQuery}`).then(
      () => '',
      (error: unknown) => (error instanceof Error ? error.message : String(error)),
    );
    expect(wrongQueryError).toBe(
      `Unregistered test request: GET /fixture/exact (query-present length=${unregisteredQuery.length}).`,
    );
    expect(wrongQueryError).not.toContain(unregisteredQuery);

    const wrongMethodError = await fetch('/fixture/exact?mode=one', { method: 'POST' }).then(
      () => '',
      (error: unknown) => (error instanceof Error ? error.message : String(error)),
    );
    expect(wrongMethodError).toBe(
      'Unregistered test request: POST /fixture/exact (query-present length=8).',
    );
    expect(wrongMethodError).not.toContain('mode=one');

    const externalOriginError = await fetch('https://external.test/fixture/exact?mode=one').then(
      () => '',
      (error: unknown) => (error instanceof Error ? error.message : String(error)),
    );
    expect(externalOriginError).toBe(
      'Unregistered test request: GET /fixture/exact (query-present length=8).',
    );
    expect(externalOriginError).not.toContain('mode=one');

    const duplicateQuery = 'duplicate-sensitive-query-marker';
    let duplicateError = '';
    try {
      installJsonFetchFixtures([
        { method: 'GET', path: `/fixture/duplicate?${duplicateQuery}`, body: {} },
        { method: 'GET', path: `/fixture/duplicate?${duplicateQuery}`, body: {} },
      ]);
    } catch (error) {
      duplicateError = error instanceof Error ? error.message : String(error);
    }
    expect(duplicateError).toBe(
      `Duplicate test fixture: GET /fixture/duplicate (query-present length=${duplicateQuery.length}).`,
    );
    expect(duplicateError).not.toContain(duplicateQuery);
  });

  test('keeps real station catalogs isolated in one process', async () => {
    const admin = await renderWithProviders(<FoundationProbe station="admin" />, {
      station: 'admin',
    });
    const user = await renderWithProviders(<FoundationProbe station="user" />, {
      station: 'user',
    });

    expect(admin.i18n).not.toBe(user.i18n);
    expect(admin.queryClient).not.toBe(user.queryClient);
    expect(admin.i18n.t('common.save')).toBe('Save');
    expect(admin.i18n.t('admin.dashboard.title')).toBe('Operations overview');
    expect(user.i18n.t('common.save')).toBe('Save');
    expect(user.i18n.t('user.home.title')).toBe('Your workspace');
    expect(user.i18n.exists('admin.dashboard.title')).toBe(false);
  });

  test('cancels and clears QueryClient state and releases i18n listeners', async () => {
    const rendered = await renderWithProviders(<FoundationProbe station="user" />, {
      station: 'user',
    });
    rendered.queryClient.setQueryData(['fixture'], { value: 'cached' });
    let languageEvents = 0;
    rendered.i18n.on('languageChanged', () => {
      languageEvents += 1;
    });
    const cancelSpy = vi.spyOn(rendered.queryClient, 'cancelQueries');
    const clearSpy = vi.spyOn(rendered.queryClient, 'clear');
    const offSpy = vi.spyOn(rendered.i18n, 'off');

    await disposeTestProviders();

    expect(cancelSpy).toHaveBeenCalledOnce();
    expect(clearSpy).toHaveBeenCalledOnce();
    expect(offSpy).toHaveBeenCalledTimes(2);
    expect(offSpy).toHaveBeenCalledWith('languageChanged');
    expect(offSpy).toHaveBeenCalledWith('loaded');
    expect(rendered.queryClient.getQueryCache().getAll()).toHaveLength(0);
    await rendered.i18n.changeLanguage('zh');
    expect(languageEvents).toBe(0);
  });

  test('scans actual query and mutation caches with bounded marker evidence', async () => {
    const marker = 'synthetic-query-cache-marker';
    const rendered = await renderWithProviders(<FoundationProbe station="user" />, {
      station: 'user',
    });
    const queryFailure = new Error('Synthetic query failure.', {
      cause: { marker },
    });
    const query = rendered.queryClient.getQueryCache().build(rendered.queryClient, {
      queryKey: ['fixture-query'],
      queryFn: async () => ({ value: 'safe-query-data' }),
      meta: { marker },
    });
    query.setState({
      data: { value: marker },
      fetchFailureReason: queryFailure,
      fetchMeta: { fetchMore: { direction: marker as 'forward' } },
    });
    const mutationFailure = Object.assign(new Error('Synthetic mutation failure.'), {
      details: { marker },
    });
    const mutation = rendered.queryClient.getMutationCache().build(rendered.queryClient, {
      mutationKey: ['fixture-mutation'],
      mutationFn: async () => {
        throw mutationFailure;
      },
      meta: { marker },
    });
    await expect(mutation.execute('synthetic-safe-value')).rejects.toThrow(
      'Synthetic mutation failure.',
    );
    const markedResult = scanQueryClientForTokens(rendered.queryClient, [marker]);
    expect(markedResult.hitSurfaces).toEqual(
      expect.arrayContaining([
        'query:meta',
        'query:data',
        'query:fetch-meta',
        'query:fetch-failure-reason',
        'mutation:meta',
        'mutation:failure-reason',
      ]),
    );
    expect(() => assertNoSensitiveQueryCache(rendered.queryClient, [marker])).toThrow(
      'Synthetic marker reached query-cache surfaces',
    );

    rendered.queryClient.clear();
    rendered.queryClient.setQueryData(['safe-query'], { value: 'synthetic-safe-value' });
    const safeMutation = rendered.queryClient.getMutationCache().build(rendered.queryClient, {
      mutationKey: ['safe-mutation'],
      mutationFn: async (value: string) => ({ value }),
    });
    await safeMutation.execute('synthetic-safe-value');
    const result = assertNoSensitiveQueryCache(rendered.queryClient, [marker]);
    expect(result.queryCount).toBe(1);
    expect(result.mutationCount).toBe(1);
    expect(result.bytesRead).toBeGreaterThan(0);
  });
});
