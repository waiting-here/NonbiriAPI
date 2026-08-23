import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, type RenderResult } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import i18next, { type i18n } from 'i18next';
import { createContext, useContext, type ReactElement, type ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { MemoryRouter } from 'react-router';
import { vi, type Mock } from 'vitest';
import adminEn from '../../src/admin/i18n/en.json';
import adminZh from '../../src/admin/i18n/zh.json';
import commonEn from '../../src/shared/i18n/common/en.json';
import commonZh from '../../src/shared/i18n/common/zh.json';
import userEn from '../../src/user/i18n/en.json';
import userZh from '../../src/user/i18n/zh.json';
import { ThemeContext, type Theme } from '@shared/theme/context';

export type TestStation = 'admin' | 'user';
export type TestRole = 'anonymous' | 'user' | 'level4' | 'level5' | 'admin';
export type TestLocale = 'zh' | 'en';

interface RenderOptions {
  station: TestStation;
  route?: string;
  locale?: TestLocale;
  theme?: Exclude<Theme, 'system'>;
  role?: TestRole;
}

interface RenderWithProvidersResult extends RenderResult {
  i18n: i18n;
  queryClient: QueryClient;
  user: UserEvent;
}

const RoleContext = createContext<TestRole>('anonymous');
const stationCatalogs = {
  admin: { en: adminEn, zh: adminZh },
  user: { en: userEn, zh: userZh },
} as const;

interface ManagedProviders {
  i18n: i18n;
  queryClient: QueryClient;
}

const activeProviders = new Set<ManagedProviders>();

export function useTestRole(): TestRole {
  return useContext(RoleContext);
}

function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0, gcTime: Number.POSITIVE_INFINITY },
      mutations: { retry: false, gcTime: 0 },
    },
  });
}

async function createTestI18n(locale: TestLocale, station: TestStation): Promise<i18n> {
  const instance = i18next.createInstance();
  const stationCatalog = stationCatalogs[station];
  const en = structuredClone({
    ...commonEn,
    ...stationCatalog.en,
    fixture: { title: `${station} test station`, apiReady: 'API ready' },
  });
  const zh = structuredClone({
    ...commonZh,
    ...stationCatalog.zh,
    fixture: { title: `${station} 测试站`, apiReady: '接口就绪' },
  });
  await instance.use(initReactI18next).init({
    resources: {
      en: { translation: en },
      zh: { translation: zh },
    },
    lng: locale,
    fallbackLng: 'en',
    interpolation: { escapeValue: false },
    react: { useSuspense: false },
    initAsync: false,
  });
  return instance;
}

export async function disposeTestProviders(): Promise<void> {
  const providers = [...activeProviders];
  activeProviders.clear();
  let firstError: unknown;
  for (const provider of providers) {
    try {
      await provider.queryClient.cancelQueries();
    } catch (error) {
      firstError ??= error;
    }
    try {
      provider.queryClient.clear();
    } catch (error) {
      firstError ??= error;
    }
    try {
      provider.i18n.off('languageChanged');
      provider.i18n.off('loaded');
    } catch (error) {
      firstError ??= error;
    }
  }
  if (firstError) throw firstError;
}

export interface QueryCacheScanResult {
  bytesRead: number;
  hitSurfaces: string[];
  mutationCount: number;
  overflow: boolean;
  queryCount: number;
  unsupportedValue: boolean;
}

export function scanQueryClientForTokens(
  queryClient: QueryClient,
  forbiddenTokens: readonly string[],
  byteLimit = 128 * 1024,
): QueryCacheScanResult {
  if (
    forbiddenTokens.length === 0 ||
    forbiddenTokens.some((token) => token.length < 8) ||
    !Number.isSafeInteger(byteLimit) ||
    byteLimit < 1
  ) {
    throw new Error('Query-cache scans require synthetic tokens and a positive byte limit.');
  }
  const encoder = new TextEncoder();
  const hitSurfaces = new Set<string>();
  let bytesRead = 0;
  let overflow = false;
  let unsupportedValue = false;
  const scan = (surface: string, value: unknown) => {
    const seen = new WeakSet<object>();
    const scanString = (text: string) => {
      if (overflow) return;
      for (const character of text) {
        bytesRead += encoder.encode(character).byteLength;
        if (bytesRead > byteLimit) {
          overflow = true;
          return;
        }
      }
      if (forbiddenTokens.some((token) => text.includes(token))) hitSurfaces.add(surface);
    };
    const visit = (nested: unknown): void => {
      if (overflow) return;
      if (nested === null || nested === undefined) {
        scanString(String(nested));
        return;
      }
      if (typeof nested === 'string') {
        scanString(nested);
        return;
      }
      if (typeof nested === 'number' || typeof nested === 'boolean' || typeof nested === 'bigint') {
        scanString(String(nested));
        return;
      }
      if (typeof nested === 'function' || typeof nested === 'symbol') {
        unsupportedValue = true;
        return;
      }
      if (seen.has(nested)) return;
      seen.add(nested);
      if (nested instanceof Error) {
        let cause: unknown;
        let keys: string[];
        try {
          scanString(nested.name);
          scanString(nested.message);
          cause = nested.cause;
          keys = Object.keys(nested);
        } catch {
          unsupportedValue = true;
          return;
        }
        visit(cause);
        for (const key of keys) {
          scanString(key);
          if (key === 'cause') continue;
          try {
            visit((nested as Error & Record<string, unknown>)[key]);
          } catch {
            unsupportedValue = true;
          }
        }
        return;
      }
      if (nested instanceof Date || nested instanceof URL) {
        scanString(nested.toString());
        return;
      }
      if (Array.isArray(nested)) {
        for (const entry of nested) visit(entry);
        return;
      }
      let prototype: object | null;
      let keys: string[];
      try {
        prototype = Object.getPrototypeOf(nested);
        keys = Object.keys(nested);
      } catch {
        unsupportedValue = true;
        return;
      }
      if (prototype !== Object.prototype && prototype !== null) {
        unsupportedValue = true;
        return;
      }
      for (const key of keys) {
        scanString(key);
        try {
          visit((nested as Record<string, unknown>)[key]);
        } catch {
          unsupportedValue = true;
        }
      }
    };
    visit(value);
  };

  const queries = queryClient.getQueryCache().getAll();
  for (const query of queries) {
    scan('query:key', query.queryKey);
    scan('query:meta', query.options.meta);
    scan('query:data', query.state.data);
    scan('query:error', query.state.error);
    scan('query:fetch-meta', query.state.fetchMeta);
    scan('query:fetch-failure-reason', query.state.fetchFailureReason);
  }
  const mutations = queryClient.getMutationCache().getAll();
  for (const mutation of mutations) {
    scan('mutation:key', mutation.options.mutationKey);
    scan('mutation:meta', mutation.options.meta);
    scan('mutation:data', mutation.state.data);
    scan('mutation:error', mutation.state.error);
    scan('mutation:failure-reason', mutation.state.failureReason);
    scan('mutation:variables', mutation.state.variables);
    scan('mutation:context', mutation.state.context);
  }
  return {
    bytesRead,
    hitSurfaces: [...hitSurfaces],
    mutationCount: mutations.length,
    overflow,
    queryCount: queries.length,
    unsupportedValue,
  };
}

export function assertNoSensitiveQueryCache(
  queryClient: QueryClient,
  forbiddenTokens: readonly string[],
  byteLimit?: number,
): QueryCacheScanResult {
  const result = scanQueryClientForTokens(queryClient, forbiddenTokens, byteLimit);
  if (result.overflow) throw new Error('Query-cache scan exceeded its byte budget.');
  if (result.unsupportedValue) throw new Error('Query-cache scan found an unsupported value.');
  if (result.hitSurfaces.length > 0) {
    throw new Error(
      `Synthetic marker reached query-cache surfaces: ${result.hitSurfaces.join(', ')}.`,
    );
  }
  return result;
}

export async function renderWithProviders(
  ui: ReactElement,
  { station, route = '/', locale = 'en', theme = 'light', role = 'anonymous' }: RenderOptions,
): Promise<RenderWithProvidersResult> {
  const queryClient = createTestQueryClient();
  const testI18n = await createTestI18n(locale, station);
  activeProviders.add({ i18n: testI18n, queryClient });
  document.documentElement.lang = locale === 'zh' ? 'zh-CN' : 'en';
  document.documentElement.dataset.theme = theme;

  const setTheme = () => undefined;
  const wrapper = ({ children }: { children: ReactNode }) => (
    <I18nextProvider i18n={testI18n}>
      <ThemeContext.Provider value={{ theme, setTheme, toggleTheme: setTheme }}>
        <QueryClientProvider client={queryClient}>
          <RoleContext.Provider value={role}>
            <MemoryRouter initialEntries={[route]}>{children}</MemoryRouter>
          </RoleContext.Provider>
        </QueryClientProvider>
      </ThemeContext.Provider>
    </I18nextProvider>
  );

  const result = render(ui, { wrapper });
  return { ...result, i18n: testI18n, queryClient, user: userEvent.setup() };
}

export interface JsonFetchFixture {
  method: string;
  path: string;
  body: unknown;
  status?: number;
}

function canonicalMethod(method: string): string {
  const canonical = method.trim().toUpperCase();
  if (!/^[A-Z]+$/.test(canonical)) throw new Error('Fixture methods must be HTTP tokens.');
  return canonical;
}

function fixtureTarget(path: string): URL {
  if (!path.startsWith('/')) throw new Error('Fixture paths must be same-origin absolute paths.');
  const parsed = new URL(path, window.location.origin);
  if (parsed.origin !== window.location.origin || parsed.hash) {
    throw new Error('Fixture paths must not contain an origin or fragment.');
  }
  return parsed;
}

function fixtureKey(method: string, target: URL): string {
  return `${method} ${target.pathname}${target.search}`;
}

function fixtureRequestLabel(method: string, target: URL): string {
  const queryMetadata = target.search
    ? `query-present length=${target.search.length - 1}`
    : 'query-absent';
  return `${method} ${target.pathname} (${queryMetadata})`;
}

export function installJsonFetchFixtures(fixtures: readonly JsonFetchFixture[]): Mock {
  const registry = new Map<string, JsonFetchFixture>();
  for (const fixture of fixtures) {
    const method = canonicalMethod(fixture.method);
    const target = fixtureTarget(fixture.path);
    const key = fixtureKey(method, target);
    if (registry.has(key)) {
      throw new Error(`Duplicate test fixture: ${fixtureRequestLabel(method, target)}.`);
    }
    registry.set(key, fixture);
  }

  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const method = canonicalMethod(
      init?.method ?? (input instanceof Request ? input.method : 'GET'),
    );
    const rawURL = input instanceof Request ? input.url : String(input);
    const parsed = new URL(rawURL, window.location.origin);
    const fixture =
      parsed.origin === window.location.origin
        ? registry.get(fixtureKey(method, parsed))
        : undefined;
    if (!fixture) {
      throw new Error(`Unregistered test request: ${fixtureRequestLabel(method, parsed)}.`);
    }
    return new Response(JSON.stringify(fixture.body), {
      status: fixture.status ?? 200,
      headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
    });
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}
